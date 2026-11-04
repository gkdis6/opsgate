// Package alert batches and delivers watch alerts.
//
// Delivery is best effort: a slow or failing channel never blocks the
// watchers, and alerts that do not fit are counted as dropped.
package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Severities.
const (
	Warning = "warning"
	Info    = "info"
)

// Alert is one observed change. Text fields must already be redacted.
type Alert struct {
	Time      time.Time `json:"time"`
	Cluster   string    `json:"cluster"`
	Severity  string    `json:"severity"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace,omitempty"`
	Name      string    `json:"name"`
	// Reason is a short machine-readable cause, e.g. OOMKilled or ImageChanged.
	Reason  string `json:"reason"`
	Summary string `json:"summary"`
	// Hint is fixed advice chosen by the watcher. It never contains cluster text.
	Hint string `json:"hint,omitempty"`
	// Restart names a workload a person may choose to restart. Nothing acts on
	// it automatically.
	Restart *Workload `json:"restart,omitempty"`
	// Suppressed counts repeats folded into this alert during the cooldown.
	Suppressed int `json:"suppressed,omitempty"`
	// Key identifies repeats; it includes the container for pod alerts.
	Key string `json:"-"`
}

// Workload identifies the target of a suggested k8s_rollout_restart call.
type Workload struct {
	// Kind is deployment, statefulset or daemonset.
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// URL opens the approval page for this restart, if the watcher knows it.
	URL string `json:"url,omitempty"`
}

// Batch is one delivery, also the JSON body of a webhook POST.
type Batch struct {
	Source string    `json:"source"`
	SentAt time.Time `json:"sent_at"`
	Alerts []Alert   `json:"alerts"`
	// Dropped counts alerts lost to the queue or batch limit.
	Dropped int `json:"dropped,omitempty"`
	// Text renders the batch for chat forwarding without further code.
	Text string `json:"text"`
}

// Sender delivers one batch.
type Sender interface {
	Name() string
	Send(context.Context, *Batch) (retry bool, err error)
}

// Options configures a Dispatcher.
type Options struct {
	// Sender delivers batches; nil posts them to URL as a webhook.
	Sender Sender
	// ProblemsOnly drops every alert that is not a warning.
	ProblemsOnly bool
	URL          string
	Secret       []byte
	Batch        time.Duration
	Cooldown     time.Duration
	MaxAlerts    int
	// PrivateOnly refuses connections to addresses that are neither
	// private nor loopback, checked after DNS resolution. Use it for
	// plain http, where a host name could otherwise lead anywhere.
	PrivateOnly bool
	Client      *http.Client
}

const queueSize = 1000

// Dispatcher gathers and delivers alerts in batches.
type Dispatcher struct {
	opts  Options
	queue chan Alert

	mu      sync.Mutex
	dropped int
	// last holds when each warning key was last sent; repeats within the
	// cooldown only increment suppressed.
	last       map[string]time.Time
	suppressed map[string]int
	now        func() time.Time
	// backoff holds the wait before each delivery attempt.
	backoff []time.Duration
}

// New returns a Dispatcher; call Run to deliver.
func New(opts Options) *Dispatcher {
	if opts.Sender == nil {
		opts.Sender = newWebhook(opts)
	}
	return &Dispatcher{opts: opts, queue: make(chan Alert, queueSize), last: map[string]time.Time{}, suppressed: map[string]int{}, now: time.Now, backoff: []time.Duration{0, time.Second, 4 * time.Second}}
}

// Emit queues a without blocking.
func (d *Dispatcher) Emit(a Alert) {
	if d.opts.ProblemsOnly && a.Severity != Warning {
		return
	}
	select {
	case d.queue <- a:
	default:
		d.mu.Lock()
		d.dropped++
		d.mu.Unlock()
	}
}

// Run delivers batches until ctx ends, then sends what is pending.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.opts.Batch)
	defer ticker.Stop()
	var pending []Alert
	for {
		select {
		case a := <-d.queue:
			pending = d.add(pending, a)
		case <-ticker.C:
			pending = d.flush(ctx, pending)
		case <-ctx.Done():
			for {
				select {
				case a := <-d.queue:
					pending = d.add(pending, a)
				default:
					shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					d.flush(shutdown, pending)
					cancel()
					return
				}
			}
		}
	}
}

// add bounds the alerts held between flushes to the queue size.
func (d *Dispatcher) add(pending []Alert, a Alert) []Alert {
	if len(pending) >= queueSize {
		d.mu.Lock()
		d.dropped++
		d.mu.Unlock()
		return pending
	}
	return append(pending, a)
}

func (d *Dispatcher) flush(ctx context.Context, pending []Alert) []Alert {
	batch := d.prepare(pending)
	if batch == nil {
		return pending[:0]
	}
	if err := d.post(ctx, batch); err != nil {
		log.Printf("watch %s: %v; %d alerts lost", d.opts.Sender.Name(), err, len(batch.Alerts))
	}
	return pending[:0]
}

// prepare applies coalescing, cooldown and the batch limit. It returns nil
// when there is nothing to send.
func (d *Dispatcher) prepare(pending []Alert) *Batch {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	var alerts []Alert
	index := map[string]int{}
	for _, a := range pending {
		if a.Key == "" {
			a.Key = strings.Join([]string{a.Cluster, a.Kind, a.Namespace, a.Name, a.Reason}, "/")
		}
		if i, ok := index[a.Key]; ok {
			// Keep the first alert of a burst, count the rest.
			alerts[i].Suppressed++
			if a.Severity == Info {
				alerts[i].Summary = a.Summary
				alerts[i].Time = a.Time
			}
			continue
		}
		if a.Severity == Warning && d.opts.Cooldown > 0 {
			if sent, ok := d.last[a.Key]; ok && now.Sub(sent) < d.opts.Cooldown {
				d.suppressed[a.Key]++
				continue
			}
			a.Suppressed += d.suppressed[a.Key]
			delete(d.suppressed, a.Key)
			d.last[a.Key] = now
		}
		index[a.Key] = len(alerts)
		alerts = append(alerts, a)
	}
	// Suppressed repeats ride along with the key's next alert; keys that
	// stay quiet for a day are forgotten so pod churn cannot grow the maps.
	for key, sent := range d.last {
		if age := now.Sub(sent); age >= d.opts.Cooldown && (d.suppressed[key] == 0 || age >= 24*time.Hour) {
			delete(d.last, key)
			delete(d.suppressed, key)
		}
	}
	dropped := d.dropped
	if len(alerts) > d.opts.MaxAlerts {
		dropped += len(alerts) - d.opts.MaxAlerts
		alerts = alerts[:d.opts.MaxAlerts]
	}
	if len(alerts) == 0 && dropped == 0 {
		return nil
	}
	d.dropped = 0
	b := &Batch{Source: "opsgate", SentAt: now.UTC(), Alerts: alerts, Dropped: dropped}
	b.Text = render(b)
	return b
}

func render(b *Batch) string { return renderAlerts(b, true) }

// renderAlerts writes one line per alert, plus indented advice lines if asked.
func renderAlerts(b *Batch, advice bool) string {
	var s strings.Builder
	for _, a := range b.Alerts {
		object := a.Kind + " " + a.Name
		if a.Namespace != "" {
			object = a.Kind + " " + a.Namespace + "/" + a.Name
		}
		fmt.Fprintf(&s, "[%s] %s %s: %s", a.Severity, a.Cluster, object, a.Summary)
		if a.Suppressed > 0 {
			fmt.Fprintf(&s, " (+%d similar)", a.Suppressed)
		}
		s.WriteByte('\n')
		if advice && a.Hint != "" {
			fmt.Fprintf(&s, "  hint: %s\n", a.Hint)
		}
		if advice && a.Restart != nil {
			fmt.Fprintf(&s, "  if needed, ask to restart: %s %s/%s\n", a.Restart.Kind, a.Restart.Namespace, a.Restart.Name)
			if a.Restart.URL != "" {
				fmt.Fprintf(&s, "  or approve it here: %s\n", a.Restart.URL)
			}
		}
	}
	if b.Dropped > 0 {
		fmt.Fprintf(&s, "%d alerts dropped\n", b.Dropped)
	}
	return strings.TrimSuffix(s.String(), "\n")
}

// post sends b with bounded retries.
func (d *Dispatcher) post(ctx context.Context, b *Batch) error {
	var last error
	for _, wait := range d.backoff {
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		retry, err := d.opts.Sender.Send(ctx, b)
		if err == nil {
			return nil
		}
		last = err
		if !retry {
			break
		}
	}
	return last
}

// Sign returns the X-Opsgate-Signature value for a body:
// "sha256=" + hex(HMAC-SHA256(secret, body)). Receivers check freshness
// with the signed sent_at field.
func Sign(secret []byte, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
