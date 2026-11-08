package alert

import (
	"testing"
	"time"
)

func TestHostNotices(t *testing.T) {
	now := time.Now()
	unit := Alert{Time: now, Cluster: HostCluster, Namespace: HostCluster, Kind: "Unit", Name: "app@1.service", Reason: "UnitFailed", Severity: Warning, OptionsURL: "https://ops.example/approve/host-options/unit/app@1.service"}
	if HostKind(unit) != "unit" || !choiceAlert(unit) {
		t.Fatal("unit alert not offered as a choice")
	}
	pod := unit
	pod.Cluster = "main"
	if HostKind(pod) != "" {
		t.Fatal("a Kubernetes cluster named differently became a host alert")
	}
	n := Notice{Pod: unit.Name, HostKind: "unit", Reason: unit.Reason, Severity: Warning, OccurredAt: now}
	if !n.Valid() {
		t.Fatal("valid host notice rejected")
	}
	for name, bad := range map[string]Notice{
		"with cluster":  {Cluster: "main", Pod: "app.service", HostKind: "unit", Reason: "UnitFailed", Severity: Warning, OccurredAt: now},
		"bad unit":      {Pod: "app", HostKind: "unit", Reason: "UnitFailed", Severity: Warning, OccurredAt: now},
		"unknown kind":  {Pod: "app.service", HostKind: "pod", Reason: "UnitFailed", Severity: Warning, OccurredAt: now},
		"with workload": {Pod: "web", HostKind: "container", WorkloadKind: "deployment", WorkloadName: "web", Reason: "x", Severity: Warning, OccurredAt: now},
		"info severity": {Pod: "web", HostKind: "container", Reason: "x", Severity: Info, OccurredAt: now},
	} {
		if bad.Valid() {
			t.Fatalf("%s accepted", name)
		}
	}
}
