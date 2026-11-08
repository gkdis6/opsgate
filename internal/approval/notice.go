package approval

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
)

type inboxNotice struct {
	alert.Notice
	ID, URL string
	Updated time.Time
}

type noticeStore struct {
	secret    []byte
	subjects  []string
	users     map[string][]inboxNotice
	replay    map[string]time.Time
	pushed    map[string]time.Time
	rateStart time.Time
	requests  int
}

// EnableInboxNotify loads the shared key before serving. Notices never create
// approvals; the usual authenticated live choices flow remains the only gate.
func (m *Manager) EnableInboxNotify(cfg config.InboxNotify) error {
	if cfg.SecretFile == "" {
		return nil
	}
	if m.browser == nil || len(cfg.Subjects) == 0 || len(cfg.Subjects) > 100 {
		return fmt.Errorf("inbox notices require browser approval and subjects")
	}
	secret, err := alert.ReadSecret(cfg.SecretFile)
	if err != nil {
		return err
	}
	m.notices = &noticeStore{secret: secret, subjects: slices.Clone(cfg.Subjects), users: map[string][]inboxNotice{}, replay: map[string]time.Time{}, pushed: map[string]time.Time{}}
	return nil
}

func (s *noticeStore) sweep(now time.Time) {
	for sig, expiry := range s.replay {
		if !now.Before(expiry) {
			delete(s.replay, sig)
		}
	}
	for user, notices := range s.users {
		s.users[user] = slices.DeleteFunc(notices, func(n inboxNotice) bool { return !now.Before(n.Updated.Add(time.Hour)) })
	}
}

func (m *Manager) notify(w http.ResponseWriter, r *http.Request) {
	// Bound even rejected uploads, including the HTTP/1 server's body drain.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
	reject := func(code int) {
		if r.ProtoMajor == 1 {
			w.Header().Set("Connection", "close")
		}
		http.Error(w, "Notice not accepted", code)
	}
	m.mu.Lock()
	s := m.notices
	if s == nil {
		m.mu.Unlock()
		reject(http.StatusNotFound)
		return
	}
	now := m.now()
	if now.Sub(s.rateStart) >= time.Minute {
		s.rateStart, s.requests = now, 0
	}
	s.requests++
	limited := s.requests > 60
	// Saturate even under sustained unauthenticated traffic.
	if limited {
		s.requests = 61
	}
	m.mu.Unlock()
	if limited {
		reject(http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		reject(http.StatusRequestEntityTooLarge)
		return
	}
	// Only a fully consumed upload may return to a reusable connection.
	_ = controller.SetReadDeadline(time.Time{})
	signature := r.Header.Get("X-Opsgate-Signature")
	if len(r.Header.Values("X-Opsgate-Signature")) != 1 || len(signature) != 71 || !strings.HasPrefix(signature, "sha256=") {
		reject(http.StatusUnauthorized)
		return
	}
	digest, err := hex.DecodeString(signature[7:])
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(body)
	if err != nil || !hmac.Equal(digest, mac.Sum(nil)) {
		reject(http.StatusUnauthorized)
		return
	}
	var batch alert.NoticeBatch
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&batch) != nil || dec.Decode(new(any)) != io.EOF || len(batch.Notices) == 0 || len(batch.Notices) > 50 {
		reject(http.StatusBadRequest)
		return
	}
	// Validate navigation and build each immutable entry once, before fan-out.
	notices := make([]inboxNotice, 0, len(batch.Notices))
	for _, n := range batch.Notices {
		if !n.Valid() {
			reject(http.StatusBadRequest)
			return
		}
		hint := ""
		if n.WorkloadKind != "" {
			hint = n.WorkloadKind + "/" + n.WorkloadName
		}
		link, err := action.OptionsURL("", n.Cluster, n.Namespace, n.Pod, hint)
		if err != nil {
			reject(http.StatusBadRequest)
			return
		}
		// JSON gives unambiguous field boundaries without retaining more input.
		key, _ := json.Marshal([]string{n.Cluster, n.Namespace, n.Pod, n.Reason})
		sum := sha256.Sum256(key)
		notices = append(notices, inboxNotice{Notice: n, ID: hex.EncodeToString(sum[:]), URL: link})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now = m.now()
	if !now.Before(batch.SentAt.Add(5*time.Minute)) || batch.SentAt.After(now.Add(time.Minute)) {
		reject(http.StatusBadRequest)
		return
	}
	s.sweep(now)
	// Canonicalize the digest so alternate hex case cannot bypass replay checks.
	replayKey := hex.EncodeToString(digest)
	if _, exists := s.replay[replayKey]; exists {
		reject(http.StatusConflict)
		return
	}
	if len(s.replay) >= 1000 {
		reject(http.StatusServiceUnavailable)
		return
	}
	s.replay[replayKey] = batch.SentAt.Add(5 * time.Minute)
	for _, user := range s.subjects {
		entries := s.users[user]
		for _, n := range notices {
			entries = slices.DeleteFunc(entries, func(e inboxNotice) bool { return e.ID == n.ID })
			n.Updated = now
			entries = append(entries, n)
			if len(entries) > 50 {
				entries = entries[len(entries)-50:]
			}
		}
		s.users[user] = entries
		if m.push != nil && (s.pushed[user].IsZero() || now.Sub(s.pushed[user]) >= time.Minute) {
			s.pushed[user] = now
			m.push.enqueue(user, now.Add(time.Hour))
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) dismissNotice(w http.ResponseWriter, r *http.Request, user string) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if r.ParseForm() != nil || len(r.PostForm) != 2 || len(r.PostForm["csrf"]) != 1 || len(r.PostForm["id"]) != 1 || !hmac.Equal([]byte(r.PostForm.Get("csrf")), []byte(m.inboxCSRF(user))) || m.inboxCSRF(user) == "" {
		http.Error(w, "Notice could not be dismissed", http.StatusForbidden)
		return
	}
	m.mu.Lock()
	if m.notices != nil && m.notices.users[user] != nil {
		m.notices.sweep(m.now())
		m.notices.users[user] = slices.DeleteFunc(m.notices.users[user], func(n inboxNotice) bool { return n.ID == r.PostForm.Get("id") })
	}
	m.mu.Unlock()
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}
