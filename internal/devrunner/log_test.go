package devrunner

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/redact"
)

func TestLogRedactsFragments(t *testing.T) {
	for _, tc := range []struct{ name, source, secret, want string }{
		{"token", "ghp_abcdefghijk next", "abcdefghijk", redact.Mask + " next"},
		{"UTF-8 prefix", "한글 ghp_abcdefghijk next", "abcdefghijk", redact.Mask + " next"},
		{"JWT", "eyJhbGci.eyJzdWI.abc_DEF next", "eyJzdWI.abc_DEF", redact.Mask + " next"},
		{"authorization", "Authorization: Digest username=alice\nnext", "username=alice", redact.Mask + "\nnext"},
		{"assignment", "password=hunter2 next=ok", "hunter2", redact.Mask + " next=ok"},
		{"quoted assignment", `password="two words" next=ok`, "two words", redact.Mask + `" next=ok`},
		{"PEM", "before\n-----BEGIN PRIVATE KEY-----\nabc\ndef\n-----END PRIVATE KEY-----\nafter", "abc\ndef", strings.Repeat(redact.Mask+"\n", 3) + "after"},
		{"PEM without begin", "abc\ndef\n-----END PRIVATE KEY-----\nafter", "abc\ndef", strings.Repeat(redact.Mask+"\n", 3) + "after"},
		{"PEM without end", "before\n-----BEGIN PRIVATE KEY-----\nabc\ndef", "abc\ndef", redact.Mask + "\n" + redact.Mask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ownershipRunner(t)
			id := strings.Repeat("a", 20)
			r.tasks = []*Task{{ID: id, owner: "owner", State: "succeeded"}}
			if err := r.root.MkdirAll("tasks/"+id, 0700); err != nil {
				t.Fatal(err)
			}
			if err := r.root.WriteFile("tasks/"+id+"/log.txt", []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			offset := strings.Index(tc.source, tc.secret)
			for _, limit := range []int{1, len(tc.secret), len(tc.source)} {
				out := ownershipRequest(r, "GET", fmt.Sprintf("/v1/tasks/%s/log?offset=%d&limit=%d", id, offset, limit), "owner")
				var page Log
				if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &page) != nil {
					t.Fatalf("%d %s", out.Code, out.Body)
				}
				end := min(offset+limit, len(tc.source))
				want := redact.Mask
				if limit == len(tc.source) {
					want = tc.want
				} else if limit == len(tc.secret) && strings.Contains(tc.secret, "\n") {
					want += "\n" + redact.Mask
				}
				if page.Text != want || !strings.HasPrefix(page.Text, redact.Mask) || page.NextOffset != int64(end) || page.Truncated != (end < len(tc.source)) {
					t.Fatalf("limit %d: %+v, want text %q, next %d", limit, page, want, end)
				}
			}
			for _, offset := range []int{len(tc.source), len(tc.source) + 5} {
				out := ownershipRequest(r, "GET", fmt.Sprintf("/v1/tasks/%s/log?offset=%d", id, offset), "owner")
				var page Log
				if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &page) != nil || page.Text != "" || page.NextOffset != int64(offset) || page.Truncated {
					t.Fatalf("EOF: %d %s", out.Code, out.Body)
				}
			}
		})
	}
}

func TestLogRedactsAcrossWindow(t *testing.T) {
	r := ownershipRunner(t)
	id := strings.Repeat("a", 20)
	r.tasks = []*Task{{ID: id, owner: "owner", State: "succeeded"}}
	if err := r.root.MkdirAll("tasks/"+id, 0700); err != nil {
		t.Fatal(err)
	}
	// A key whose BEGIN marker lies before the scanned window is still masked.
	key := "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("QUJD\n", (logRedactMargin+4096)/5) + "-----END PRIVATE KEY-----\nafter"
	if err := r.root.WriteFile("tasks/"+id+"/log.txt", []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	offset := logRedactMargin + 2048
	out := ownershipRequest(r, "GET", fmt.Sprintf("/v1/tasks/%s/log?offset=%d&limit=64", id, offset), "owner")
	var page Log
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &page) != nil {
		t.Fatalf("%d %s", out.Code, out.Body)
	}
	if strings.Contains(page.Text, "QUJD") || !strings.Contains(page.Text, redact.Mask) {
		t.Fatalf("key material visible: %q", page.Text)
	}
}
