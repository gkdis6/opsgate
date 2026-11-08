package host

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/gkdis6/opsgate/internal/hosttarget"
	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

// maxUnits bounds how many allowlisted units one call reads.
const maxUnits = 100

type unitState struct {
	Unit      string `json:"unit"`
	Load      string `json:"load"`
	Active    string `json:"active"`
	Sub       string `json:"sub"`
	Result    string `json:"result,omitempty"`
	NRestarts uint64 `json:"restarts"`
	Since     string `json:"since,omitempty"`
}

// failed reports whether the unit is in a state a restart may fix.
func (u unitState) failed() bool {
	return u.Active == "failed" || u.Result != "" && u.Result != "success"
}

type unitInput struct {
	Unit string `json:"unit" jsonschema:"Required allowlisted systemd unit name, e.g. app.service"`
}

type unitLogsInput struct {
	Unit  string `json:"unit" jsonschema:"Required allowlisted systemd unit name"`
	Lines int    `json:"lines,omitempty" jsonschema:"Journal lines to return; default 100, maximum 500"`
}

type restartUnitInput struct {
	tool.Approval
	Unit string `json:"unit" jsonschema:"Required systemd unit name allowed by providers.host.systemd.restart"`
}

// allowedUnit validates a concrete unit name against an allowlist.
func (p *provider) allowedUnit(name string, patterns []string) error {
	if !hosttarget.Valid("unit", name) {
		return tool.Denied("invalid systemd unit name")
	}
	if !hosttarget.Match(p.cfg.Systemd.Units, name) || !hosttarget.Match(patterns, name) {
		return tool.Denied("systemd unit %s is not allowed", name)
	}
	return nil
}

// unitNames lists loaded units that match the allowlist, in name order.
func (p *provider) unitNames(ctx context.Context) ([]string, error) {
	out, err := p.run(ctx, systemctl, "list-units", "--all", "--plain", "--no-legend", "--no-pager", "--full")
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) == 0 || !hosttarget.Valid("unit", fields[0]) || !hosttarget.Match(p.cfg.Systemd.Units, fields[0]) {
			continue
		}
		if len(names) == maxUnits {
			return nil, tool.Denied("more than %d allowlisted units; narrow providers.host.systemd.units", maxUnits)
		}
		names = append(names, fields[0])
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// showUnits reads the state of concrete, already validated unit names.
func (p *provider) showUnits(ctx context.Context, names []string) ([]unitState, error) {
	if len(names) == 0 {
		return nil, nil
	}
	args := append([]string{"show", "--no-pager", "--property=Id,LoadState,ActiveState,SubState,Result,NRestarts,StateChangeTimestamp", "--"}, names...)
	out, err := p.run(ctx, systemctl, args...)
	if err != nil {
		return nil, err
	}
	var units []unitState
	for block := range strings.SplitSeq(strings.TrimSpace(out), "\n\n") {
		var u unitState
		for line := range strings.Lines(block) {
			key, value, ok := strings.Cut(strings.TrimRight(line, "\n"), "=")
			if !ok {
				continue
			}
			switch key {
			case "Id":
				u.Unit = value
			case "LoadState":
				u.Load = value
			case "ActiveState":
				u.Active = value
			case "SubState":
				u.Sub = value
			case "Result":
				u.Result = value
			case "NRestarts":
				u.NRestarts, _ = strconv.ParseUint(value, 10, 64)
			case "StateChangeTimestamp":
				u.Since = value
			}
		}
		// systemctl output is host-controlled text; keep only known units.
		if slices.Contains(names, u.Unit) {
			units = append(units, u)
		}
	}
	return units, nil
}

func (p *provider) units(ctx context.Context) ([]unitState, error) {
	names, err := p.unitNames(ctx)
	if err != nil {
		return nil, err
	}
	return p.showUnits(ctx, names)
}

func (p *provider) unit(ctx context.Context, name string) (unitState, error) {
	units, err := p.showUnits(ctx, []string{name})
	if err != nil {
		return unitState{}, err
	}
	if len(units) != 1 || units[0].Load != "loaded" {
		return unitState{}, tool.Denied("systemd unit %s is not loaded", name)
	}
	return units[0], nil
}

func (p *provider) unitLogs(ctx context.Context, _ tool.Caller, in unitLogsInput) (string, error) {
	if err := p.allowedUnit(in.Unit, p.cfg.Systemd.Units); err != nil {
		return "", err
	}
	lines := boundedLines(in.Lines)
	// Read extra lines and mask before cutting, so a multi-line secret that
	// starts above the cut is still recognised.
	out, err := p.run(ctx, journalctl, "--unit="+in.Unit, "--lines="+strconv.Itoa(lines+50), "--no-pager", "--quiet", "--output=short-iso", "--no-hostname")
	if err != nil {
		return "", err
	}
	return lastLines(redact.String(out), lines), nil
}

func (p *provider) planRestartUnit(ctx context.Context, _ tool.Caller, in restartUnitInput) (tool.Plan, error) {
	if err := p.allowedUnit(in.Unit, p.cfg.Systemd.Restart); err != nil {
		return tool.Plan{}, err
	}
	if _, err := p.unit(ctx, in.Unit); err != nil {
		return tool.Plan{}, err
	}
	// The state is left out on purpose: a crash-looping unit changes state
	// between planning and approval, which would void every approval.
	return tool.Plan{Summary: fmt.Sprintf("Restart systemd unit %s on this host (systemctl restart). The unit stops and starts again; check host_units afterwards.", in.Unit), Target: "host"}, nil
}

func (p *provider) applyRestartUnit(ctx context.Context, _ tool.Caller, in restartUnitInput, _ tool.Plan) (string, error) {
	if err := p.allowedUnit(in.Unit, p.cfg.Systemd.Restart); err != nil {
		return "", err
	}
	if _, err := p.run(ctx, systemctl, "--no-ask-password", "restart", "--", in.Unit); err != nil {
		return "", err
	}
	return fmt.Sprintf("Restarted systemd unit %s; check host_units for its state.", in.Unit), nil
}

func boundedLines(n int) int {
	switch {
	case n <= 0:
		return 100
	case n > 500:
		return 500
	}
	return n
}

func lastLines(text string, n int) string {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "")
}
