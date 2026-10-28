package kube

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	durationutil "k8s.io/apimachinery/pkg/util/duration"
)

type eventsInput struct {
	Cluster      string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace    string `json:"namespace,omitempty" jsonschema:"Namespace; omitted means all allowed namespaces"`
	WarningsOnly *bool  `json:"warnings_only,omitempty" jsonschema:"Only warnings; default true"`
	Since        string `json:"since,omitempty" jsonschema:"Recent event window as a positive Go duration; default 1h; maximum 168h"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum events; default 50; clamped to 1 through 500"`
}

func duration(s, def string) (time.Duration, error) {
	if s == "" {
		s = def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > 7*24*time.Hour {
		return 0, fmt.Errorf("since must be a positive Go duration no greater than 168h")
	}
	return d, nil
}

func (p *provider) events(ctx context.Context, _ tool.Caller, in eventsInput) (string, error) {
	warnings := in.WarningsOnly == nil || *in.WarningsOnly
	return p.recentEvents(ctx, in.Namespace, warnings, in.Since, clamp(in.Limit, 50, 500), "", "")
}

func eventTime(e corev1.Event) time.Time {
	if e.Series != nil && !e.Series.LastObservedTime.IsZero() {
		return e.Series.LastObservedTime.Time
	}
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}

const maxEventsScanned = 5000

// Retain table fields only, ordered oldest first so the heap stays bounded
// while later pages can still supply newer events.
type eventRow struct {
	last                                        time.Time
	namespace, kind, name, typ, reason, message string
	order                                       int
}

type eventHeap []eventRow

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].last.Equal(h[j].last) {
		return h[i].order > h[j].order
	}
	return h[i].last.Before(h[j].last)
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(v any)   { *h = append(*h, v.(eventRow)) }
func (h *eventHeap) Pop() any {
	old := *h
	v := old[len(old)-1]
	old[len(old)-1] = eventRow{}
	*h = old[:len(old)-1]
	return v
}

func (p *provider) recentEvents(ctx context.Context, ns string, warnings bool, since string, limit int, name, kind string) (string, error) {
	d, err := duration(since, "1h")
	if err != nil {
		return "", err
	}
	scopes, err := p.namespaces(ctx, ns)
	if err != nil {
		return "", err
	}
	selector := fields.Set{}
	if warnings {
		selector["type"] = "Warning"
	}
	if name != "" {
		selector["involvedObject.name"] = name
		selector["involvedObject.namespace"] = ns
		selector["involvedObject.kind"] = kind
	}
	limit = clamp(limit, 50, 500)
	events := make(eventHeap, 0, limit)
	scanned, matched := 0, 0
	scanCapped := false
	cutoff := time.Now().Add(-d)
scan:
	for scopeIndex, scope := range scopes {
		opts := metav1.ListOptions{FieldSelector: selector.AsSelector().String(), Limit: int64(min(limit+1, 500))}
		for {
			list, err := p.client.CoreV1().Events(scope).List(ctx, opts)
			if err != nil {
				return "", err
			}
			for i, e := range list.Items {
				scanned++
				if scanned == maxEventsScanned {
					scanCapped = i+1 < len(list.Items) || list.Continue != "" || scopeIndex+1 < len(scopes)
				}
				last := eventTime(e)
				// Filter locally too: clients and API proxies need not honor selectors.
				eligible := p.policy.allowed(e.Namespace) && (e.InvolvedObject.Namespace == "" || p.policy.allowed(e.InvolvedObject.Namespace)) && !last.Before(cutoff)
				if eligible && (!warnings || e.Type == "Warning") && (name == "" || (e.InvolvedObject.Name == name && e.InvolvedObject.Namespace == ns && e.InvolvedObject.Kind == kind)) {
					matched++
					row := eventRow{last: last, namespace: e.Namespace, kind: e.InvolvedObject.Kind, name: e.InvolvedObject.Name, typ: e.Type, reason: e.Reason, message: e.Message, order: scanned}
					if len(events) < limit {
						heap.Push(&events, row)
					} else if row.last.After(events[0].last) {
						events[0] = row
						heap.Fix(&events, 0)
					}
				}
				if scanned == maxEventsScanned {
					break scan
				}
			}
			opts.Continue = list.Continue
			if opts.Continue == "" {
				break
			}
		}
	}
	sort.Slice(events, func(i, j int) bool { return events.Less(j, i) })
	truncated := scanCapped || matched > limit
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	// AGE follows kubectl: time since the event was last seen, not since the
	// (possibly long-lived, aggregated) Event object was created.
	fmt.Fprintln(w, "AGE\tLAST SEEN\tTYPE\tREASON\tOBJECT\tMESSAGE")
	for _, e := range events {
		last := e.last.UTC().Format(time.RFC3339)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s/%s/%s\t%s\n", age(last), last, display(e.typ), display(e.reason), display(e.namespace), display(e.kind), display(e.name), display(e.message))
	}
	w.Flush()
	if truncated {
		fmt.Fprintf(&b, "... truncated to %d events\n", limit)
	}
	if scanCapped {
		fmt.Fprintf(&b, "... event scan capped at %d; newer events may be omitted\n", maxEventsScanned)
	}
	return b.String(), nil
}

func age(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return "-"
	}
	return durationutil.HumanDuration(time.Since(t))
}
