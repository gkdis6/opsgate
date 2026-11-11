// Package metrics keeps a few counters and gauges and writes them in the
// Prometheus text format. It has no dependencies and no label taken from
// request input: every label value comes from a fixed set in this program,
// so the output stays small whatever clients send.
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Counter counts events by a fixed list of labels.
type Counter struct {
	name, help string
	labels     []string
	mu         sync.Mutex
	values     map[string]uint64
}

type gauge struct {
	name, help string
	read       func() float64
}

var (
	registry sync.Mutex
	counters []*Counter
	gauges   []gauge

	// AuditEvents counts audited events: tool calls, approval decisions and
	// recovery results, by tool, outcome and approval method.
	AuditEvents = NewCounter("opsgate_audit_events_total", "Audited events by tool, outcome and approval method.", "tool", "outcome", "approval")
	// PushDeliveries counts Web Push sends by what the push service answered.
	PushDeliveries = NewCounter("opsgate_push_deliveries_total", "Web Push deliveries by result (accepted, gone, failed).", "result")
	// InboxNotices counts signed notice posts by result.
	InboxNotices = NewCounter("opsgate_inbox_notices_total", "Signed inbox notice posts by result (accepted, rejected).", "result")
)

// NewCounter registers a counter. Call it from package variables only.
func NewCounter(name, help string, labels ...string) *Counter {
	c := &Counter{name: name, help: help, labels: labels, values: map[string]uint64{}}
	registry.Lock()
	counters = append(counters, c)
	registry.Unlock()
	return c
}

// maxSeries bounds one counter. Label values are meant to come from fixed
// sets; past the bound further combinations are counted together.
const maxSeries = 2000

// Inc adds one for the given label values, in the order of the labels.
func (c *Counter) Inc(values ...string) {
	if len(values) != len(c.labels) {
		return
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = c.labels[i] + `="` + escape(v) + `"`
	}
	key := strings.Join(parts, ",")
	c.mu.Lock()
	if _, ok := c.values[key]; !ok && len(c.values) >= maxSeries {
		for i := range parts {
			parts[i] = c.labels[i] + `="other"`
		}
		key = strings.Join(parts, ",")
	}
	c.values[key]++
	c.mu.Unlock()
}

// Gauge registers a value read at scrape time. A second registration under
// the same name replaces the first, so a restarted component reports itself.
func Gauge(name, help string, read func() float64) {
	registry.Lock()
	defer registry.Unlock()
	for i := range gauges {
		if gauges[i].name == name {
			gauges[i].read = read
			return
		}
	}
	gauges = append(gauges, gauge{name, help, read})
}

func escape(v string) string {
	if len(v) > 100 {
		v = v[:100]
	}
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// Handler serves the registered metrics. It needs no authentication because
// it is meant for a listener that only the cluster's scraper can reach.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var out strings.Builder
		registry.Lock()
		cs, gs := append([]*Counter(nil), counters...), append([]gauge(nil), gauges...)
		registry.Unlock()
		for _, c := range cs {
			fmt.Fprintf(&out, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
			c.mu.Lock()
			keys := make([]string, 0, len(c.values))
			for k := range c.values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&out, "%s{%s} %d\n", c.name, k, c.values[k])
			}
			c.mu.Unlock()
		}
		for _, g := range gs {
			fmt.Fprintf(&out, "# HELP %s %s\n# TYPE %s gauge\n%s %g\n", g.name, g.help, g.name, g.name, g.read())
		}
		_, _ = w.Write([]byte(out.String()))
	})
}
