package host

import (
	"errors"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestNormalizeUnit(t *testing.T) {
	tests := []struct{ in, want string }{
		{"nginx", "nginx.service"}, {"a.b", "a.b.service"}, {"worker@tenant-1", "worker@tenant-1.service"}, {`dev-sda\x2d1.mount`, `dev-sda\x2d1.mount`}, {"a:b", "a:b.service"},
		{"", ""}, {"-nginx", ""}, {"../nginx", ""}, {"a b", ""}, {"a\nb", ""}, {"a\x00b", ""}, {"a;id", ""}, {"a*", ""}, {"$(id)", ""}, {"é", ""},
		{strings.Repeat("a", 248), strings.Repeat("a", 248) + ".service"}, {strings.Repeat("a", 249), ""}, {strings.Repeat("a", 257), ""},
	}
	for _, suffix := range unitSuffixes {
		tests = append(tests, struct{ in, want string }{"foo" + suffix, "foo" + suffix})
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normalizeUnit(tt.in)
			if got != tt.want || (err != nil) != (tt.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
func TestAllowlist(t *testing.T) {
	for _, tt := range []struct {
		name     string
		patterns []string
		unit     string
		allowed  bool
	}{
		{"empty", nil, "nginx", false}, {"literal", []string{"nginx"}, "nginx.service", true}, {"normalized", []string{"nginx.service"}, "nginx", true},
		{"prefix", []string{"nginx*"}, "nginx-debug.socket", true}, {"template", []string{"worker@*.service"}, "worker@a", true},
		{"middle glob", []string{"worker@*prod"}, "worker@a-prod", true}, {"question", []string{"foo?"}, "foo1.timer", false},
		{"class", []string{"foo[12].service"}, "foo2", true}, {"wrong type", []string{"nginx"}, "nginx.timer", false},
		{"no prefix match", []string{"nginx"}, "nginx-evil", false}, {"all", []string{"*"}, "a.scope", true},
		{"escape", []string{`foo\\x2d.service`}, `foo\x2d`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := provider{cfg: config.Host{Units: tt.patterns}}
			_, err := p.allowed(tt.unit)
			if (err == nil) != tt.allowed {
				t.Fatalf("allowed=%v, err=%v", tt.allowed, err)
			}
			if !tt.allowed && !errors.Is(err, tool.ErrDenied) {
				t.Fatalf("not policy denied: %v", err)
			}
		})
	}
}
func TestPatterns(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"nginx", "nginx.service"}, {"a.b", "a.b.service"}, {"worker@*prod", "worker@*prod.service"}, {"nginx*", "nginx*"}, {"foo?", "foo?"}, {"foo[12]", "foo[12]"}, {"*.timer", "*.timer"},
		{"", ""}, {"-x", ""}, {"a/b", ""}, {"a\nb", ""}, {"[", ""}, {"a[xyz", ""}, {strings.Repeat("a", 257), ""},
	} {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normalizePattern(tt.in)
			if got != tt.want || (err != nil) != (tt.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
