package analyzer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
)

// A signed watch batch of any shape is accepted or rejected without a server
// error, and the queue never retains more than its byte budget.
func FuzzReceive(f *testing.F) {
	now := time.Now().UTC()
	good, _ := json.Marshal(testBatch(now))
	f.Add(string(good), int64(0))
	f.Add(`{"source":"opsgate","sent_at":"`+now.Format(time.RFC3339)+`","alerts":[{"cluster":"c","kind":"Pod","name":"`+strings.Repeat("x", 300)+`","reason":"r","severity":"warning","time":"`+now.Format(time.RFC3339)+`","suppressed":-1}]}`, int64(1))
	f.Add(`{"source":"opsgate","dropped":-1}`, int64(0))
	key := []byte("0123456789abcdef")
	f.Fuzz(func(t *testing.T, body string, shift int64) {
		s := newService(Default(), key)
		s.now = func() time.Time { return now.Add(time.Duration(shift%3600) * time.Second) }
		r := httptest.NewRequest("POST", "/alerts", strings.NewReader(body))
		r.Header.Set("X-Opsgate-Signature", alert.Sign(key, []byte(body)))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code >= 500 && w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d for %q", w.Code, body)
		}
		if s.queuedBytes > s.queueByteLimit {
			t.Fatalf("queued %d bytes over %d", s.queuedBytes, s.queueByteLimit)
		}
		for _, j := range s.jobs {
			for _, a := range j.batch.Alerts {
				if !alertFieldsFit(a) || a.Display != nil {
					t.Fatalf("queued unchecked alert %+v", a)
				}
			}
		}
		unsigned := httptest.NewRequest("POST", "/alerts", strings.NewReader(body))
		unsigned.Header.Set("X-Opsgate-Signature", alert.Sign([]byte("another key 0123"), []byte(body)))
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, unsigned)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("wrong key: %d", w.Code)
		}
	})
}
