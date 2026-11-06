package analyzer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/alert"
)

type pending struct {
	batch alert.Batch
	due   time.Time
	bytes int
}

// Service is a bounded, in-memory investigation queue and webhook handler.
// One worker serializes investigations, limiting model spending and MCP load.
type Service struct {
	cfg                          Config
	secret                       []byte
	mu                           sync.Mutex
	jobs                         map[string]*pending
	seen                         map[[32]byte]time.Time
	cooldown                     map[string]time.Time
	queuedBytes                  int
	jobByteLimit, queueByteLimit int
	now                          func() time.Time
	investigate                  func(context.Context, alert.Batch) (investigation, error)
	notify                       func(context.Context, string, []alert.ActionLink) error
	ready                        func(context.Context) error
}

func newService(c Config, key []byte) *Service {
	return &Service{cfg: c, secret: key, jobs: map[string]*pending{}, seen: map[[32]byte]time.Time{}, cooldown: map[string]time.Time{}, now: time.Now, jobByteLimit: 64 << 10, queueByteLimit: 8 << 20}
}

// Handler exposes only /alerts and /healthz. Alert bodies are capped at 1 MiB.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if s.ready == nil || s.ready(ctx) != nil {
			http.Error(w, "MCP reader unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /alerts", s.receive)
	return mux
}

func (s *Service) receive(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid body size", http.StatusRequestEntityTooLarge)
		return
	}
	want := alert.Sign(s.secret, raw)
	if subtle.ConstantTimeCompare([]byte(want), []byte(r.Header.Get("X-Opsgate-Signature"))) != 1 {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	var b alert.Batch
	if json.Unmarshal(raw, &b) != nil || b.Source != "opsgate" || b.SentAt.IsZero() || len(b.Alerts) > 500 || b.Dropped < 0 {
		http.Error(w, "invalid watch batch", http.StatusBadRequest)
		return
	}
	now := s.now()
	if age := now.Sub(b.SentAt); age < -time.Minute || age > 10*time.Minute {
		http.Error(w, "stale watch batch", http.StatusBadRequest)
		return
	}
	groups := map[string][]alert.Alert{}
	for _, a := range b.Alerts {
		if !alertFieldsFit(a) {
			http.Error(w, "alert field too large", http.StatusRequestEntityTooLarge)
			return
		}
		if a.Cluster == "" || a.Name == "" || a.Kind == "" || a.Reason == "" || a.Time.IsZero() || a.Suppressed < 0 || a.Severity != alert.Warning && a.Severity != alert.Info {
			http.Error(w, "invalid alert", http.StatusBadRequest)
			return
		}
		if s.cfg.WarningsOnly && a.Severity != alert.Warning {
			continue
		}
		// Localized display duplicates source evidence; the engine does not use
		// it. Batch text/actions/language are likewise never retained in jobs.
		a.Display = nil
		a.Key = ""
		key := strings.Join([]string{a.Cluster, a.Kind, a.Namespace, a.Name}, "/")
		groups[key] = append(groups[key], a)
	}
	digest := sha256.Sum256(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, until := range s.seen {
		if !now.Before(until) {
			delete(s.seen, k)
		}
	}
	for k, until := range s.cooldown {
		if !now.Before(until) {
			delete(s.cooldown, k)
		}
	}
	if _, ok := s.seen[digest]; ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	needed := 0
	for k := range groups {
		if _, ok := s.jobs[k]; !ok && !now.Before(s.cooldown[k]) {
			needed++
		}
	}
	if len(s.seen) >= 4096 || len(s.jobs)+needed > s.cfg.QueueSize || len(s.cooldown)+needed > 4096 {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "investigation queue full", http.StatusServiceUnavailable)
		return
	}
	// Stage complete copies first: neither a rejected insertion nor a larger
	// replacement may partially alter the queue or its retained-byte charge.
	updates := map[string]*pending{}
	total := s.queuedBytes
	for key, alerts := range groups {
		if now.Before(s.cooldown[key]) {
			continue
		}
		j := &pending{batch: alert.Batch{Source: b.Source, SentAt: b.SentAt, Dropped: b.Dropped}, due: now.Add(s.cfg.BatchWindow)}
		if old := s.jobs[key]; old != nil {
			*j = *old
			j.batch.Alerts = append([]alert.Alert(nil), old.batch.Alerts...)
			total -= old.bytes
		}
		for _, a := range alerts {
			mergeAlert(&j.batch, a)
		}
		wire, _ := json.Marshal(j.batch)
		// Serialized bytes account for strings (including escaping), plus a
		// conservative allowance for retained structs, slices and map entries.
		j.bytes = len(wire) + len(key) + 512 + 256*len(j.batch.Alerts)
		total += j.bytes
		if j.bytes > s.jobByteLimit {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "investigation byte budget full", http.StatusServiceUnavailable)
			return
		}
		updates[key] = j
	}
	if total > s.queueByteLimit {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "investigation byte budget full", http.StatusServiceUnavailable)
		return
	}
	for key, j := range updates {
		s.jobs[key] = j
	}
	s.queuedBytes = total
	s.seen[digest] = now.Add(10 * time.Minute)
	w.WriteHeader(http.StatusAccepted)
}

func alertFieldsFit(a alert.Alert) bool {
	if len(a.Cluster) > 63 || len(a.Kind) > 64 || len(a.Namespace) > 63 || len(a.Name) > 253 {
		return false
	}
	// Watch emits at most 300 Unicode characters and a truncation ellipsis.
	for _, text := range []string{a.Reason, a.Summary, a.Hint} {
		if len(text) > 1203 || utf8.RuneCountInString(text) > 301 {
			return false
		}
	}
	if r := a.Restart; r != nil {
		return len(r.Kind) <= 64 && len(r.Namespace) <= 63 && len(r.Name) <= 253 && len(r.URL) <= 4096
	}
	return true
}

func mergeAlert(b *alert.Batch, a alert.Alert) {
	for i, old := range b.Alerts {
		if old.Reason == a.Reason && old.Summary == a.Summary {
			if old.Suppressed == math.MaxInt || a.Suppressed > math.MaxInt-old.Suppressed-1 {
				a.Suppressed = math.MaxInt
			} else {
				a.Suppressed += old.Suppressed + 1
			}
			b.Alerts[i] = a
			return
		}
	}
	if len(b.Alerts) >= 50 {
		b.Dropped++
		return
	}
	b.Alerts = append(b.Alerts, a)
}

func (s *Service) take() (alert.Batch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var key string
	var selected *pending
	for k, j := range s.jobs {
		if !now.Before(j.due) && (selected == nil || j.due.Before(selected.due)) {
			key, selected = k, j
		}
	}
	if selected == nil {
		return alert.Batch{}, false
	}
	delete(s.jobs, key)
	s.queuedBytes -= selected.bytes
	s.cooldown[key] = now.Add(s.cfg.Cooldown)
	return selected.batch, true
}

// Run processes pending investigations until canceled; pending jobs are not persistent.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for {
			if ctx.Err() != nil {
				return
			}
			b, ok := s.take()
			if !ok {
				break
			}
			s.process(ctx, b)
		}
	}
}

func (s *Service) process(ctx context.Context, b alert.Batch) {
	start := time.Now()
	investigationCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	result, err := s.investigate(investigationCtx, b)
	code := failureCode(err, investigationCtx.Err())
	cancel()
	status := "completed"
	if err != nil {
		status = "failed"
		result.Actions = nil
	}
	text := reportNotification(s.cfg, b, result, status, code)

	notifyCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	notifyErr := s.notify(notifyCtx, text, result.Actions)
	stop()
	log.Printf("analyzer status=%s failure_code=%s alerts=%d calls=%d tokens=%d duration=%s notification_ok=%v", status, code, len(b.Alerts), result.Calls, result.Tokens, time.Since(start).Round(time.Millisecond), notifyErr == nil)
}

// Emit stable categories instead of raw errors that may hold upstream data.
func failureCode(err, contextErr error) string {
	if err == nil {
		return "none"
	}
	if contextErr != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout_or_shutdown"
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "budget") || strings.Contains(text, "limit"):
		return "investigation_limit"
	case strings.Contains(text, "disallowed") || strings.Contains(text, "invalid tool"):
		return "tool_policy"
	case strings.Contains(text, "MCP"):
		return "mcp_or_evidence"
	case strings.Contains(text, "model"):
		return "model_request"
	default:
		return "investigation_failed"
	}
}
