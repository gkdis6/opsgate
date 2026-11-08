package approval

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestWindowConfig(t *testing.T) {
	for _, tc := range []struct {
		name     string
		window   time.Duration
		patterns []string
		valid    bool
	}{
		{"off", 0, nil, true},
		{"minimum", time.Minute, []string{"note_*", "k8s_?cale"}, true},
		{"maximum", 12 * time.Hour, []string{"k8s_scale"}, true},
		{"negative", -time.Minute, nil, false},
		{"too short", time.Minute - 1, nil, false},
		{"too long", 12*time.Hour + 1, nil, false},
		{"invalid glob while off", 0, []string{"["}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.HTTP.PublicURL, cfg.Auth.Mode = "https://ops.example", config.AuthBuiltin
			cfg.Approval.Window, cfg.Approval.WindowTools = tc.window, tc.patterns
			for _, validate := range []func() error{cfg.Validate, cfg.ValidateHTTP} {
				if err := validate(); (err == nil) != tc.valid {
					t.Fatalf("validation = %v", err)
				}
			}
		})
	}
}

func TestSummaryLimit(t *testing.T) {
	for _, size := range []int{32 << 10, 64 << 10, 64<<10 + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			m, _ := newWebManager(t)
			r := approvalRequest()
			r.Summary = strings.Repeat("x", size)
			_, err := m.Confirm(context.Background(), nil, r)
			if size <= 64<<10 && !errors.Is(err, tool.ErrApprovalPending) || size > 64<<10 && !errors.Is(err, tool.ErrDenied) {
				t.Fatalf("size %d: %v", size, err)
			}
		})
	}
}
