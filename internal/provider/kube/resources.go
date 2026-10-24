package kube

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type resource struct {
	name, kind string
	cluster    bool
}

func resolveKind(kind string) (resource, error) {
	switch strings.ToLower(kind) {
	case "pods", "po", "pod":
		return resource{"pods", "Pod", false}, nil
	case "deployments", "deploy", "deployment":
		return resource{"deployments", "Deployment", false}, nil
	case "statefulsets", "sts", "statefulset":
		return resource{"statefulsets", "StatefulSet", false}, nil
	case "daemonsets", "ds", "daemonset":
		return resource{"daemonsets", "DaemonSet", false}, nil
	case "replicasets", "rs", "replicaset":
		return resource{"replicasets", "ReplicaSet", false}, nil
	case "jobs", "job":
		return resource{"jobs", "Job", false}, nil
	case "cronjobs", "cronjob":
		return resource{"cronjobs", "CronJob", false}, nil
	case "services", "svc", "service":
		return resource{"services", "Service", false}, nil
	case "ingresses", "ing", "ingress":
		return resource{"ingresses", "Ingress", false}, nil
	case "configmaps", "cm", "configmap":
		return resource{"configmaps", "ConfigMap", false}, nil
	case "persistentvolumeclaims", "pvc", "persistentvolumeclaim":
		return resource{"persistentvolumeclaims", "PersistentVolumeClaim", false}, nil
	case "persistentvolumes", "pv", "persistentvolume":
		return resource{"persistentvolumes", "PersistentVolume", true}, nil
	case "nodes", "no", "node":
		return resource{"nodes", "Node", true}, nil
	case "namespaces", "ns", "namespace":
		return resource{"namespaces", "Namespace", true}, nil
	}
	return resource{}, fmt.Errorf("unsupported kind %q; supported kinds: pods, deployments, statefulsets, daemonsets, replicasets, jobs, cronjobs, services, ingresses, configmaps, persistentvolumeclaims, persistentvolumes, nodes, namespaces", kind)
}

func (p *provider) listResource(ctx context.Context, r resource, ns string, opts metav1.ListOptions) (runtime.Object, error) {
	switch r.name {
	case "pods":
		return p.client.CoreV1().Pods(ns).List(ctx, opts)
	case "deployments":
		return p.client.AppsV1().Deployments(ns).List(ctx, opts)
	case "statefulsets":
		return p.client.AppsV1().StatefulSets(ns).List(ctx, opts)
	case "daemonsets":
		return p.client.AppsV1().DaemonSets(ns).List(ctx, opts)
	case "replicasets":
		return p.client.AppsV1().ReplicaSets(ns).List(ctx, opts)
	case "jobs":
		return p.client.BatchV1().Jobs(ns).List(ctx, opts)
	case "cronjobs":
		return p.client.BatchV1().CronJobs(ns).List(ctx, opts)
	case "services":
		return p.client.CoreV1().Services(ns).List(ctx, opts)
	case "ingresses":
		return p.client.NetworkingV1().Ingresses(ns).List(ctx, opts)
	case "configmaps":
		return p.client.CoreV1().ConfigMaps(ns).List(ctx, opts)
	case "persistentvolumeclaims":
		return p.client.CoreV1().PersistentVolumeClaims(ns).List(ctx, opts)
	case "persistentvolumes":
		return p.client.CoreV1().PersistentVolumes().List(ctx, opts)
	case "nodes":
		return p.client.CoreV1().Nodes().List(ctx, opts)
	case "namespaces":
		return p.client.CoreV1().Namespaces().List(ctx, opts)
	}
	return nil, fmt.Errorf("unsupported resource")
}

func (p *provider) getResource(ctx context.Context, r resource, ns, name string) (runtime.Object, error) {
	switch r.name {
	case "pods":
		return p.client.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	case "deployments":
		return p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	case "statefulsets":
		return p.client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	case "daemonsets":
		return p.client.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	case "replicasets":
		return p.client.AppsV1().ReplicaSets(ns).Get(ctx, name, metav1.GetOptions{})
	case "jobs":
		return p.client.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
	case "cronjobs":
		return p.client.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
	case "services":
		return p.client.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	case "ingresses":
		return p.client.NetworkingV1().Ingresses(ns).Get(ctx, name, metav1.GetOptions{})
	case "configmaps":
		return p.client.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	case "persistentvolumeclaims":
		return p.client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
	case "persistentvolumes":
		return p.client.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	case "nodes":
		return p.client.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	case "namespaces":
		return p.client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	}
	return nil, fmt.Errorf("unsupported resource")
}
