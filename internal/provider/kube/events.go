package kube

import (
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
	var events []corev1.Event
	cutoff := time.Now().Add(-d)
	for _, scope := range scopes {
		opts := metav1.ListOptions{FieldSelector: selector.AsSelector().String(), Limit: 500}
		for {
			list, err := p.client.CoreV1().Events(scope).List(ctx, opts)
			if err != nil {
				return "", err
			}
			for _, e := range list.Items {
				if !p.policy.allowed(e.Namespace) || (e.InvolvedObject.Namespace != "" && !p.policy.allowed(e.InvolvedObject.Namespace)) || eventTime(e).Before(cutoff) {
					continue
				}
				if warnings && e.Type != "Warning" {
					continue
				}
				if name != "" && (e.InvolvedObject.Name != name || e.InvolvedObject.Namespace != ns || e.InvolvedObject.Kind != kind) {
					continue
				}
				events = append(events, e)
			}
			opts.Continue = list.Continue
			if opts.Continue == "" {
				break
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return eventTime(events[i]).After(eventTime(events[j])) })
	truncated := len(events) > limit
	if truncated {
		events = events[:limit]
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	// AGE follows kubectl: time since the event was last seen, not since the
	// (possibly long-lived, aggregated) Event object was created.
	fmt.Fprintln(w, "AGE\tLAST SEEN\tTYPE\tREASON\tOBJECT\tMESSAGE")
	for _, e := range events {
		last := eventTime(e).UTC().Format(time.RFC3339)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s/%s/%s\t%s\n", age(last), last, display(e.Type), display(e.Reason), display(e.Namespace), display(e.InvolvedObject.Kind), display(e.InvolvedObject.Name), display(e.Message))
	}
	w.Flush()
	if truncated {
		fmt.Fprintf(&b, "... truncated to %d events\n", limit)
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
