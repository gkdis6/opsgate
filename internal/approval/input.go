package approval

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/tool"
)

const (
	approvalInputID = "opsgate_approval"
	maxUsedNonces   = 1000
	maxStateBytes   = 16 << 10
)

type inputState struct {
	Nonce              string            `json:"nonce"`
	User               string            `json:"user"`
	Transport          string            `json:"transport"`
	Tool               string            `json:"tool"`
	Target             string            `json:"target,omitempty"`
	NoWindow           bool              `json:"no_window,omitempty"`
	FullReviewRequired bool              `json:"full_review_required,omitempty"`
	ArgsHash           [sha256.Size]byte `json:"args_hash"`
	SummaryHash        [sha256.Size]byte `json:"summary_hash"`
	Expiry             time.Time         `json:"expiry"`
	Window             time.Duration     `json:"window,omitempty"`
}

func (m *Manager) requestInput(r tool.ConfirmRequest, hash [sha256.Size]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	if len(m.key) != 32 || len(m.used) >= maxUsedNonces {
		return tool.Denied("cannot create approval")
	}
	nonce, err := randomToken(32)
	if err != nil {
		return tool.Denied("cannot create approval")
	}
	var window time.Duration
	properties := map[string]any{"approve": map[string]any{"type": "boolean", "title": "Approve this action"}}
	title := displayTitle(r)
	summary := r.Summary
	if r.Target != "" {
		title += " on " + r.Target
		summary = "Target: " + r.Target + "\n\n" + summary
	}
	if r.NoWindow {
		summary += "\n\nThis target requires approval for every call."
	}
	if m.windowEligible(r.Tool, r.NoWindow) {
		window = m.window
		properties["allow_window"] = map[string]any{
			"type": "boolean", "title": fmt.Sprintf("Also auto-approve %s for %s", title, windowLabel(window)),
		}
	}
	payload, err := json.Marshal(inputState{
		Nonce: nonce, User: r.Caller.User, Transport: r.Caller.Transport, Tool: r.Tool,
		Target: r.Target, NoWindow: r.NoWindow,
		FullReviewRequired: r.FullReviewRequired,
		ArgsHash:           hash, SummaryHash: sha256.Sum256([]byte(r.Summary)), Expiry: m.now().Add(m.ttl),
		Window: window,
	})
	if err != nil {
		return tool.Denied("cannot create approval")
	}
	mac := hmac.New(sha256.New, m.key)
	mac.Write(payload)
	state := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(state) > maxStateBytes {
		return tool.Denied("cannot create approval")
	}
	return &tool.InputRequired{
		Requests: mcp.InputRequestMap{approvalInputID: &mcp.ElicitParams{
			Mode: "form",
			Message: fmt.Sprintf("opsgate approval request\n\n%s\n\nRequested by %s via %s. Approve?",
				summary, r.Caller.User, r.Caller.Transport),
			RequestedSchema: map[string]any{
				"type": "object", "required": []string{"approve"},
				"properties": properties,
			},
		}},
		State: state,
	}
}

func (m *Manager) confirmInput(state string, response mcp.InputResponse, r tool.ConfirmRequest, hash [sha256.Size]byte) (string, error) {
	var grantEvent *audit.Event
	defer func() {
		if grantEvent != nil {
			m.audit.Record(*grantEvent)
		}
	}()
	invalid := tool.Denied("invalid or expired approval")
	if len(m.key) != 32 || len(state) > maxStateBytes {
		return "elicitation", invalid
	}
	encoded, signature, ok := strings.Cut(state, ".")
	if !ok {
		return "elicitation", invalid
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return "elicitation", invalid
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil {
		return "elicitation", invalid
	}
	mac := hmac.New(sha256.New, m.key)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "elicitation", invalid
	}
	var p inputState
	if json.Unmarshal(payload, &p) != nil || p.Nonce == "" {
		return "elicitation", invalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	if !m.now().Before(p.Expiry) || !m.used[p.Nonce].IsZero() || len(m.used) >= maxUsedNonces {
		return "elicitation", invalid
	}
	// Consume authenticated state even when the binding or response is wrong.
	// Never evict a live nonce to make room for another approval.
	m.used[p.Nonce] = p.Expiry
	if p.User != r.Caller.User || p.Transport != r.Caller.Transport || p.Tool != r.Tool ||
		p.Target != r.Target || p.NoWindow != r.NoWindow || p.FullReviewRequired != r.FullReviewRequired || r.FullReviewRequired ||
		p.ArgsHash != hash || p.SummaryHash != sha256.Sum256([]byte(r.Summary)) {
		return "elicitation", invalid
	}
	result, ok := response.(*mcp.ElicitResult)
	if !ok || result == nil || result.Action != "accept" || result.Content["approve"] != true {
		return "elicitation", tool.Denied("rejected by human")
	}
	for key := range result.Content {
		if key != "approve" && key != "allow_window" {
			return "elicitation", invalid
		}
	}
	if value, exists := result.Content["allow_window"]; exists {
		allow, ok := value.(bool)
		if !ok || p.Window == 0 || p.Window != m.window || !m.windowEligible(r.Tool, r.NoWindow) || m.requireWeb {
			return "elicitation", invalid
		}
		if allow {
			grantEvent, err = m.grantLocked(grantKey{r.Caller.User, r.Tool, r.Target}, r.Caller.Transport, "elicitation", r.NoWindow)
			if err != nil {
				return "elicitation", err
			}
		}
	}
	return "elicitation", nil
}
