package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
)

func TestWatchPasswordStartup(t *testing.T) {
	cfg := config.Default()
	cfg.Watch.Email = config.WatchEmail{Host: "smtp.example.com", Username: "alerts@example.com", PasswordFile: filepath.Join(t.TempDir(), "password")}
	for _, value := range []string{"missing", "", " \r\n\t ", "  test-password\n"} {
		if value != "missing" {
			if err := os.WriteFile(cfg.Watch.Email.PasswordFile, []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := watchDispatchers(cfg.Watch, nil)
		if value == "  test-password\n" {
			if err != nil || len(got) != 1 {
				t.Fatalf("dispatchers=%v err=%v", got, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "watch.email.password_file") {
			t.Fatalf("%q: %v", value, err)
		}
	}
}

func TestAddApprovalLink(t *testing.T) {
	a := alert.Alert{Cluster: "main", Restart: &alert.Workload{Kind: "deployment", Namespace: "apps", Name: "web"}}
	addApprovalLink(&a, "")
	if a.Restart.URL != "" {
		t.Fatalf("link without a base: %q", a.Restart.URL)
	}
	addApprovalLink(&a, "https://ops.example")
	if a.Restart.URL != "https://ops.example/approve/restart/main/deployment/apps/web" {
		t.Fatalf("link = %q", a.Restart.URL)
	}
	plain := alert.Alert{Cluster: "main"}
	addApprovalLink(&plain, "https://ops.example")
	if plain.Restart != nil {
		t.Fatal("alert gained a restart target")
	}
}
