package kube

import (
	"sync"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	"k8s.io/apimachinery/pkg/types"
)

type writeKey struct {
	user, transport, action, kind, namespace, name string
	replicas                                       int32
}

type writeSnapshot struct {
	uid              types.UID
	version, summary string
	expires          time.Time
}

// The shared write contract passes inputs by value and carries no plan state.
// Keep the first snapshot until consumed; a repeated preview cannot replace it.
type writeSnapshots struct {
	mu      sync.Mutex
	pending map[writeKey]writeSnapshot
}

func (s *writeSnapshots) remember(key writeKey, uid types.UID, version, summary string) (tool.Plan, error) {
	if uid == "" || version == "" {
		return tool.Plan{}, tool.Denied("resource identity or version unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.pending == nil {
		s.pending = make(map[writeKey]writeSnapshot)
	}
	for k, snapshot := range s.pending {
		if !now.Before(snapshot.expires) {
			delete(s.pending, k)
		}
	}
	if old, ok := s.pending[key]; ok {
		if old.uid != uid || old.version != version || old.summary != summary {
			return tool.Plan{}, tool.Denied("resource changed since preview; wait for pending preview to expire")
		}
		return tool.Plan{Summary: old.summary}, nil
	}
	if len(s.pending) >= 1024 {
		return tool.Plan{}, tool.Denied("too many pending write previews")
	}
	s.pending[key] = writeSnapshot{uid: uid, version: version, summary: summary, expires: now.Add(10 * time.Minute)}
	return tool.Plan{Summary: summary}, nil
}

func (s *writeSnapshots) take(key writeKey) (writeSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.pending[key]
	delete(s.pending, key)
	if !ok || !time.Now().Before(snapshot.expires) {
		return writeSnapshot{}, tool.Denied("write preview missing or expired")
	}
	return snapshot, nil
}
