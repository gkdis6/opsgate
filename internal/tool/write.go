package tool

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
}

// Plan is the side-effect-free preview of a write. Summary is shown to the
// human approver verbatim, so it must name the exact target and effect.
type Plan struct {
	Summary string
}

// WriteHandler splits a write into a side-effect-free Plan (input
// validation, allowlist checks, current state) and Apply. Apply runs only
// after Plan succeeded and a human confirmed the plan's summary.
type WriteHandler[In WriteInput] struct {
	Plan  func(ctx context.Context, c Caller, in In) (Plan, error)
	Apply func(ctx context.Context, c Caller, in In) (string, error)
}

// ConfirmRequest is what a Confirmer needs to obtain human approval.
type ConfirmRequest struct {
	Caller Caller
	Tool   string
	// Args are the redacted call arguments without approval_id; an approval
	// is bound to exactly these arguments.
	Args       map[string]any
	Summary    string
	ApprovalID string
}

// Confirmer obtains a human decision that the model cannot forge. It
// returns the method used ("elicitation", "web") on approval, an error
// wrapping ErrDenied on refusal, or an error wrapping ErrApprovalPending
// whose message tells the model what the human must do next.
type Confirmer interface {
	Confirm(ctx context.Context, req *mcp.CallToolRequest, r ConfirmRequest) (method string, err error)
}

// ErrApprovalPending means a human has been asked but has not decided yet.
var ErrApprovalPending = errors.New("approval pending")

// AddWrite registers a write tool on s. Without env.Confirm every call is
// denied, so writes can never run unconfirmed.
func AddWrite[In WriteInput](s *mcp.Server, env *Env, spec WriteSpec, h WriteHandler[In]) {
	// TODO(codex): implement.
}
