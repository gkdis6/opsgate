package oauth

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Reads and writes share a hard bound so an oversized snapshot cannot replace
// a loadable one. The larger bound accommodates code pools with escaped URIs.
const maxStateBytes = 512 << 20

type client struct {
	Pending      bool      `json:"pending,omitempty"`
	ID           string    `json:"client_id"`
	RedirectURIs []string  `json:"redirect_uris"`
	Name         string    `json:"client_name"`
	Method       string    `json:"token_endpoint_auth_method"`
	SecretHash   string    `json:"secret_hash,omitempty"`
	GrantTypes   []string  `json:"grant_types"`
	Created      time.Time `json:"created"`
	LastUsed     time.Time `json:"last_used"`
}
type accessToken struct {
	ClientID string
	Subject  string
	Scopes   []string
	Family   string
	Expires  time.Time
}
type refreshToken struct {
	accessToken
	Used        bool
	RetainUntil time.Time
}
type authorizationCode struct {
	ClientID, RedirectURI, Challenge, Subject, Resource, Family string
	Scopes                                                      []string
	Expires, RetainUntil                                        time.Time
	Used                                                        bool
}
type pendingAuth struct {
	Kind, ClientID, RedirectURI, Challenge, State, UpstreamState, Verifier, Nonce, CSRF, Subject, ReturnPath string
	Requested, Scopes                                                                                        []string
	Expires                                                                                                  time.Time
	InFlight                                                                                                 bool
	// Passkey login: the open WebAuthn challenge, then the verified subject
	// waiting for /oauth/passkey/complete.
	WebAuthn       *webauthn.SessionData
	PasskeySubject string
}
type browserSession struct {
	Subject string
	CSRF    string
	Expires time.Time
}
type diskState struct {
	Version  int                           `json:"version"`
	Issuer   string                        `json:"issuer"`
	Resource string                        `json:"resource"`
	Clients  map[string]*client            `json:"clients"`
	Access   map[string]*accessToken       `json:"access"`
	Refresh  map[string]*refreshToken      `json:"refresh"`
	Families map[string]time.Time          `json:"revoked_families"`
	Codes    map[string]*authorizationCode `json:"codes"`
	Passkeys map[string]*passkeyUser       `json:"passkeys,omitempty"`
}

func (s *Server) load() error {
	if s.cfg.StateFile == "" {
		return nil
	}
	f, err := os.Open(s.cfg.StateFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot open OAuth state")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxStateBytes {
		return fmt.Errorf("invalid OAuth state file")
	}
	if err := os.Chmod(s.cfg.StateFile, 0600); err != nil {
		return fmt.Errorf("cannot secure OAuth state file")
	}
	var d diskState
	dec := json.NewDecoder(io.LimitReader(f, maxStateBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return fmt.Errorf("invalid OAuth state")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return fmt.Errorf("invalid trailing OAuth state")
	}
	if d.Version != 1 || d.Issuer != s.issuer || d.Resource != s.resource || d.Clients == nil || d.Access == nil || d.Refresh == nil || d.Families == nil || d.Codes == nil {
		return fmt.Errorf("incompatible OAuth state")
	}
	for _, c := range d.Clients {
		if c == nil {
			return fmt.Errorf("invalid OAuth client state")
		}
	}
	for _, t := range d.Access {
		if t == nil {
			return fmt.Errorf("invalid OAuth token state")
		}
	}
	for _, t := range d.Refresh {
		if t == nil {
			return fmt.Errorf("invalid OAuth token state")
		}
	}
	for _, c := range d.Codes {
		if c == nil {
			return fmt.Errorf("invalid OAuth code state")
		}
	}
	for subject, u := range d.Passkeys {
		if u == nil || len(u.Handle) == 0 || subject == "" {
			return fmt.Errorf("invalid passkey state")
		}
	}
	if d.Passkeys == nil {
		d.Passkeys = map[string]*passkeyUser{}
	}
	s.clients, s.access, s.refresh, s.families, s.codes, s.passkeys = d.Clients, d.Access, d.Refresh, d.Families, d.Codes, d.Passkeys
	s.sweep()
	return nil
}

// Callers hold mu. A failed durable write makes the running instance fail closed.
func (s *Server) save() error {
	if s.storageErr != nil {
		return s.storageErr
	}
	s.sweep()
	if s.cfg.StateFile == "" {
		return nil
	}
	err := s.writeState()
	if err != nil {
		s.storageErr = fmt.Errorf("cannot persist OAuth state")
		return s.storageErr
	}
	return nil
}
func (s *Server) writeState() error {
	dir := filepath.Dir(s.cfg.StateFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".oauth-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	d := diskState{Version: 1, Issuer: s.issuer, Resource: s.resource, Clients: s.clients, Access: s.access, Refresh: s.refresh, Families: s.families, Codes: s.codes, Passkeys: s.passkeys}
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if err = json.NewEncoder(&stateWriter{Writer: f, remaining: maxStateBytes}).Encode(d); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, s.cfg.StateFile); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

type stateWriter struct {
	io.Writer
	remaining int
}

func (w *stateWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, fmt.Errorf("OAuth state exceeds size limit")
	}
	n, err := w.Writer.Write(p)
	w.remaining -= n
	return n, err
}

func (s *Server) sweep() bool {
	before := len(s.access) + len(s.refresh) + len(s.codes) + len(s.families) + len(s.clients)
	now := s.now()
	for k, t := range s.access {
		if !now.Before(t.Expires) {
			delete(s.access, k)
		}
	}
	for k, t := range s.refresh {
		if !now.Before(t.RetainUntil) {
			delete(s.refresh, k)
		}
	}
	for k, t := range s.codes {
		if !now.Before(t.RetainUntil) || !t.Used && !now.Before(t.Expires) {
			delete(s.codes, k)
		}
	}
	for k, expiry := range s.families {
		if !now.Before(expiry) {
			delete(s.families, k)
		}
	}
	for k, p := range s.pending {
		if !now.Before(p.Expires) {
			delete(s.pending, k)
		}
	}
	for k, p := range s.sessions {
		if !now.Before(p.Expires) {
			delete(s.sessions, k)
		}
	}
	protected := s.protectedClients()
	for k, c := range s.clients {
		if c.Pending && now.Sub(c.Created) >= 24*time.Hour || !protected[k] && (c.LastUsed.IsZero() && now.Sub(c.Created) >= 24*time.Hour || !c.LastUsed.IsZero() && now.Sub(c.LastUsed) >= 90*24*time.Hour) {
			delete(s.clients, k)
		}
	}
	return before != len(s.access)+len(s.refresh)+len(s.codes)+len(s.families)+len(s.clients)
}
func (s *Server) sweepLoop() {
	defer close(s.done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.sweep() {
				_ = s.save()
			}
			s.mu.Unlock()
		}
	}
}
func (s *Server) revoked(family string) bool { _, ok := s.families[family]; return ok }
func (s *Server) revokeFamily(family string) bool {
	if s.revoked(family) {
		return false
	}
	expiry := s.now()
	for _, t := range s.access {
		if t.Family == family && t.Expires.After(expiry) {
			expiry = t.Expires
		}
	}
	for _, t := range s.refresh {
		if t.Family == family && t.Expires.After(expiry) {
			expiry = t.Expires
		}
	}
	for _, c := range s.codes {
		if c.Family == family && c.RetainUntil.After(expiry) {
			expiry = c.RetainUntil
		}
	}
	s.families[family] = expiry
	return true
}

// Caller holds mu. Pending browser approvals use a separate, smaller pool so
// DCR clients cannot crowd out logins needed to approve existing operations.
// Login entries expire in sweep; live entries are never evicted.
func (s *Server) makePendingRoom(in *pendingAuth) bool {
	login := in.Kind == "login"
	limit := maxPending
	if login {
		limit = maxLoginPending
	}
	for {
		count, clientCount := 0, 0
		oldest, clientOldest := "", ""
		for key, p := range s.pending {
			if (p.Kind == "login") != login {
				continue
			}
			count++
			if oldest == "" || p.Expires.Before(s.pending[oldest].Expires) {
				oldest = key
			}
			if !login && p.ClientID == in.ClientID {
				clientCount++
				if clientOldest == "" || p.Expires.Before(s.pending[clientOldest].Expires) {
					clientOldest = key
				}
			}
		}
		if clientCount >= maxClientPending {
			delete(s.pending, clientOldest)
		} else if count >= limit {
			if login {
				return false
			}
			delete(s.pending, oldest)
		} else {
			return true
		}
	}
}

func (s *Server) protectedClients() map[string]bool {
	protected := make(map[string]bool)
	for _, t := range s.access {
		protected[t.ClientID] = true
	}
	for _, t := range s.refresh {
		protected[t.ClientID] = true
	}
	for _, c := range s.codes {
		protected[c.ClientID] = true
	}
	for _, p := range s.pending {
		protected[p.ClientID] = true
	}
	return protected
}

// Caller holds mu. Pending registrations go first, then old unused clients.
// Active clients and outstanding authorizations retain their registration.
func (s *Server) pruneClients() {
	if len(s.clients) < s.cfg.MaxClients {
		return
	}
	protected := s.protectedClients()
	for len(s.clients) >= s.cfg.MaxClients {
		oldest := ""
		for id, c := range s.clients {
			if !c.LastUsed.IsZero() || protected[id] || !c.Pending && s.now().Sub(c.Created) <= 10*time.Minute {
				continue
			}
			if oldest == "" || c.Pending && !s.clients[oldest].Pending || c.Pending == s.clients[oldest].Pending && c.Created.Before(s.clients[oldest].Created) {
				oldest = id
			}
		}
		if oldest == "" {
			return
		}
		delete(s.clients, oldest)
	}
}
