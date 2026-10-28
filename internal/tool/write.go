package tool

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/redact"
)

// Approval is embedded in the input struct of every write tool. A client
// that was handed a web approval link calls the tool again with the same
// arguments plus approval_id once a human has approved it.
type Approval struct {
	ApprovalID string `json:"approval_id,omitempty" jsonschema:"ID of a human approval obtained via the link returned by a previous call; omit on the first call"`
}

func (a Approval) approvalID() string { return a.ApprovalID }

// WriteInput is satisfied by any struct embedding Approval.
type WriteInput interface{ approvalID() string }

// WriteSpec describes a state-changing tool. Its scope is always ops:write.
type WriteSpec struct {
	Name        string
	Title       string
	Description string
	// Idempotent marks tools whose repetition has no further effect.
	Idempotent bool
	// Scope required in addition to human approval; default ops:write.
	Scope string
	// MaxArgBytes caps the raw JSON arguments (default 16 KiB). Raise it only
	// for tools that carry document content.
	MaxArgBytes int
}

// Plan is the side-effect-free preview of a write. Summary is shown to the
// human approver verbatim, so it must name the exact target and effect.
type Plan struct {
	Summary string
	// Target names the system the write acts on (for example a Kubernetes
	// cluster) when one server reaches several. Approval screens show it
	// prominently and approval windows are granted per target. Empty means
	// the server's only system.
	Target string
	// NoWindow requires a fresh human approval for every call, even when the
	// tool is listed in approval.window_tools.
	NoWindow bool
	// Data belongs to this invocation and is never shown or serialized.
	Data any `json:"-"`
}

// WriteHandler splits a write into a side-effect-free Plan (input
// validation, allowlist checks, current state) and Apply. Apply runs only
// after Plan succeeded and a human confirmed the plan's summary.
type WriteHandler[In WriteInput] struct {
	Plan  func(ctx context.Context, c Caller, in In) (Plan, error)
	Apply func(ctx context.Context, c Caller, in In, p Plan) (string, error)
}

// ConfirmRequest is what a Confirmer needs to obtain human approval.
type ConfirmRequest struct {
	Caller Caller
	Tool   string
	// Title is the tool's human-readable title, for approval screens only.
	Title string
	// Args are redacted arguments for display only, without approval_id.
	// ArgsHash binds the canonical unredacted arguments, without approval_id.
	Args     map[string]any
	ArgsHash [sha256.Size]byte
	Summary  string
	// Target and NoWindow are copied from the tool's Plan.
	Target     string
	NoWindow   bool
	ApprovalID string
	// MaxArgBytes is the tool's argument limit, defaulting to 16 KiB.
	MaxArgBytes int
}

// Confirmer obtains a human decision that the model cannot forge. It
// returns the method used ("elicitation", "web") on approval, an error
// wrapping ErrDenied on refusal, or an error wrapping ErrApprovalPending
// whose message tells the model what the human must do next. InputRequired
// requests input through MCP's multi round-trip flow.
type Confirmer interface {
	Confirm(ctx context.Context, req *mcp.CallToolRequest, r ConfirmRequest) (method string, err error)
}

// ErrApprovalPending means a human has been asked but has not decided yet.
var ErrApprovalPending = errors.New("approval pending")

// InputRequired is returned by a Confirmer to ask the client for input.
type InputRequired struct {
	Requests mcp.InputRequestMap
	State    string
}

// Error implements error without exposing the request state.
func (e *InputRequired) Error() string { return "human approval input required" }

// AddWrite registers a write tool on s. Without env.Confirm every call is
// denied, so writes can never run unconfirmed.
func AddWrite[In WriteInput](s *mcp.Server, env *Env, spec WriteSpec, h WriteHandler[In]) {
	if !env.Policy.register(spec.Name) {
		return
	}
	destructive := true
	scope := spec.Scope
	if scope == "" {
		scope = config.ScopeWrite
	}
	maxArgs := spec.MaxArgBytes
	if maxArgs <= 0 {
		maxArgs = maxArgumentBytes
	}
	maxSummary := (ConfirmRequest{MaxArgBytes: maxArgs}).SummaryLimit()
	t := &mcp.Tool{
		Name: spec.Name, Title: spec.Title, Description: spec.Description,
		Annotations: &mcp.ToolAnnotations{
			Title: spec.Title, ReadOnlyHint: false,
			DestructiveHint: &destructive, IdempotentHint: spec.Idempotent,
		},
	}
	mcp.AddTool(s, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		var input *InputRequired
		var args map[string]any
		var argsHash [sha256.Size]byte
		var argsErr error
		// Check the wire size before redaction or adding arguments to the audit.
		if req == nil || req.Params == nil || len(req.Params.Arguments) > maxArgs {
			argsErr = Denied("write arguments must be JSON of at most %d KiB", maxArgs>>10)
		} else {
			args, argsHash, argsErr = writeArgs(req.Params.Arguments, maxArgs)
		}
		ev := audit.Event{Time: start, Tool: spec.Name, Args: args}
		c, ok := env.caller(req)
		if ok {
			ev.User, ev.Transport = c.User, c.Transport
		}
		text, err := func() (string, error) {
			if !ok {
				return "", Denied("unauthenticated")
			}
			if !c.allowsTool(spec.Name) {
				return "", Denied("tool not allowed for this user")
			}
			if !c.Has(scope) {
				return "", Denied("missing scope %s", scope)
			}
			if argsErr != nil {
				return "", argsErr
			}
			if h.Plan == nil || h.Apply == nil {
				return "", Denied("write handler unavailable")
			}
			planCtx, cancel := context.WithTimeout(ctx, env.Timeout)
			plan, err := h.Plan(planCtx, c, in)
			if err == nil {
				err = planCtx.Err()
			}
			cancel()
			if len(plan.Summary) > maxSummary {
				return "", Denied("approval summary exceeds %d KiB limit", maxSummary>>10)
			}
			ev.Plan = truncate(redact.String(plan.Summary), maxSummary)
			if err != nil {
				return "", err
			}
			if ev.Plan != plan.Summary || len(plan.Target) > 256 || redact.String(plan.Target) != plan.Target {
				return "", Denied("target cannot be shown unambiguously")
			}
			if env.Confirm == nil {
				return "", Denied("no human confirmation channel is available")
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			ev.Approval, err = env.Confirm.Confirm(ctx, req, ConfirmRequest{
				Caller: c, Tool: spec.Name, Title: spec.Title, Args: args, ArgsHash: argsHash, Summary: ev.Plan,
				Target: plan.Target, NoWindow: plan.NoWindow, ApprovalID: in.approvalID(), MaxArgBytes: maxArgs,
			})
			if err != nil {
				if errors.As(err, &input) && input != nil {
					ev.Approval = "elicitation"
				}
				return "", err
			}
			applyCtx, cancel := context.WithTimeout(ctx, env.Timeout)
			defer cancel()
			if err := applyCtx.Err(); err != nil {
				return "", err
			}
			return h.Apply(applyCtx, c, in, plan)
		}()

		ev.DurationMS = time.Since(start).Milliseconds()
		if input != nil {
			ev.Outcome = "pending"
			env.Audit.Record(ev)
			return &mcp.CallToolResult{InputRequests: input.Requests, RequestState: input.State}, nil, nil
		}
		ev.Outcome = "ok"
		if err != nil {
			ev.Outcome = "error"
			switch {
			case errors.Is(err, ErrDenied):
				ev.Outcome = "denied"
			case errors.Is(err, ErrApprovalPending):
				ev.Outcome = "pending"
			}
			text = err.Error()
		}
		text = outputText(text, env.MaxOutputBytes)
		if err != nil && ev.Outcome != "pending" {
			ev.Error = text
			env.Audit.Record(ev)
			return errorResult(text), nil, nil
		}
		ev.OutputSize = len(text)
		env.Audit.Record(ev)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	})
}
