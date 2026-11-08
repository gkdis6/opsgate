package alert

import (
	"context"
	"encoding/json"
	"github.com/gkdis6/opsgate/internal/hosttarget"
	"time"
)

// NoticeBatch is the signed, identifier-only watch-to-inbox protocol.
type NoticeBatch struct {
	SentAt  time.Time `json:"sent_at"`
	Notices []Notice  `json:"notices"`
}

type Notice struct {
	Cluster      string    `json:"cluster"`
	Namespace    string    `json:"namespace"`
	Pod          string    `json:"pod"`
	WorkloadKind string    `json:"workload_kind"`
	WorkloadName string    `json:"workload_name"`
	Reason       string    `json:"reason"`
	Severity     string    `json:"severity"`
	OccurredAt   time.Time `json:"occurred_at"`
	// HostKind is "unit" or "container" for a host target named by Pod; then
	// Cluster, Namespace and the workload fields are empty.
	HostKind string `json:"host_kind,omitempty"`
}

// Valid bounds all display and navigation fields and excludes free text.
func (n Notice) Valid() bool {
	if n.HostKind != "" {
		return n.Cluster == "" && n.Namespace == "" && n.WorkloadKind == "" && n.WorkloadName == "" && hosttarget.Valid(n.HostKind, n.Pod) &&
			noticeIdentifier(n.Reason, 128) && n.Severity == Warning && !n.OccurredAt.IsZero()
	}
	if !noticeIdentifier(n.Cluster, 64) || !noticeIdentifier(n.Namespace, 63) || !noticeIdentifier(n.Pod, 253) || !noticeIdentifier(n.Reason, 128) || n.Severity != Warning || n.OccurredAt.IsZero() {
		return false
	}
	if n.WorkloadKind == "" {
		return n.WorkloadName == ""
	}
	return (n.WorkloadKind == "deployment" || n.WorkloadKind == "statefulset" || n.WorkloadKind == "daemonset") && noticeIdentifier(n.WorkloadName, 253)
}

func noticeIdentifier(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	first := s[0]
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z' || first >= '0' && first <= '9') {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

type noticeSender struct{ webhook *webhookSender }

// NewNoticeSender reuses webhook signing, transport policy and retry decisions.
func NewNoticeSender(opts Options) Sender { return &noticeSender{newWebhook(opts).(*webhookSender)} }
func (s *noticeSender) Name() string      { return "inbox" }
func (s *noticeSender) Send(ctx context.Context, b *Batch) (bool, error) {
	batch := NoticeBatch{SentAt: b.SentAt}
	for _, a := range b.Alerts {
		if !choiceAlert(a) {
			continue
		}
		n := Notice{Cluster: a.Cluster, Namespace: a.Namespace, Pod: a.Name, Reason: a.Reason, Severity: a.Severity, OccurredAt: a.Time}
		if kind := HostKind(a); kind != "" {
			n = Notice{Pod: a.Name, HostKind: kind, Reason: a.Reason, Severity: a.Severity, OccurredAt: a.Time}
		}
		if a.Workload != nil {
			n.WorkloadKind, n.WorkloadName = a.Workload.Kind, a.Workload.Name
		}
		if n.Valid() {
			batch.Notices = append(batch.Notices, n)
		}
	}
	if len(batch.Notices) == 0 {
		return false, nil
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return false, err
	}
	return s.webhook.postBody(ctx, body)
}
