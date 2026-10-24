package kube

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func display(s string) string {
	if s == "" {
		return "-"
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}

func tableHeaders(kind string) []string {
	switch kind {
	case "pods":
		return []string{"READY", "STATUS", "RESTARTS"}
	case "deployments", "statefulsets", "daemonsets", "replicasets":
		return []string{"READY"}
	case "nodes":
		return []string{"READY", "KUBELET"}
	case "services":
		return []string{"TYPE", "CLUSTER-IP"}
	case "jobs":
		return []string{"COMPLETIONS", "ACTIVE", "FAILED"}
	case "cronjobs":
		return []string{"SCHEDULE", "SUSPEND", "ACTIVE"}
	case "configmaps":
		return []string{"KEYS"}
	case "ingresses":
		return []string{"CLASS", "HOSTS"}
	case "persistentvolumeclaims":
		return []string{"STATUS", "VOLUME", "CAPACITY"}
	case "persistentvolumes":
		return []string{"STATUS", "CAPACITY", "STORAGECLASS"}
	default:
		return []string{"STATUS"}
	}
}

func tableCells(m map[string]any, kind string) []string {
	switch kind {
	case "pods":
		containers, _, _ := unstructured.NestedSlice(m, "spec", "containers")
		statuses, _, _ := unstructured.NestedSlice(m, "status", "containerStatuses")
		ready, restarts := 0, int64(0)
		for _, v := range statuses {
			c, _ := v.(map[string]any)
			if c["ready"] == true {
				ready++
			}
			restarts += num(c, "restartCount")
		}
		return []string{fmt.Sprintf("%d/%d", ready, len(containers)), display(str(m, "status", "phase")), fmt.Sprint(restarts)}
	case "deployments", "statefulsets", "replicasets":
		return []string{fmt.Sprintf("%d/%d", num(m, "status", "readyReplicas"), desired(m))}
	case "daemonsets":
		return []string{fmt.Sprintf("%d/%d", num(m, "status", "numberReady"), num(m, "status", "desiredNumberScheduled"))}
	case "nodes":
		ready := "Unknown"
		conditions, _, _ := unstructured.NestedSlice(m, "status", "conditions")
		for _, v := range conditions {
			c, _ := v.(map[string]any)
			if str(c, "type") == "Ready" {
				ready = str(c, "status")
			}
		}
		return []string{ready, display(str(m, "status", "nodeInfo", "kubeletVersion"))}
	case "services":
		return []string{display(str(m, "spec", "type")), display(str(m, "spec", "clusterIP"))}
	case "jobs":
		want := num(m, "spec", "completions")
		if want == 0 {
			want = 1
		}
		return []string{fmt.Sprintf("%d/%d", num(m, "status", "succeeded"), want), fmt.Sprint(num(m, "status", "active")), fmt.Sprint(num(m, "status", "failed"))}
	case "cronjobs":
		active, _, _ := unstructured.NestedSlice(m, "status", "active")
		return []string{display(str(m, "spec", "schedule")), display(str(m, "spec", "suspend")), fmt.Sprint(len(active))}
	case "configmaps":
		data, _, _ := unstructured.NestedMap(m, "data")
		binary, _, _ := unstructured.NestedMap(m, "binaryData")
		return []string{fmt.Sprint(len(data) + len(binary))}
	case "ingresses":
		var hosts []string
		rules, _, _ := unstructured.NestedSlice(m, "spec", "rules")
		for _, v := range rules {
			r, _ := v.(map[string]any)
			hosts = append(hosts, display(str(r, "host")))
		}
		return []string{display(str(m, "spec", "ingressClassName")), display(strings.Join(hosts, ","))}
	case "persistentvolumeclaims":
		return []string{display(str(m, "status", "phase")), display(str(m, "spec", "volumeName")), display(str(m, "status", "capacity", "storage"))}
	case "persistentvolumes":
		return []string{display(str(m, "status", "phase")), display(str(m, "spec", "capacity", "storage")), display(str(m, "spec", "storageClassName"))}
	default:
		return []string{display(str(m, "status", "phase"))}
	}
}
