package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	for _, tt := range []struct{ module, want string }{
		{"", "dev"}, {"(devel)", "dev"}, {"v1.2.3", "v1.2.3"}, {"v0.0.0-20260101000000-abcdef123456", "v0.0.0-20260101000000-abcdef123456"},
	} {
		info := &debug.BuildInfo{Main: debug.Module{Version: tt.module}}
		if got := moduleVersion(info); got != tt.want {
			t.Errorf("moduleVersion(%q)=%q, want %q", tt.module, got, tt.want)
		}
	}
}

func TestVersion(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v2.3.4"
	if got := Version(); got != version {
		t.Fatalf("Version()=%q, want %q", got, version)
	}
	version = ""
	want := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		want = moduleVersion(info)
	}
	if got := Version(); got != want {
		t.Fatalf("Version()=%q, want %q", got, want)
	}
}
