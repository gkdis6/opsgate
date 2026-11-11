package approval

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/tool"
)

const maxApprovalState = 4 << 20

// diskState is what survives a restart: the key that signs action links, the
// undecided approvals opened from links and the inbox notices. It holds no
// CSRF token and no session; a restored approval gets a new token and still
// needs the same signed-in user to approve it.
//
// Approvals requested by an MCP client are not stored: their execution is
// bound to the client's original call, which a restart ends.
type diskState struct {
	Version   int                     `json:"version"`
	LinkKey   []byte                  `json:"link_key"`
	Approvals []diskApproval          `json:"approvals,omitempty"`
	Notices   map[string][]diskNotice `json:"notices,omitempty"`
}

type diskApproval struct {
	ID      string          `json:"id"`
	User    string          `json:"user"`
	Tool    string          `json:"tool"`
	Source  string          `json:"source"`
	Summary string          `json:"summary"`
	Target  string          `json:"target"`
	Args    json.RawMessage `json:"args"`
	Raw     json.RawMessage `json:"raw"`
	Created time.Time       `json:"created"`
	Expires time.Time       `json:"expires"`
}

type diskNotice struct {
	alert.Notice
	ID      string    `json:"id"`
	URL     string    `json:"url"`
	Updated time.Time `json:"updated"`
}

// EnableState loads path, if set, and returns the link-signing key to use.
// Call it after EnableInboxNotify and before serving. A missing file starts
// empty and a file this process cannot read leaves the state in memory. A
// readable file in another format is an error rather than silently replaced.
// Without a path the key is random and nothing is stored.
func (m *Manager) EnableState(path string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if path == "" || m.browser == nil {
		return key, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateFile = path
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Approval does not depend on the file: saveLocked logs a failure.
		m.linkKey = key
		_ = m.saveLocked()
		return key, nil
	}
	if err != nil {
		// The default path may simply not be available to this process.
		// Approval works from memory; only persistence is lost.
		m.stateFile = ""
		log.Printf("approval state %s is not readable; waiting approvals will not survive a restart", path)
		return key, nil
	}
	var state diskState
	if len(raw) > maxApprovalState || json.Unmarshal(raw, &state) != nil || state.Version != 1 || len(state.LinkKey) != 32 {
		return nil, fmt.Errorf("cannot read approval state %s", path)
	}
	m.linkKey = state.LinkKey
	now := m.now()
	for _, a := range state.Approvals {
		if !validID(a.ID) || !now.Before(a.Expires) || a.User == "" || len(m.approvals) >= 100 {
			continue
		}
		csrf, err := randomToken(32)
		if err != nil {
			return nil, err
		}
		m.approvals[a.ID] = &pending{
			id: a.ID, csrf: csrf, user: a.User, transport: "web", tool: a.Tool, title: label(a.Tool), source: a.Source, summary: a.Summary,
			target: a.Target, noWindow: true, args: a.Args, raw: a.Raw, created: a.Created, expires: a.Expires,
			run: m.linkRun(a.Tool, a.User, a.Source, a.Raw, a.Summary),
		}
	}
	if m.notices != nil {
		for _, user := range m.notices.subjects {
			for _, n := range state.Notices[user] {
				// Stored fields are validated again as if they had just arrived.
				if n.Valid() && now.Before(n.Updated.Add(time.Hour)) && len(m.notices.users[user]) < 50 {
					m.notices.users[user] = append(m.notices.users[user], inboxNotice{Notice: n.Notice, ID: n.ID, URL: n.URL, Updated: n.Updated})
				}
			}
		}
	}
	return state.LinkKey, nil
}

// linkRun plans and applies a link's action for its user at approval time,
// with the user's access as it is then.
func (m *Manager) linkRun(name, user, source string, raw []byte, summary string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		var d *tool.Direct
		if m.direct != nil {
			d = m.direct(name)
		}
		access, ok := m.browser.(browserAccess)
		if d == nil || !ok {
			return "", tool.Denied("this server no longer offers this action")
		}
		scopes, tools, known := access.Access(user)
		if !known {
			return "", tool.Denied("this account cannot approve changes")
		}
		return d.Run(ctx, tool.Caller{User: user, Transport: "web", Scopes: scopes, Tools: tools, Source: source}, raw, summary)
	}
}

// saveLocked writes the state atomically. Approval never depends on it: a
// failed write is logged once and the server continues from memory.
func (m *Manager) saveLocked() error {
	if m.stateFile == "" {
		return nil
	}
	state := diskState{Version: 1, LinkKey: m.linkKey}
	for _, p := range m.approvals {
		if p.raw != nil && p.decision == "" {
			state.Approvals = append(state.Approvals, diskApproval{ID: p.id, User: p.user, Tool: p.tool, Source: p.source, Summary: p.summary, Target: p.target, Args: p.args, Raw: p.raw, Created: p.created, Expires: p.expires})
		}
	}
	if m.notices != nil {
		state.Notices = map[string][]diskNotice{}
		for user, notices := range m.notices.users {
			for _, n := range notices {
				state.Notices[user] = append(state.Notices[user], diskNotice{Notice: n.Notice, ID: n.ID, URL: n.URL, Updated: n.Updated})
			}
		}
	}
	err := writeState(m.stateFile, state)
	if err != nil && !m.stateWarned {
		m.stateWarned = true
		log.Printf("approval state not saved (%v); waiting approvals will not survive a restart", err)
	}
	return err
}

func writeState(path string, state diskState) error {
	b, err := json.Marshal(state)
	if err != nil || len(b) > maxApprovalState {
		return fmt.Errorf("approval state exceeds size limit")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create approval state directory")
	}
	f, err := os.CreateTemp(dir, ".approvals-*")
	if err != nil {
		return fmt.Errorf("cannot create approval state")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("cannot persist approval state")
	}
	return nil
}

// Counts reports the approvals waiting for a decision and the inbox notices
// held, for metrics.
func (m *Manager) Counts() (waiting, notices int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	for _, p := range m.approvals {
		if p.decision == "" {
			waiting++
		}
	}
	if m.notices != nil {
		for _, list := range m.notices.users {
			notices += len(list)
		}
	}
	return waiting, notices
}
