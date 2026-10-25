package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
)

// Invites live as one small file per token hash next to the state file, so
// `opsgate passkey invite` (run as the server's account) can add one while
// the server is running. Only the SHA-256 of the token is stored.

const inviteDirName = "passkey-invites"

type passkeyInvite struct {
	Subject   string    `json:"subject"`
	Expires   time.Time `json:"expires"`
	Bootstrap bool      `json:"bootstrap,omitempty"`
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return b
}

func inviteDir(cfg config.BuiltinAuth) string {
	if cfg.StateFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.StateFile), inviteDirName)
}

// CreatePasskeyInvite writes a one-time registration invite for subject and
// returns its URL. subject must be a configured user and the upstream
// provider must be "passkey".
func CreatePasskeyInvite(cfg config.BuiltinAuth, issuer, subject string, ttl time.Duration) (string, error) {
	if cfg.Upstream.Provider != "passkey" {
		return "", fmt.Errorf("auth.builtin.upstream.provider is not passkey")
	}
	if _, ok := configuredSubject(cfg, subject); !ok {
		return "", fmt.Errorf("%q is not in auth.builtin.users", subject)
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return "", fmt.Errorf("invite lifetime must be between 1s and 7d")
	}
	dir := inviteDir(cfg)
	if dir == "" {
		return "", fmt.Errorf("auth.builtin.state_file is required for invites")
	}
	token, err := writeInvite(dir, passkeyInvite{Subject: subject, Expires: time.Now().Add(ttl)})
	if err != nil {
		return "", err
	}
	return issuer + "/passkey/register?invite=" + token, nil
}

func configuredSubject(cfg config.BuiltinAuth, subject string) (config.User, bool) {
	for _, u := range cfg.Users {
		if u.Subject == subject {
			return u, true
		}
	}
	return config.User{}, false
}

func writeInvite(dir string, inv passkeyInvite) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create invite directory: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(randomBytes(32))
	f, err := os.CreateTemp(dir, ".invite-*")
	if err != nil {
		return "", fmt.Errorf("cannot write invite: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	err = json.NewEncoder(f).Encode(inv)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, hash(token)+".json"))
	}
	if err != nil {
		return "", fmt.Errorf("cannot write invite: %w", err)
	}
	return token, nil
}

// lookupInvite returns a live invite for token and its key. Caller holds mu.
func (s *Server) lookupInvite(token string) (*passkeyInvite, string) {
	if b, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(b) != 32 {
		return nil, ""
	}
	key := hash(token)
	inv := s.invites[key]
	if inv == nil {
		inv = readInvite(inviteDir(s.cfg), key)
	}
	if inv == nil || !s.now().Before(inv.Expires) {
		return nil, ""
	}
	if _, ok := configuredSubject(s.cfg, inv.Subject); !ok {
		return nil, ""
	}
	s.invites[key] = inv
	return inv, key
}

func readInvite(dir, key string) *passkeyInvite {
	if dir == "" {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, key+".json"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	var inv passkeyInvite
	if json.NewDecoder(io.LimitReader(f, 4<<10)).Decode(&inv) != nil || inv.Subject == "" {
		return nil
	}
	return &inv
}

// consumeInvite makes an invite unusable. Caller holds mu.
func (s *Server) consumeInvite(key string) error {
	delete(s.invites, key)
	if dir := inviteDir(s.cfg); dir != "" {
		if err := os.Remove(filepath.Join(dir, key+".json")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// bootstrapInvites logs a fresh invite for every configured user without a
// passkey, replacing earlier bootstrap invites, so a new server can be set
// up from its logs alone. Caller holds mu (or runs before serving).
func (s *Server) bootstrapInvites() {
	dir := inviteDir(s.cfg)
	if dir == "" {
		for _, subject := range s.passkeyMissing() {
			token := base64.RawURLEncoding.EncodeToString(randomBytes(32))
			s.invites[hash(token)] = &passkeyInvite{Subject: subject, Expires: s.now().Add(24 * time.Hour), Bootstrap: true}
			log.Printf("passkey setup for %s (valid 24h): %s/passkey/register?invite=%s", subject, s.issuer, token)
		}
		return
	}
	entries, _ := os.ReadDir(dir)
	missing := map[string]bool{}
	for _, subject := range s.passkeyMissing() {
		missing[subject] = true
	}
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		inv := readInvite(dir, name[:len(name)-len(".json")])
		if inv == nil || !s.now().Before(inv.Expires) || inv.Bootstrap {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	for subject := range missing {
		token, err := writeInvite(dir, passkeyInvite{Subject: subject, Expires: s.now().Add(24 * time.Hour), Bootstrap: true})
		if err != nil {
			log.Printf("passkey setup for %s: %v", subject, err)
			continue
		}
		log.Printf("passkey setup for %s (valid 24h): %s/passkey/register?invite=%s", subject, s.issuer, token)
	}
}
