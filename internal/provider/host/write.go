package host

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/gkdis6/opsgate/internal/tool"
)

type restartInput struct {
	tool.Approval
	Unit string `json:"unit" jsonschema:"Required unit allowed by restart_units; .service is appended when omitted; maximum 256 bytes."`
}

func validatePrefix(prefix []string) error {
	for i, arg := range prefix {
		if strings.ContainsAny(arg, "\x00\r\n") || len(arg) > 4096 || (i == 0 && !filepath.IsAbs(arg)) || (!filepath.IsAbs(arg) && !strings.HasPrefix(arg, "-")) {
			return fmt.Errorf("invalid command_prefix: first element must be an absolute executable path; remaining elements must be paths or flags")
		}
	}
	return nil
}

func (p *provider) restartUnit(raw string) (string, error) {
	unit, err := normalizeUnit(raw)
	if err != nil {
		return "", err
	}
	for _, pattern := range p.cfg.RestartUnits {
		pattern, err = normalizePattern(pattern)
		if err != nil {
			continue
		}
		if ok, _ := path.Match(pattern, unit); ok {
			return unit, nil
		}
	}
	return "", tool.Denied("unit is not allowed")
}

func (p *provider) restartState(ctx context.Context, unit string) (string, error) {
	rows, err := p.properties(ctx, []string{unit}, false)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0]["LoadState"] == "not-found" || rows[0]["ActiveState"] == "" || rows[0]["SubState"] == "" {
		return "", fmt.Errorf("unit state unavailable")
	}
	for _, key := range []string{"ActiveState", "SubState", "ActiveEnterTimestamp"} {
		value := rows[0][key]
		if len(value) > 128 || strings.ContainsFunc(value, unicode.IsControl) {
			return "", fmt.Errorf("invalid unit state")
		}
	}
	if !unitName.MatchString(rows[0]["ActiveState"]) || !unitName.MatchString(rows[0]["SubState"]) {
		return "", fmt.Errorf("invalid unit state")
	}
	return fmt.Sprintf("%s/%s, since %s", rows[0]["ActiveState"], rows[0]["SubState"], rows[0]["ActiveEnterTimestamp"]), nil
}

func (p *provider) planRestart(ctx context.Context, _ tool.Caller, in restartInput) (tool.Plan, error) {
	unit, err := p.restartUnit(in.Unit)
	if err != nil {
		return tool.Plan{}, err
	}
	if err = validatePrefix(p.cfg.CommandPrefix); err != nil {
		return tool.Plan{}, err
	}
	state, err := p.restartState(ctx, unit)
	if err != nil {
		return tool.Plan{}, err
	}
	hostname, err := os.Hostname()
	if err != nil || len(hostname) > 253 || strings.ContainsFunc(hostname, unicode.IsControl) {
		return tool.Plan{}, fmt.Errorf("hostname unavailable")
	}
	return tool.Plan{Summary: fmt.Sprintf("Restart systemd unit %s on host %s (currently %s)", unit, hostname, state)}, nil
}

func (p *provider) applyRestart(ctx context.Context, _ tool.Caller, in restartInput, _ tool.Plan) (string, error) {
	unit, err := p.restartUnit(in.Unit)
	if err != nil {
		return "", err
	}
	if err = validatePrefix(p.cfg.CommandPrefix); err != nil {
		return "", err
	}
	resolve := p.resolve
	if resolve == nil {
		resolve = trustedBinary
	}
	binary, err := resolve("systemctl")
	if err != nil {
		return "", err
	}
	argv := append(append([]string{}, p.cfg.CommandPrefix...), binary, "restart", "--", unit)
	if _, err = p.run(ctx, argv[0], argv[1:]...); err != nil {
		return "", err
	}
	state, err := p.restartState(ctx, unit)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Restarted %s: %s", unit, state), nil
}
