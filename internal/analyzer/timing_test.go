package analyzer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type timedSession struct {
	fakeSession
	advance func(time.Duration)
}

func (s *timedSession) Call(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	s.advance(12 * time.Second)
	return s.fakeSession.Call(ctx, name, args)
}

type timedModel struct {
	fakeModel
	advance func(time.Duration)
	fail    bool
}

func (m *timedModel) Complete(ctx context.Context, messages []message, defs []definition, n int) (modelReply, error) {
	if m.fail {
		m.advance(3 * time.Second)
		return modelReply{}, context.DeadlineExceeded
	}
	m.advance(24 * time.Second)
	return m.fakeModel.Complete(ctx, messages, defs, n)
}

func TestInvestigationTimingFooterAndFailure(t *testing.T) {
	for _, language := range []string{"ko", "en"} {
		t.Run(language, func(t *testing.T) {
			now := time.Now()
			advance := func(d time.Duration) { now = now.Add(d) }
			c := Default()
			c.Report.Language = language
			m := &timedModel{advance: advance}
			s := &timedSession{advance: advance}
			e := engine{cfg: c, model: m, now: func() time.Time { return now }, dial: func(context.Context) (toolSession, error) { return s, nil }}
			result, err := e.investigate(context.Background(), testBatch(now))
			if err != nil {
				t.Fatal(err)
			}
			want := "1m12s (model 48s)"
			if language == "ko" {
				want = "소요 1분 12초 (모델 48초)"
			}
			if !strings.Contains(result.Text, want) || result.Total != 72*time.Second || result.ModelTime != 48*time.Second {
				t.Fatalf("timing missing: %+v", result)
			}
			m.fail = true
			result, err = e.investigate(context.Background(), testBatch(now))
			if err == nil {
				t.Fatal("timeout accepted")
			}
			notice := reportNotification(c, testBatch(now), result, "failed", "timeout_or_shutdown")
			want = "Elapsed: 15s"
			if language == "ko" {
				want = "소요 15초"
			}
			if !strings.Contains(notice, want) || !result.Started {
				t.Fatalf("failure timing missing: %s", notice)
			}
		})
	}
}

func TestReportDurationRounding(t *testing.T) {
	for _, tc := range []struct {
		d      time.Duration
		en, ko string
	}{
		{0, "1s", "1초"}, {400 * time.Millisecond, "1s", "1초"}, {1500 * time.Millisecond, "2s", "2초"},
		{59 * time.Second, "59s", "59초"}, {59600 * time.Millisecond, "1m0s", "1분 0초"}, {72 * time.Second, "1m12s", "1분 12초"},
	} {
		if got := reportDuration(tc.d, "en"); got != tc.en {
			t.Errorf("English %v: %s", tc.d, got)
		}
		if got := reportDuration(tc.d, "ko"); got != tc.ko {
			t.Errorf("Korean %v: %s", tc.d, got)
		}
	}
}
