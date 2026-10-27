package redact

import (
	"reflect"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"PEM", "before\n-----BEGIN RSA PRIVATE KEY-----\nabc\ndef\n-----END RSA PRIVATE KEY-----\nafter", "before\n" + Mask + "\nafter"},
		{"PKCS8", "-----BEGIN PRIVATE KEY-----\na\n-----END PRIVATE KEY-----", Mask},
		{"encrypted PEM", "-----BEGIN ENCRYPTED PRIVATE KEY-----\na\n-----END ENCRYPTED PRIVATE KEY-----", Mask},
		{"authorization", "Authorization: Digest username=alice, response=abcdef\nnext", "Authorization: " + Mask + "\nnext"},
		{"proxy", "proxy-Authorization: Basic dXNlcjpwYXNz\r\nnext", "proxy-Authorization: " + Mask + "\r\nnext"},
		{"bearer", "use Bearer abc.def-123 now", "use Bearer " + Mask + " now"},
		{"basic", "Basic dXNlcjpwYXNz==", "Basic " + Mask},
		{"JWT", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc_DEF-123", Mask},
		{"Google trailing hyphen", "AIza" + strings.Repeat("a", 34) + "-", Mask},
		{"Telegram trailing hyphen", "123456789:" + strings.Repeat("a", 34) + "-", Mask},
		{"url", "postgres://alice:p%40ss@db:5432/app", "postgres://alice:" + Mask + "@db:5432/app"},
		{"URL sensitive username", "postgres://password:secret@db/app", "postgres://password:" + Mask + "@db/app"},
		{"URL sensitive scheme", "secret://alice:pass@db/app", "secret://alice:" + Mask + "@db/app"},
		{"URL secret assignment", "password=postgres://alice:pass@db/app", "password=" + Mask},
		{"brackets", "password=abc[def]", "password=" + Mask},
		{"assignment", "DB_PASSWORD=hunter2, next=ok", "DB_PASSWORD=" + Mask + ", next=ok"},
		{"colon", "Password: hunter2", "Password: " + Mask},
		{"json", `{"client_secret": "a\"b"}`, `{"client_secret": "` + Mask + `"}`},
		{"quoted", `api_key="two words"`, `api_key="` + Mask + `"`},
		{"single quoted", `pwd='two words'`, `pwd='` + Mask + `'`},
		{"empty assignment", `password=""`, `password="` + Mask + `"`},
		{"empty URL password", "postgres://alice:@db/app", "postgres://alice:" + Mask + "@db/app"},
	}
	for _, prefix := range []string{"AKIA", "ASIA"} {
		tests = append(tests, struct{ name, in, want string }{prefix, prefix + strings.Repeat("A", 16), Mask})
	}
	for _, prefix := range []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "xoxa-", "xoxb-", "xoxp-", "xoxr-", "xoxs-", "sk_live_", "rk_live_", "sk-", "sk-ant-", "AIza", "123456789:"} {
		tests = append(tests, struct{ name, in, want string }{prefix, prefix + strings.Repeat("a", 35), Mask})
	}
	for _, key := range []string{"password", "passwd", "pwd", "secret", "client_secret", "token", "access_token", "refresh_token", "api_key", "apikey", "access_key", "secret_key", "private_key", "credential", "credentials", "auth_token", "session_id", "cookie", "set-cookie", "APP_DB_PASSWORD"} {
		tests = append(tests, struct{ name, in, want string }{key, key + "=hidden", key + "=" + Mask})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkString(t, tc.in, tc.want)
		})
	}
}

func adversarialInputs() []struct{ name, text string } {
	const n = 1 << 20
	return []struct{ name, text string }{
		{"flag spaces", "--" + strings.Repeat(" ", n-2)},
		{"long key", strings.Repeat("A", n-1) + " "},
		{"flag long key", "--" + strings.Repeat("A", n-3) + " "},
		{"alternating separators", "--" + strings.Repeat(" \t:=", (n-2)/4)},
		{"equals", strings.Repeat("=", n)},
		{"flag tabs", "--" + strings.Repeat("\t", n-2)},
	}
}

func TestStringAdversarial(t *testing.T) {
	for _, tc := range adversarialInputs() {
		t.Run(tc.name, func(t *testing.T) {
			checkString(t, tc.text, tc.text)
			if got := Fragment(tc.text, Ranges(tc.text), 0, len(tc.text)); got != tc.text {
				t.Fatal("changed text without secrets")
			}
		})
	}
}

func BenchmarkStringAdversarial(b *testing.B) {
	for _, tc := range adversarialInputs() {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.text)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				String(tc.text)
			}
		})
	}
}

func TestNormalText(t *testing.T) {
	for _, s := range []string{
		"tokens processed: 120", "password policy updated", "Using tokenizer v2",
		"Normal Pulled pod/web Successfully pulled image nginx:1.27",
		"Oct 24 12:00:00 node systemd[1]: Started nginx.service.",
		"id=550e8400-e29b-41d4-a716-446655440000 commit=0123456789abcdef0123456789abcdef01234567",
		"image=repo/app@sha256:" + strings.Repeat("a", 64),
		"configmap data: aGVsbG8gd29ybGQ=", "PASSWORD_FILE=/run/secrets/db",
		"passwords=3 token_count=120 tokenizer=v2",
		"token_count=3", "csrf token missing", "nopassword auth disabled",
		"-p hunter2", "-p=hunter2", "--port 8080", "--token-count 3", "--password-file /run/secret",
		"--password", "--password ", "PGPASSWORD", "PGPASSWORD ",
		"dbpassword hunter2", "githubtoken abc", "PgPassword hunter2", "pgpassword hunter2",
		"prefix--password hunter2", "MY-PASSWORD hunter2",
		"https://alice@example.com/path", "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----",
	} {
		t.Run(s, func(t *testing.T) {
			if got := String(s); got != s {
				t.Errorf("changed to %q", got)
			}
		})
	}
}

func TestCLIFlags(t *testing.T) {
	for _, flag := range []string{"--password", "--db-password", "--token", "--client-secret", "--api-key"} {
		for _, sep := range []string{" ", "\t", "="} {
			for _, value := range []string{"hunter2", `"two words"`, `'two words'`} {
				in := "cmd " + flag + sep + value + " --port 8080"
				masked := Mask
				if value[0] == '"' || value[0] == '\'' {
					masked = string(value[0]) + Mask + string(value[0])
				}
				want := "cmd " + flag + sep + masked + " --port 8080"
				t.Run(in, func(t *testing.T) {
					if got := String(in); got != want {
						t.Errorf("String(%q) = %q, want %q", in, got, want)
					}
				})
			}
		}
	}
}

func TestSuffixKeys(t *testing.T) {
	for _, key := range []string{"PGPASSWORD", "MYSQLPASSWORD", "dbpassword", "X_API_KEY", "AUTHTOKEN", "githubtoken", "dbpasswd", "clientsecret", "serviceapikey", "dbcredential", "dbcredentials", "notpassword"} {
		for _, sep := range []string{"=", ": ", " = ", " : "} {
			in, want := key+sep+"hunter2", key+sep+Mask
			t.Run(in, func(t *testing.T) {
				if got := String(in); got != want {
					t.Errorf("String(%q) = %q, want %q", in, got, want)
				}
			})
		}
		t.Run(key+" map", func(t *testing.T) {
			if got := Value(map[string]any{key: "hunter2"}); !reflect.DeepEqual(got, map[string]any{key: Mask}) {
				t.Errorf("Value = %#v", got)
			}
		})
		for _, sep := range []string{" ", "\t", "  "} {
			upper := strings.ToUpper(key)
			in, want := upper+sep+"hunter2 next=ok", upper+sep+Mask+" next=ok"
			t.Run(in, func(t *testing.T) {
				if got := String(in); got != want {
					t.Errorf("String(%q) = %q, want %q", in, got, want)
				}
			})
		}
	}
}

func TestValue(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, want any
	}{
		{"nested", map[string]any{"DB_PASSWORD": map[string]any{"nested": "secret"}, "rows": []any{"pwd=hidden", float64(2), nil, map[string]any{"token": 123}}}, map[string]any{"DB_PASSWORD": Mask, "rows": []any{"pwd=" + Mask, float64(2), nil, map[string]any{"token": Mask}}}},
		{"negative", map[string]any{"PASSWORD_FILE": "/run/secret", "tokenizer": "v2", "tokens": 120}, map[string]any{"PASSWORD_FILE": "/run/secret", "tokenizer": "v2", "tokens": 120}},
		{"scalar", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Value(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Value = %#v, want %#v", got, tc.want)
			}
		})
	}
	original := map[string]any{"token": "original"}
	Value(original)
	if original["token"] != "original" {
		t.Fatal("mutated input")
	}
}

func BenchmarkString64KiB(b *testing.B) {
	for _, tc := range []struct{ name, line string }{
		{"normal", "Oct 24 node kubelet[123]: Normal Pulled pod/web image nginx:1.27\n"},
		{"secrets", "DB_PASSWORD=hidden Authorization: Bearer abcdefghijklmnop\n"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			s := strings.Repeat(tc.line, (64<<10)/len(tc.line)+1)[:64<<10]
			b.SetBytes(int64(len(s)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				String(s)
			}
		})
	}
}

func checkString(t *testing.T, in, want string) {
	t.Helper()
	if got := String(in); got != want {
		t.Errorf("String(%q) = %q, want %q", in, got, want)
	}
	ranges := Ranges(in)
	for i, r := range ranges {
		if r.Start < 0 || r.End < r.Start || r.End > len(in) || i > 0 && ranges[i-1].End > r.Start {
			t.Fatalf("invalid ranges: %v", ranges)
		}
	}
	if got := render(in, ranges, 0, len(in), false); got != want {
		t.Errorf("render(%q) = %q, want %q", in, got, want)
	}
}

func TestTruncatedPEM(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"missing end", "before\n-----BEGIN RSA PRIVATE KEY-----\nabc\ndef", "before\n" + Mask},
		{"missing begin", "abc\ndef\n-----END PRIVATE KEY-----\nafter", Mask + "\nafter"},
		{"missing begin encrypted", "abc\n-----END ENCRYPTED PRIVATE KEY-----", Mask},
		{"nested begins", "-----BEGIN PRIVATE KEY-----\nabc\n-----BEGIN RSA PRIVATE KEY-----\ndef\n-----END RSA PRIVATE KEY-----", Mask},
		{"extra end", "-----BEGIN PRIVATE KEY-----\na\n-----END PRIVATE KEY-----\nb\n-----END PRIVATE KEY-----\nafter", Mask + "\nafter"},
		{"multiple blocks", "-----BEGIN PRIVATE KEY-----\na\n-----END PRIVATE KEY-----\npublic\n-----BEGIN PRIVATE KEY-----\nb", Mask + "\npublic\n" + Mask},
	} {
		t.Run(tc.name, func(t *testing.T) { checkString(t, tc.in, tc.want) })
	}
}

func TestRanges(t *testing.T) {
	for _, tc := range []struct{ in, secret string }{
		{"before ghp_abcdefghijk after", "ghp_abcdefghijk"},
		{"before eyJhbGci.eyJzdWI.abc_DEF after", "eyJhbGci.eyJzdWI.abc_DEF"},
		{"Authorization: Digest username=alice\nnext", "Digest username=alice"},
		{"Bearer abcdef", "abcdef"},
		{"password=hidden next=ok", "hidden"},
		{`password="two words"`, "two words"},
		{"password=ghp_abcdefghijk", "ghp_abcdefghijk"},
		{"postgres://alice:hidden@host/db", "hidden"},
		{"before\n-----BEGIN PRIVATE KEY-----\nabc\ndef\n-----END PRIVATE KEY-----\nafter", "-----BEGIN PRIVATE KEY-----\nabc\ndef\n-----END PRIVATE KEY-----"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			start := strings.Index(tc.in, tc.secret)
			want := []Range{{start, start + len(tc.secret)}}
			ranges := Ranges(tc.in)
			if !reflect.DeepEqual(ranges, want) {
				t.Fatalf("Ranges = %v, want %v", ranges, want)
			}
			for lo := 0; lo < len(tc.secret); lo++ {
				for hi := lo + 1; hi <= len(tc.secret); hi++ {
					visible := tc.secret[lo:hi]
					parts := strings.Split(visible, "\n")
					for i, part := range parts {
						if part != "" {
							parts[i] = Mask
						}
					}
					if got := Fragment(tc.in, ranges, start+lo, start+hi); got != strings.Join(parts, "\n") {
						t.Fatalf("fragment [%d:%d] = %q", lo, hi, got)
					}
				}
			}
		})
	}
}

func TestFragmentLineEndings(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		in := "before" + newline + "-----BEGIN PRIVATE KEY-----" + newline + "abc" + newline + newline + "def" + newline + "-----END PRIVATE KEY-----" + newline + "after"
		want := "before" + newline + Mask + newline + Mask + newline + newline + Mask + newline + Mask + newline + "after"
		if got := Fragment(in, Ranges(in), 0, len(in)); got != want {
			t.Fatalf("Fragment = %q, want %q", got, want)
		}
		if got := Fragment(in, Ranges(in), 3, 3); got != "" {
			t.Fatalf("empty Fragment = %q", got)
		}
	}
}
