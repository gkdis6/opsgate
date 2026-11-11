package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExposition(t *testing.T) {
	c := NewCounter("test_events_total", "Test events.", "tool", "outcome")
	c.Inc("k8s_get", "ok")
	c.Inc("k8s_get", "ok")
	c.Inc("a\"b\n", "denied")
	c.Inc("wrong arity")
	Gauge("test_pending", "Pending things.", func() float64 { return 1 })
	Gauge("test_pending", "Pending things.", func() float64 { return 3 })
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()
	for _, want := range []string{
		"# TYPE test_events_total counter\n",
		`test_events_total{tool="k8s_get",outcome="ok"} 2` + "\n",
		`test_events_total{tool="a\"b\n",outcome="denied"} 1` + "\n",
		"# TYPE test_pending gauge\ntest_pending 3\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Count(body, "test_pending ") != 3 { // HELP, TYPE and one sample
		t.Errorf("gauge registered twice:\n%s", body)
	}
	if w := httptest.NewRecorder(); true {
		Handler().ServeHTTP(w, httptest.NewRequest("POST", "/metrics", nil))
		if w.Code != 405 {
			t.Fatalf("POST: %d", w.Code)
		}
	}
}

// Label values past the bound collapse instead of growing without limit.
func TestSeriesBound(t *testing.T) {
	c := NewCounter("test_bound_total", "Bound.", "v")
	for i := range maxSeries + 500 {
		c.Inc(strings.Repeat("x", i%90) + string(rune('a'+i%26)) + strings.Repeat("y", i/90))
	}
	c.mu.Lock()
	n := len(c.values)
	c.mu.Unlock()
	if n > maxSeries+1 {
		t.Fatalf("%d series", n)
	}
}
