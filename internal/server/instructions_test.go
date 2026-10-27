package server

import (
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestNoteInstructions(t *testing.T) {
	for _, tc := range []struct {
		name                string
		enabled, allowWrite bool
		search, record      bool
	}{
		{"disabled", false, false, false, false},
		{"read only", true, false, true, false},
		{"writable", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Providers.Note.Enabled = tc.enabled
			cfg.Providers.Note.AllowWrite = tc.allowWrite
			cfg.Providers.Note.Vault = t.TempDir()
			logger, err := audit.Open("")
			if err != nil {
				t.Fatal(err)
			}
			env := &tool.Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 4096,
				Fallback: &tool.Caller{User: "local", Transport: "stdio", Scopes: config.Scopes}}
			s, err := New(cfg, env)
			if err != nil {
				t.Fatal(err)
			}
			got := connect(t, s).InitializeResult().Instructions
			if !strings.HasPrefix(got, instructions) ||
				strings.Contains(got, "search notes") != tc.search || strings.Contains(got, "offer to record") != tc.record {
				t.Fatalf("instructions = %q", got)
			}
		})
	}
}
