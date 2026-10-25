package kube

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// tableObject retains only fields read by the list tables and overview. In
// particular, do not retain annotations, environment, or ConfigMap payloads.
func tableObject(m map[string]any, kind string) map[string]any {
	paths := []string{"metadata.name", "metadata.namespace", "metadata.creationTimestamp", "status.phase"}
	switch kind {
	case "pods":
		paths = append(paths, "spec.containers.name", "status.conditions.type", "status.conditions.status")
		for _, field := range []string{"containerStatuses", "initContainerStatuses", "ephemeralContainerStatuses"} {
			for _, suffix := range []string{"name", "ready", "restartCount", "state.waiting.reason"} {
				paths = append(paths, "status."+field+"."+suffix)
			}
		}
	case "deployments", "statefulsets", "replicasets":
		paths = append(paths, "spec.replicas", "status.readyReplicas")
	case "daemonsets":
		paths = append(paths, "status.numberReady", "status.desiredNumberScheduled")
	case "nodes":
		paths = append(paths, "status.conditions.type", "status.conditions.status", "status.nodeInfo.kubeletVersion", "status.allocatable.cpu", "status.allocatable.memory")
	case "services":
		paths = append(paths, "spec.type", "spec.clusterIP")
	case "jobs":
		paths = append(paths, "spec.completions", "status.succeeded", "status.active", "status.failed")
	case "cronjobs":
		paths = append(paths, "spec.schedule", "spec.suspend", "status.active.name")
	case "ingresses":
		paths = append(paths, "spec.ingressClassName", "spec.rules.host")
	case "persistentvolumeclaims":
		paths = append(paths, "spec.volumeName", "status.capacity.storage")
	case "persistentvolumes":
		paths = append(paths, "spec.capacity.storage", "spec.storageClassName")
	}
	out := tableProjection(m, paths).(map[string]any)
	if kind == "configmaps" {
		for _, field := range []string{"data", "binaryData"} {
			values, _ := m[field].(map[string]any)
			keys := make(map[string]any, len(values))
			for key := range values {
				keys[key] = nil
			}
			out[field] = keys
		}
	}
	if kind == "nodes" {
		labels, _, _ := unstructured.NestedMap(m, "metadata", "labels")
		roles := map[string]any{}
		for key := range labels {
			if strings.HasPrefix(key, "node-role.kubernetes.io/") {
				roles[key] = ""
			}
		}
		out["metadata"].(map[string]any)["labels"] = roles
	}
	return out
}

func tableProjection(v any, paths []string) any {
	switch x := v.(type) {
	case map[string]any:
		children := map[string][]string{}
		for _, path := range paths {
			key, rest, _ := strings.Cut(path, ".")
			children[key] = append(children[key], rest)
		}
		out := map[string]any{}
		for key, paths := range children {
			if value, ok := x[key]; ok {
				out[key] = tableProjection(value, paths)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = tableProjection(value, paths)
		}
		return out
	default:
		return v
	}
}

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
