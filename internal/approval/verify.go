package approval

import (
	"context"
	"time"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

// Recovery states of an applied action whose plan carries a check.
const (
	recoveryChecking   = "checking"
	recoveryRecovered  = "recovered"
	recoveryFailing    = "not_recovered"
	recoveryUnverified = "unverified"
)

// recovery is what the approval page reports after "Applied": whether the
// target is serving again, not only whether the API accepted the change.
type recovery struct {
	state, detail string
	at            time.Time
}

// startRecoveryLocked begins observing an applied write. m.mu must be held.
func (m *Manager) startRecoveryLocked(p *pending, check tool.Check) {
	if check == nil || m.verifyFor <= 0 {
		return
	}
	p.recovery = recovery{state: recoveryChecking, detail: "waiting for the first observation", at: m.now()}
	// Keep the record past the check so its outcome can still be read.
	if keep := m.now().Add(m.verifyFor + 10*time.Minute); keep.After(p.retainUntil) {
		p.retainUntil = keep
	}
	go m.observe(p, check, m.verifyFor, m.verifyEvery)
}

// observe polls check until the target is healthy twice in a row, reports a
// final failure, or verifyFor elapses. Two healthy observations avoid
// reporting recovery from a status the controller has not updated yet.
func (m *Manager) observe(p *pending, check tool.Check, limit, every time.Duration) {
	deadline := time.Now().Add(limit)
	timer := time.NewTimer(every)
	defer timer.Stop()
	healthy, observed := 0, false
	last := ""
	for {
		select {
		case <-m.stop:
			return
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		h, err := check(ctx)
		cancel()
		if err == nil {
			observed, last = true, redact.String(h.Detail)
			switch {
			case h.Done && !h.Healthy:
				m.finishRecovery(p, recoveryFailing, last)
				return
			case h.Healthy:
				healthy++
				if healthy >= 2 {
					m.finishRecovery(p, recoveryRecovered, last)
					return
				}
			default:
				healthy = 0
			}
			m.mu.Lock()
			p.recovery.detail = last
			m.mu.Unlock()
		} else {
			healthy = 0
		}
		if !time.Now().Before(deadline) {
			if !observed {
				m.finishRecovery(p, recoveryUnverified, "could not read the target")
			} else {
				m.finishRecovery(p, recoveryFailing, last)
			}
			return
		}
		timer.Reset(every)
	}
}

func (m *Manager) finishRecovery(p *pending, state, detail string) {
	m.mu.Lock()
	p.recovery = recovery{state: state, detail: detail, at: m.now()}
	if keep := m.now().Add(10 * time.Minute); keep.After(p.retainUntil) {
		p.retainUntil = keep
	}
	user, name, source, prefix := p.user, p.tool, p.source, p.id[:8]
	m.mu.Unlock()
	m.audit.Record(audit.Event{Time: m.now(), User: redact.String(user), Transport: "web", Tool: name, Source: source,
		Outcome: state, Approval: "web", Plan: detail, Args: map[string]any{"approval_id_prefix": prefix}})
	if m.push != nil {
		m.push.enqueue(user, m.now().Add(time.Hour))
	}
}

// recoveryHeading is the page heading for a recovery state.
func recoveryHeading(state string) string {
	switch state {
	case recoveryChecking:
		return "Applied, checking recovery / 적용됨, 복구 확인 중"
	case recoveryRecovered:
		return "Recovered / 복구됨"
	case recoveryFailing:
		return "Not recovered yet / 아직 복구되지 않음"
	case recoveryUnverified:
		return "Applied, recovery unknown / 적용됨, 복구 여부 모름"
	}
	return ""
}

// recoveryText is a one-line recovery summary for the inbox.
func recoveryText(r recovery) string {
	if r.state == "" {
		return ""
	}
	if r.detail == "" {
		return recoveryHeading(r.state)
	}
	return recoveryHeading(r.state) + " (" + r.detail + ")"
}
