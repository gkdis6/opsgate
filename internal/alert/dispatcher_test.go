package alert

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

type testSender struct {
	name string
	send func(context.Context, *Batch) (bool, error)
}

func (s testSender) Name() string                                     { return s.name }
func (s testSender) Send(ctx context.Context, b *Batch) (bool, error) { return s.send(ctx, b) }

func TestIndependentDispatchers(t *testing.T) {
	blocked, release, delivered := make(chan struct{}), make(chan struct{}), make(chan *Batch, 1)
	defer close(release)
	slow, _ := newTest(t, Options{Sender: testSender{"slow", func(ctx context.Context, _ *Batch) (bool, error) {
		close(blocked)
		select {
		case <-ctx.Done():
		case <-release:
		}
		return false, errors.New("unavailable")
	}}})
	fast, _ := newTest(t, Options{Sender: testSender{"fast", func(_ context.Context, b *Batch) (bool, error) { delivered <- b; return false, nil }}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slow.Emit(warning("a", "Error"))
	fast.Emit(warning("a", "Error"))
	slowDone, fastDone := make(chan struct{}), make(chan struct{})
	go func() { slow.Run(ctx); close(slowDone) }()
	go func() { fast.Run(ctx); close(fastDone) }()
	cancel() // Both dispatchers flush on shutdown.
	<-blocked
	select {
	case b := <-delivered:
		if len(b.Alerts) != 1 {
			t.Fatalf("%+v", b)
		}
	case <-time.After(time.Second):
		t.Fatal("slow channel blocked fast channel")
	}
	// The slow shutdown uses its own context, so release it explicitly.
	release <- struct{}{}
	<-slowDone
	<-fastDone
}

func TestDispatcherProblemsOnlyAndFailureName(t *testing.T) {
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	calls := 0
	d, _ := newTest(t, Options{ProblemsOnly: true, Sender: testSender{"email", func(_ context.Context, b *Batch) (bool, error) {
		calls++
		if len(b.Alerts) != 1 || b.Alerts[0].Severity != Warning || b.Dropped != 0 {
			t.Fatalf("%+v", b)
		}
		return false, errors.New("unavailable")
	}}})
	for range queueSize + 1 {
		d.Emit(Alert{Severity: Info})
	}
	d.Emit(warning("a", "Error"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Run(ctx)
	if calls != 1 || !strings.Contains(output.String(), "watch email: unavailable; 1 alerts lost") {
		t.Fatalf("calls=%d log=%s", calls, output.String())
	}
}
