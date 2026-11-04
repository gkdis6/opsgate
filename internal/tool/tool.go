// Package tool is the only way providers expose MCP tools.
//
// Add wraps every handler with the same guard rails: scope check, timeout,
// output redaction and truncation, and an audit record. Providers return
// plain text and never touch mcp.CallToolResult themselves.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/redact"
)

// Caller is the authenticated identity behind a tool call.
type Caller struct {
	User      string
	Transport string
	Scopes    []string
	// Tools optionally restricts this identity to matching tool-name globs.
	Tools []string
}

// Has reports whether the caller has scope.
func (c Caller) Has(scope string) bool { return slices.Contains(c.Scopes, scope) }

// Env is shared by all tools of one server.
type Env struct {
	Audit          *audit.Logger
	MaxOutputBytes int
	Timeout        time.Duration
	// Fallback is the caller used when the request carries no token
	// (stdio). HTTP transports must always authenticate.
	Fallback *Caller
	// Confirm obtains human approval for write tools. Nil denies all writes.
	Confirm Confirmer
	// Policy filters tools during registration. Nil allows every tool.
	Policy *Policy
	// direct holds writes a person may run from a browser approval page.
	direct map[string]*Direct
}

// Spec describes a tool. Scope defaults to ops:read.
type Spec struct {
	Name        string
	Title       string
	Description string
	// ReadOnly must be true for every tool that does not change state.
	ReadOnly bool
	Scope    string
}

// Handler implements a tool. The returned text is redacted and truncated
// by Add before reaching the client.
type Handler[In any] func(ctx context.Context, c Caller, in In) (string, error)

// ErrDenied marks policy refusals (allowlist misses etc.) so that they are
// audited as "denied" rather than "error".
var ErrDenied = errors.New("denied by policy")

const maxArgumentBytes = 16 << 10

// Denied returns an error wrapping ErrDenied.
func Denied(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDenied, fmt.Sprintf(format, args...))
}

// Add registers h on s with env's guard rails.
func Add[In any](s *mcp.Server, env *Env, spec Spec, h Handler[In]) {
	if !env.Policy.register(spec.Name) {
		return
	}
	if spec.Scope == "" {
		spec.Scope = config.ScopeRead
	}
	destructive := !spec.ReadOnly
	t := &mcp.Tool{
		Name:        spec.Name,
		Title:       spec.Title,
		Description: spec.Description,
		Annotations: &mcp.ToolAnnotations{
			Title:           spec.Title,
			ReadOnlyHint:    spec.ReadOnly,
			DestructiveHint: &destructive,
		},
	}
	mcp.AddTool(s, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		ev := audit.Event{Time: start, Tool: spec.Name}

		c, ok := env.caller(req)
		if ok {
			ev.User, ev.Transport = c.User, c.Transport
		}
		text, err := func() (string, error) {
			if len(req.Params.Arguments) > maxArgumentBytes {
				return "", fmt.Errorf("arguments exceed 16 KiB limit")
			}
			ev.Args = argsOf(in)
			if !ok {
				return "", Denied("unauthenticated")
			}
			if !c.allowsTool(spec.Name) {
				return "", Denied("tool not allowed for this user")
			}
			if !c.Has(spec.Scope) {
				return "", Denied("missing scope %s", spec.Scope)
			}
			ctx, cancel := context.WithTimeout(ctx, env.Timeout)
			defer cancel()
			return h(ctx, c, in)
		}()

		ev.DurationMS = time.Since(start).Milliseconds()
		if err != nil {
			ev.Outcome = "error"
			if errors.Is(err, ErrDenied) {
				ev.Outcome = "denied"
			}
			ev.Error = outputText(err.Error(), env.MaxOutputBytes)
			env.Audit.Record(ev)
			return errorResult(ev.Error), nil, nil
		}
		text = outputText(text, env.MaxOutputBytes)
		ev.Outcome, ev.OutputSize = "ok", len(text)
		env.Audit.Record(ev)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	})
}

func (env *Env) caller(req *mcp.CallToolRequest) (Caller, bool) {
	if req != nil && req.Extra != nil && req.Extra.TokenInfo != nil {
		ti := req.Extra.TokenInfo
		var tools []string
		if raw, exists := ti.Extra["tools"]; exists {
			var ok bool
			tools, ok = raw.([]string)
			if !ok {
				return Caller{}, false
			}
		}
		return Caller{User: ti.UserID, Transport: "http", Scopes: slices.Clone(ti.Scopes), Tools: slices.Clone(tools)}, true
	}
	if env.Fallback != nil {
		return *env.Fallback, true
	}
	return Caller{}, false
}

func argsOf(in any) map[string]any {
	b, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	v, _ := redact.Value(m).(map[string]any)
	return v
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	const note = "\n... [truncated by opsgate]"
	cut := max - len(note)
	if cut < 0 {
		return note[:max]
	}
	// Do not split a UTF-8 sequence.
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + note
}

const maxProviderOutputBytes = 4 << 20

func outputText(s string, max int) string {
	if len(s) > maxProviderOutputBytes {
		// Discard oversized output: clipping could remove a secret's terminator.
		s = "[output exceeds 4 MiB limit; omitted by opsgate]"
	}
	return truncate(redact.String(s), max)
}
