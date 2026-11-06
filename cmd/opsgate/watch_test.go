package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
		got, err := watchDispatchers(cfg.Watch)
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

func TestWatchOutputStartup(t *testing.T) {
	dir := t.TempDir()
	urlFile, headerFile, secretFile := filepath.Join(dir, "url"), filepath.Join(dir, "header"), filepath.Join(dir, "secret")
	for path, value := range map[string]string{urlFile: "https://discord.com/api/webhooks/id/private-token\n", headerFile: "Bearer private-token\n", secretFile: "0123456789abcdef\n"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	o := config.WatchOutput{Name: "discord", Type: "discord", URLFile: urlFile, HeaderFiles: map[string]string{"Authorization": headerFile}, SecretFile: secretFile}
	w := config.Default().Watch
	w.Outputs = []config.WatchOutput{o}
	if ds, err := watchDispatchers(w); err != nil || len(ds) != 1 {
		t.Fatalf("dispatchers=%v err=%v", ds, err)
	}
	for _, value := range []string{"", "https://user:private-token@discord.com", "http://example.com/private-token", "https://discord.com/private-token#fragment"} {
		if err := os.WriteFile(urlFile, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := watchOutputSender(o); err == nil || strings.Contains(err.Error(), "private-token") {
			t.Fatalf("err=%v", err)
		}
	}
	o.URLFile = ""
	o.URL = "https://example.com"
	if err := os.WriteFile(headerFile, []byte("Bearer private-token\r\nInjected: true"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := watchOutputSender(o); err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("err=%v", err)
	}
}

// A blocked receiver must not hold delivery to a healthy output.
func TestWatchOutputDeliveryIsolation(t *testing.T) {
	blocked, healthy, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(blocked)
		<-release
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer slow.Close()
	defer close(release)
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(healthy)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fast.Close()
	w := config.Default().Watch
	w.Batch = time.Hour
	w.Outputs = []config.WatchOutput{{Name: "slow", URL: slow.URL}, {Name: "fast", URL: fast.URL}}
	ds, err := watchDispatchers(w)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for _, d := range ds {
		d.Emit(alert.Alert{Cluster: "main", Kind: "Pod", Name: "web", Severity: alert.Warning})
		wg.Go(func() { d.Run(ctx) })
	}
	cancel() // flush both queues
	for _, ch := range []chan struct{}{blocked, healthy} {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("output blocked another destination")
		}
	}
	// Release the slow request before waiting for shutdown.
	// The deferred release also handles test failures; wait after explicitly releasing.
	release <- struct{}{}
	wg.Wait()
}

func TestWatchWebhookOutputWire(t *testing.T) {
	secret := []byte("0123456789abcdef")
	batch := &alert.Batch{SentAt: time.Date(2026, 11, 6, 1, 2, 3, 0, time.UTC), Alerts: []alert.Alert{{Cluster: "main", Kind: "Pod", Name: "web", Severity: alert.Warning, Summary: "memory exceeded"}}, Dropped: 2}
	want, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, want) {
			t.Errorf("body = %s, err = %v, want %s", body, err, want)
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write(want)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Opsgate-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Errorf("unexpected request: %s %v", r.Method, r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, append(secret, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	sender, err := watchOutputSender(config.WatchOutput{Name: "hook", Type: "webhook", URL: srv.URL, SecretFile: path, AllowPrivateHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := sender.Send(context.Background(), batch); err != nil || retry {
		t.Fatalf("Send() = %v, %v", retry, err)
	}
}
