package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
