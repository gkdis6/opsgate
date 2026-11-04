package config

import (
	"strings"
	"testing"
	"time"
)

func validEmail() WatchEmail {
	return WatchEmail{Host: "smtp.example.com", Port: 587, From: "alerts@example.com", To: []string{"me@example.com"}, Batch: 5 * time.Minute}
}

func TestWatchEmailValidation(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		change      func(*WatchEmail)
	}{
		{"valid", "", func(*WatchEmail) {}},
		{"port 465", "", func(e *WatchEmail) { e.Port = 465 }},
		{"port min", "", func(e *WatchEmail) { e.Port = 1 }},
		{"port max", "", func(e *WatchEmail) { e.Port = 65535 }},
		{"port zero", "port", func(e *WatchEmail) { e.Port = 0 }},
		{"port negative", "port", func(e *WatchEmail) { e.Port = -1 }},
		{"port high", "port", func(e *WatchEmail) { e.Port = 65536 }},
		{"host space", "host", func(e *WatchEmail) { e.Host = "smtp. example.com" }},
		{"host unicode space", "host", func(e *WatchEmail) { e.Host = "smtp.\u2003example.com" }},
		{"host scheme", "host", func(e *WatchEmail) { e.Host = "smtp://smtp.example.com" }},
		{"host port", "host", func(e *WatchEmail) { e.Host = "smtp.example.com:587" }},
		{"host slash", "host", func(e *WatchEmail) { e.Host = "smtp.example.com/" }},
		{"host newline", "host", func(e *WatchEmail) { e.Host = "smtp.example.com\n" }},
		{"username alone", "username", func(e *WatchEmail) { e.Username = "alerts@example.com" }},
		{"password alone", "password_file", func(e *WatchEmail) { e.PasswordFile = "/etc/opsgate/password" }},
		{"credentials", "", func(e *WatchEmail) { e.Username = "alerts@example.com"; e.PasswordFile = "/etc/opsgate/password" }},
		{"from empty", "from", func(e *WatchEmail) { e.From = "" }},
		{"from invalid", "from", func(e *WatchEmail) { e.From = "invalid" }},
		{"from display", "from", func(e *WatchEmail) { e.From = "Alerts <alerts@example.com>" }},
		{"from brackets", "from", func(e *WatchEmail) { e.From = "<alerts@example.com>" }},
		{"from CR", "from", func(e *WatchEmail) { e.From = "alerts@example.com\r" }},
		{"from LF", "from", func(e *WatchEmail) { e.From = "alerts@example.com\nBcc: x@example.com" }},
		{"to empty", "to", func(e *WatchEmail) { e.To = nil }},
		{"to invalid", "to", func(e *WatchEmail) { e.To = []string{"invalid"} }},
		{"to display", "to", func(e *WatchEmail) { e.To = []string{"Me <me@example.com>"} }},
		{"to CRLF", "to", func(e *WatchEmail) { e.To = []string{"me@example.com\r\n"} }},
		{"to 20", "", func(e *WatchEmail) { e.To = strings.Fields(strings.Repeat("me@example.com ", 20)) }},
		{"to 21", "to", func(e *WatchEmail) { e.To = strings.Fields(strings.Repeat("me@example.com ", 21)) }},
		{"batch min", "", func(e *WatchEmail) { e.Batch = time.Minute }},
		{"batch max", "", func(e *WatchEmail) { e.Batch = time.Hour }},
		{"batch low", "batch", func(e *WatchEmail) { e.Batch = time.Minute - time.Nanosecond }},
		{"batch high", "batch", func(e *WatchEmail) { e.Batch = time.Hour + time.Nanosecond }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Watch.Email = validEmail()
			tc.change(&cfg.Watch.Email)
			err := cfg.Validate()
			if tc.field == "" && err != nil || tc.field != "" && (err == nil || !strings.Contains(err.Error(), "watch.email."+tc.field)) {
				t.Fatalf("err=%v, want field=%q", err, tc.field)
			}
		})
	}
}

func TestWatchEmailLoad(t *testing.T) {
	const prefix = "providers:\n  kubernetes:\n    enabled: true\nwatch:\n"
	const email = "  email:\n    host: smtp.example.com\n    from: alerts@example.com\n    to: [me@example.com]\n"
	for _, channels := range []string{"  webhook_url: https://hooks.example.com/watch\n", email, "  webhook_url: https://hooks.example.com/watch\n" + email} {
		cfg, err := Parse([]byte(prefix+channels), "test")
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.ValidateWatch(); err != nil {
			t.Fatal(err)
		}
		if cfg.Watch.Email.Port != 587 || cfg.Watch.Email.Batch != 5*time.Minute {
			t.Fatalf("defaults=%+v", cfg.Watch.Email)
		}
		effective, err := cfg.Effective()
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(effective, "effective")
		if err != nil {
			t.Fatal(err)
		}
		if err := again.ValidateWatch(); err != nil {
			t.Fatal(err)
		}
		if again.Watch.Email.Host != cfg.Watch.Email.Host {
			t.Fatalf("email lost on round trip")
		}

	}
	cfg, err := Parse([]byte(prefix), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateWatch(); err == nil || !strings.Contains(err.Error(), "watch.webhook_url") || !strings.Contains(err.Error(), "watch.email.host") {
		t.Fatal(err)
	}
	for _, field := range []string{"port: 587", "username: alerts@example.com", "password_file: /etc/opsgate/password", "from: alerts@example.com", "to: [me@example.com]", "batch: 5m", "problems_only: false"} {
		name, _, _ := strings.Cut(field, ":")
		_, err := Parse([]byte(prefix+"  email:\n    "+field+"\n"), "test")
		if err == nil || !strings.Contains(err.Error(), "watch.email."+name) {
			t.Fatalf("%s: %v", field, err)
		}
	}
}

func TestWatchEmailDisabledValue(t *testing.T) {
	cfg := Default()
	cfg.Watch.Email = WatchEmail{}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
