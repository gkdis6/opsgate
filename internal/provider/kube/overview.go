package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (p *provider) overview(ctx context.Context, _ tool.Caller, _ struct{}) (string, error) {
	var b strings.Builder
	var truncated []string
	collect := func(r resource) ([]map[string]any, error) {
		items, err := p.collect(ctx, r, "", "", 500)
		if len(items) > 500 {
			truncated = append(truncated, r.name)
			items = items[:500]
		}
		return items, err
	}
	nodes, err := collect(resource{"nodes", "Node", true})
	if err != nil {
		return "", err
	}
	fmt.Fprintln(&b, "NODES")
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tREADY\tROLES\tKUBELET\tCPU\tMEMORY\tPRESSURE")
	for _, n := range nodes {
		ready := "Unknown"
		var pressure, roles []string
		conds, _, _ := unstructured.NestedSlice(n, "status", "conditions")
		for _, v := range conds {
			c, _ := v.(map[string]any)
			typ := str(c, "type")
			if typ == "Ready" {
				ready = str(c, "status")
			}
			if strings.HasSuffix(typ, "Pressure") && str(c, "status") == "True" {
				pressure = append(pressure, typ)
			}
		}
		ls, _, _ := unstructured.NestedStringMap(n, "metadata", "labels")
		for k := range ls {
			if strings.HasPrefix(k, "node-role.kubernetes.io/") {
				roles = append(roles, strings.TrimPrefix(k, "node-role.kubernetes.io/"))
			}
		}
		sort.Strings(roles)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", str(n, "metadata", "name"), ready, display(strings.Join(roles, ",")), str(n, "status", "nodeInfo", "kubeletVersion"), str(n, "status", "allocatable", "cpu"), str(n, "status", "allocatable", "memory"), display(strings.Join(pressure, ",")))
	}
	w.Flush()
	pods, err := collect(resource{"pods", "Pod", false})
	if err != nil {
		return "", err
	}
	counts := map[string]map[string]int{}
	allowedNamespaces, err := collect(resource{"namespaces", "Namespace", true})
	if err != nil {
		return "", err
	}
	for _, ns := range allowedNamespaces {
		counts[str(ns, "metadata", "name")] = map[string]int{}
	}
	for _, pod := range pods {
		ns, phase := str(pod, "metadata", "namespace"), str(pod, "status", "phase")
		if counts[ns] == nil {
			counts[ns] = map[string]int{}
		}
		counts[ns][phase]++
	}
	fmt.Fprintln(&b, "\nPOD COUNTS")
	w = tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tPending\tRunning\tSucceeded\tFailed\tUnknown")
	var namespaces []string
	for ns := range counts {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	for _, ns := range namespaces {
		c := counts[ns]
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\n", ns, c["Pending"], c["Running"], c["Succeeded"], c["Failed"], c["Unknown"]+c[""])
	}
	w.Flush()
	fmt.Fprintln(&b, "\nPROBLEM PODS")
	w = tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tNAME\tREASON")
	for _, pod := range pods {
		if reason := podProblem(pod, time.Now()); reason != "" {
			fmt.Fprintf(w, "%s\t%s\t%s\n", str(pod, "metadata", "namespace"), str(pod, "metadata", "name"), reason)
		}
	}
	w.Flush()
	fmt.Fprintln(&b, "\nUNREADY WORKLOADS")
	w = tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tKIND\tNAME\tREADY")
	for _, kind := range []string{"deployments", "statefulsets", "daemonsets"} {
		r, _ := resolveKind(kind)
		items, err := collect(r)
		if err != nil {
			return "", err
		}
		for _, m := range items {
			want, ready := desired(m), num(m, "status", "readyReplicas")
			if kind == "daemonsets" {
				want, ready = num(m, "status", "desiredNumberScheduled"), num(m, "status", "numberReady")
			}
			if ready < want {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\n", str(m, "metadata", "namespace"), r.kind, str(m, "metadata", "name"), ready, want)
			}
		}
	}
	w.Flush()
	events, err := p.recentEvents(ctx, "", true, "1h", 10, "", "")
	if err != nil {
		return "", err
	}
	fmt.Fprintln(&b, "\nRECENT WARNINGS")
	b.WriteString(events)
	if len(truncated) > 0 {
		fmt.Fprintf(&b, "\n... overview truncated to 500 objects per kind (%s); pod counts may be incomplete\n", strings.Join(truncated, ", "))
	}
	return b.String(), nil
}

func podProblem(pod map[string]any, now time.Time) string {
	var reasons []string
	phase := str(pod, "status", "phase")
	if phase == "Running" {
		ready := false
		conds, _, _ := unstructured.NestedSlice(pod, "status", "conditions")
		for _, v := range conds {
			c, _ := v.(map[string]any)
			if str(c, "type") == "Ready" && str(c, "status") == "True" {
				ready = true
			}
		}
		if !ready {
			reasons = append(reasons, "Running but not Ready")
		}
	}
	if phase == "Pending" {
		t, err := time.Parse(time.RFC3339, str(pod, "metadata", "creationTimestamp"))
		if err == nil && now.Sub(t) > 5*time.Minute {
			reasons = append(reasons, "Pending > 5m")
		}
	}
	var totalRestarts int64
	for _, field := range []string{"containerStatuses", "initContainerStatuses", "ephemeralContainerStatuses"} {
		statuses, _, _ := unstructured.NestedSlice(pod, "status", field)
		for _, v := range statuses {
			c, _ := v.(map[string]any)
			reason := str(c, "state", "waiting", "reason")
			switch reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError":
				reasons = append(reasons, str(c, "name")+": "+reason)
			}
			totalRestarts += num(c, "restartCount")
		}
	}
	if totalRestarts >= 5 {
		reasons = append(reasons, fmt.Sprintf("%d total restarts", totalRestarts))
	}
	return strings.Join(reasons, "; ")
}
