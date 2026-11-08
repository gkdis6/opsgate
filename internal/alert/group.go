package alert

import (
	"context"
	"log"
	"path"
	"sync"
	"time"
)

// Group selects namespaces in configuration order. Outputs name loaded senders.
// Settings must be validated by the caller before construction.
type Group struct {
	Name         string
	Namespaces   []string
	Outputs      []string
	ProblemsOnly bool
	Batch        time.Duration
}

type groupRoute struct {
	group   Group
	outputs []*Dispatcher
}

// Router chooses one group before applying any severity or output filters.
// With no groups it preserves the original broadcast dispatchers unchanged.
type Router struct {
	routes      []groupRoute
	dispatchers []*Dispatcher
	unmatched   *Dispatcher
}

// NewRouter gives every (group, output) independent queues, cooldown and retries.
// Senders are immutable after construction and may be shared by dispatchers.
func NewRouter(groups []Group, outputs []*Dispatcher) *Router {
	r := &Router{}
	if len(groups) == 0 {
		r.dispatchers = outputs
		return r
	}
	byName := map[string]*Dispatcher{}
	for _, d := range outputs {
		byName[d.opts.Sender.Name()] = d
	}
	for _, g := range groups {
		route := groupRoute{group: g}
		for _, name := range g.Outputs {
			opts := byName[name].opts
			opts.Group, opts.Batch = g.Name, g.Batch
			opts.ProblemsOnly = opts.ProblemsOnly || g.ProblemsOnly
			d := New(opts)
			route.outputs = append(route.outputs, d)
			r.dispatchers = append(r.dispatchers, d)
		}
		r.routes = append(r.routes, route)
	}
	// Unmatched alerts have no authorized destination. Reuse dropped accounting
	// and rendering, but send only a local summary, never the alert or namespace.
	r.unmatched = New(Options{Sender: dropSummary{}, Batch: time.Minute, MaxAlerts: 1})
	r.dispatchers = append(r.dispatchers, r.unmatched)
	return r
}

func matchesNamespace(patterns []string, namespace string) bool {
	for _, pattern := range patterns {
		if namespace == "" {
			if pattern == "" || pattern == "*" {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pattern, namespace); ok {
			return true
		}
	}
	return false
}

// Emit never blocks watchers. A matched group that filters an alert does not
// fall through to later groups, including catch-all groups.
func (r *Router) Emit(a Alert) {
	if r.unmatched == nil {
		for _, d := range r.dispatchers {
			d.Emit(a)
		}
		return
	}
	for _, route := range r.routes {
		if matchesNamespace(route.group.Namespaces, a.Namespace) {
			for _, d := range route.outputs {
				d.Emit(a)
			}
			return
		}
	}
	r.unmatched.mu.Lock()
	r.unmatched.dropped = increment(r.unmatched.dropped)
	r.unmatched.mu.Unlock()
}

// Run stops and flushes all deliveries when ctx ends.
func (r *Router) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, d := range r.dispatchers {
		wg.Go(func() { d.Run(ctx) })
	}
	wg.Wait()
}

type dropSummary struct{}

func (dropSummary) Name() string { return "unmatched" }
func (dropSummary) Send(_ context.Context, b *Batch) (bool, error) {
	log.Printf("watch unmatched: %s (no matching group)", b.Text)
	return false, nil
}
