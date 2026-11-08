package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestInboxConfigDefaultsValidationAndRoundTrip(t *testing.T) {
	if Default().Watch.Inbox.URL != "" || Default().Approval.InboxNotify.SecretFile != "" {
		t.Fatal("notice channel must be opt-in")
	}
	base := `mode: kubernetes
auth:
  builtin:
    users:
      - subject: passkey:owner
        scopes: [ops:read, ops:write]
approval:
  inbox_notify:
    secret_file: /etc/opsgate/inbox/key
    subjects: [passkey:owner]
watch:
  inbox:
    url: https://ops.example
    secret_file: /etc/opsgate/inbox/key
`
	c, err := Parse([]byte(base), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateWatch(); err != nil {
		t.Fatal("inbox-only watch", err)
	}
	b, err := c.Effective()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(b, "effective")
	if err != nil || !reflect.DeepEqual(c.Watch.Inbox, again.Watch.Inbox) || !reflect.DeepEqual(c.Approval.InboxNotify, again.Approval.InboxNotify) {
		t.Fatal("notice config round trip", err)
	}
	for _, bad := range []string{
		strings.Replace(base, "subjects: [passkey:owner]", "subjects: [passkey:unknown]", 1),
		strings.Replace(base, "subjects: [passkey:owner]", "subjects: [passkey:owner, passkey:owner]", 1),
		strings.Replace(base, "subjects: [passkey:owner]", "subjects: []", 1),
		strings.Replace(base, "approval:", "approval:\n  web: false", 1),
		strings.Replace(base, "auth:", "auth:\n  mode: external", 1),
		strings.Replace(base, "https://ops.example", "http://8.8.8.8", 1),
		strings.Replace(base, "https://ops.example", "http://ops.svc", 1),
		strings.Replace(base, "https://ops.example", "https://ops.example/path", 1),
		strings.Replace(base, "https://ops.example", "https://ops.example//", 1),
		strings.Replace(base, "https://ops.example", "https://ops.example/%2f", 1),
		strings.Replace(base, "https://ops.example", "https://ops.example?", 1),
		strings.Replace(base, "https://ops.example", "https://ops.example#fragment", 1),
		strings.Replace(base, "https://ops.example", "https://user:secret@ops.example", 1),
		strings.ReplaceAll(base, "secret_file: /etc/opsgate/inbox/key", "secret_file: ''"),
	} {
		if _, err := Parse([]byte(bad), "test"); err == nil {
			t.Fatal("invalid config accepted", bad)
		}
	}
	private := strings.Replace(base, "https://ops.example", "http://opsgate.opsgate.svc:8787\n    allow_private_http: true", 1)
	if _, err := Parse([]byte(private), "test"); err != nil {
		t.Fatal("private inbox origin", err)
	}
}
