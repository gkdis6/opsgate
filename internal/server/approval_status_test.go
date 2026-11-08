package server

import (
	"context"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/approval"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestWebApprovalStatusRegistration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, denied := range []bool{false, true} {
			cfg := config.Default()
			cfg.Approval.Web = enabled
			if denied && enabled {
				cfg.Server.Tools.Deny = []string{"approval_status"}
			}
			env := &tool.Env{Confirm: approval.New(cfg.Approval, "https://ops.example", unusedBrowser{}, nil)}
			s, err := New(cfg, env)
			if err != nil {
				t.Fatal(err)
			}
			cs := connect(t, s)
			tools, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if enabled && !denied {
				want = 1
			}
			if len(tools.Tools) != want {
				t.Fatalf("web %v denied %v: %+v", enabled, denied, tools.Tools)
			}
			if !strings.Contains(cs.InitializeResult().Instructions, "approval_status") {
				t.Fatal("instructions omit status retrieval")
			}
		}
	}
}
