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

	"github.com/gkdis6/opsgate/internal/tool"
)

const (
	approvalInputID = "opsgate_approval"
	maxUsedNonces   = 1000
	maxStateBytes   = 16 << 10
)

type inputState struct {
	Nonce       string            `json:"nonce"`
	User        string            `json:"user"`
	Transport   string            `json:"transport"`
	Tool        string            `json:"tool"`
	ArgsHash    [sha256.Size]byte `json:"args_hash"`
	SummaryHash [sha256.Size]byte `json:"summary_hash"`
	Expiry      time.Time         `json:"expiry"`
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
	payload, err := json.Marshal(inputState{
		Nonce: nonce, User: r.Caller.User, Transport: r.Caller.Transport, Tool: r.Tool,
		ArgsHash: hash, SummaryHash: sha256.Sum256([]byte(r.Summary)), Expiry: m.now().Add(m.ttl),
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
				r.Summary, r.Caller.User, r.Caller.Transport),
			RequestedSchema: map[string]any{
				"type": "object", "required": []string{"approve"},
				"properties": map[string]any{"approve": map[string]any{
					"type": "boolean", "title": "Approve this action",
				}},
			},
		}},
		State: state,
	}
}

func (m *Manager) confirmInput(state string, response mcp.InputResponse, r tool.ConfirmRequest, hash [sha256.Size]byte) (string, error) {
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
		p.ArgsHash != hash || p.SummaryHash != sha256.Sum256([]byte(r.Summary)) {
		return "elicitation", invalid
	}
	result, ok := response.(*mcp.ElicitResult)
	if !ok || result == nil || result.Action != "accept" || result.Content["approve"] != true {
		return "elicitation", tool.Denied("rejected by human")
	}
	return "elicitation", nil
}
