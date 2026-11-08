package host

import (
	"os"
	"strings"
	"testing"
)

// The reference files must keep their safety properties.
func TestReferenceFiles(t *testing.T) {
	rules, err := os.ReadFile("50-opsgate.rules")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"org.freedesktop.systemd1.manage-units"`, `subject.user !== "opsgate"`, `action.lookup("verb") === "restart"`, `action.lookup("unit")`} {
		if !strings.Contains(string(rules), want) {
			t.Fatalf("polkit rule lacks %s", want)
		}
	}
	if strings.Contains(string(rules), `*"`) || strings.Contains(string(rules), `"*`) {
		t.Fatal("polkit rule must not use wildcard unit names")
	}
	for _, unit := range []string{"opsgate.service", "opsgate-watch.service"} {
		b, err := os.ReadFile(unit)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"User=opsgate", "NoNewPrivileges=yes", "CapabilityBoundingSet=\n", "ProtectSystem=strict"} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("%s lacks %q", unit, want)
			}
		}
		if strings.Contains(string(b), "ExecStart=/usr/bin/sudo") {
			t.Fatalf("%s uses sudo", unit)
		}
	}
}
