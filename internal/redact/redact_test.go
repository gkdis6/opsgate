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
		{"passkey invitation", "passkey setup for passkey:alice (valid 24h): https://ops.example/passkey/register?invite=R4nd0m_unprefixed-setup", "passkey setup for passkey:alice (valid 24h): https://ops.example/passkey/register?invite=" + Mask},
		{"invitation query", "https://ops.example/passkey/register?next=ok&invite=R4nd0m_unprefixed-setup&lang=en#register", "https://ops.example/passkey/register?next=ok&invite=" + Mask + "&lang=en#register"},
		{"invitation URL fragment", "https://ops.example/passkey/register?invite=R4nd0m_unprefixed-setup#register", "https://ops.example/passkey/register?invite=" + Mask + "#register"},
		{"invitation assignment", "invite=R4nd0m_unprefixed-setup next=ok", "invite=" + Mask + " next=ok"},
		{"invitation env", "INVITE R4nd0m_unprefixed-setup next=ok", "INVITE " + Mask + " next=ok"},
		{"invitation quoted", `invite="two words"`, `invite="` + Mask + `"`},
		{"empty invitation query", "https://ops.example/passkey/register?invite=&lang=en", "https://ops.example/passkey/register?invite=" + Mask + "&lang=en"},
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
	for _, key := range []string{"password", "passwd", "pwd", "secret", "client_secret", "token", "access_token", "refresh_token", "api_key", "apikey", "access_key", "secret_key", "private_key", "credential", "credentials", "auth_token", "session_id", "cookie", "set-cookie", "invite", "APP_DB_PASSWORD"} {
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
		{"invitation", map[string]any{"invite": "R4nd0m_unprefixed-setup"}, map[string]any{"invite": Mask}},
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

func TestQuotedAssignments(t *testing.T) {
	for _, tc := range []struct{ name, in, secret string }{
		{"double", `password="hunter2`, "hunter2"},
		{"single", `password='hunter2`, "hunter2"},
		{"JSON", `{"password":"hunter2`, "hunter2"},
		{"line end", "x password=\"hunter2\nnext", "hunter2"},
		{"CRLF", "x password='hunter2\r\nnext", "hunter2"},
		{"escaped quote", `password="hun\"ter2`, `hun\"ter2`},
		{"trailing escape", `password='hunter2\`, `hunter2\`},
		{"incomplete multiline", "password=\"hunter2\nstill secret", "hunter2"},
		{"complete double", `password="hun\"ter2" next=ok`, `hun\"ter2`},
		{"complete single", `password='hun\'ter2' next=ok`, `hun\'ter2`},
		{"escaped newline", "password=\"hunter2\\\nnext", "hunter2\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := strings.Index(tc.in, tc.secret)
			end := start + len(tc.secret)
			checkString(t, tc.in, tc.in[:start]+Mask+tc.in[end:])
			ranges := Ranges(tc.in)
			if want := []Range{{start, end}}; !reflect.DeepEqual(ranges, want) {
				t.Errorf("Ranges = %v, want %v", ranges, want)
			}
			want := tc.in[:start] + Mask + tc.in[end:]
			if got := Fragment(tc.in, ranges, 0, len(tc.in)); got != want {
				t.Errorf("Fragment = %q, want %q", got, want)
			}
			if got := Fragment(tc.in, ranges, start, start+1); got != Mask {
				t.Errorf("partial Fragment = %q", got)
			}
		})
	}
	// A later quote cannot be told apart from other text, so only the first
	// line of a multi-line value is masked.
	for in, want := range map[string]string{
		"password=\"hunter2\nmore\r\nsecret\" next=ok": "password=\"" + Mask + "\nmore\r\nsecret\" next=ok",
		"password='hunter2\nmore' next=ok":             "password='" + Mask + "\nmore' next=ok",
		"password=\"hunter2\nother=\"visible\"":        "password=\"" + Mask + "\nother=\"visible\"",
	} {
		checkString(t, in, want)
		if got := Fragment(in, Ranges(in), 0, len(in)); got != want {
			t.Errorf("multiline Fragment = %q, want %q", got, want)
		}
	}
}

func TestOpsgateTokens(t *testing.T) {
	for _, prefix := range []string{"og_at_", "og_rt_", "og_st_"} {
		t.Run(prefix, func(t *testing.T) {
			token := prefix + strings.Repeat("a", 41) + "_-"
			in := "session " + token + " continues"
			checkString(t, in, "session "+Mask+" continues")
			ranges := Ranges(in)
			if want := []Range{{8, 8 + len(token)}}; !reflect.DeepEqual(ranges, want) {
				t.Errorf("Ranges = %v, want %v", ranges, want)
			}
			if got := Fragment(in, ranges, 10, 20); got != Mask {
				t.Errorf("partial Fragment = %q", got)
			}
			for _, ordinary := range []string{strings.TrimSuffix(prefix, "_"), prefix, prefix + "short"} {
				checkString(t, "ordinary "+ordinary+" word", "ordinary "+ordinary+" word")
			}
		})
	}
}

func TestRanges(t *testing.T) {
	for _, tc := range []struct{ in, secret string }{
		{"before ghp_abcdefghijk after", "ghp_abcdefghijk"},
		{"before eyJhbGci.eyJzdWI.abc_DEF after", "eyJhbGci.eyJzdWI.abc_DEF"},
		{"Authorization: Digest username=alice\nnext", "Digest username=alice"},
		{"Bearer abcdef", "abcdef"},
		{"password=hidden next=ok", "hidden"},
		{"https://ops.example/passkey/register?invite=R4nd0m_unprefixed-setup&lang=en#register", "R4nd0m_unprefixed-setup"},
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

func TestInvitationLogFiltering(t *testing.T) {
	const token = "R4nd0m_unprefixed-setup"
	in := "ready\npasskey setup for passkey:alice (valid 24h): https://ops.example/passkey/register?invite=" + token + "\nserving\n"
	text := Fragment(in, Ranges(in), 0, len(in))
	for _, needle := range []string{"passkey setup", token, token[:8], token[8:]} {
		var matched []string
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(strings.ToLower(line), strings.ToLower(needle)) {
				matched = append(matched, line)
			}
		}
		if needle == "passkey setup" {
			want := "passkey setup for passkey:alice (valid 24h): https://ops.example/passkey/register?invite=" + Mask
			if len(matched) != 1 || matched[0] != want {
				t.Fatalf("filtered log = %q, want %q", matched, want)
			}
		} else if len(matched) != 0 {
			t.Errorf("secret filter %q matched %q", needle, matched)
		}
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
