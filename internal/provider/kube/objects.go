package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/gkdis6/opsgate/internal/tool"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

type listInput struct {
	Kind          string `json:"kind"`
	Namespace     string `json:"namespace,omitempty"`
	LabelSelector string `json:"label_selector,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}
type getInput struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

func clamp(n, def, max int) int {
	if n == 0 {
		n = def
	}
	if n < 1 {
		n = 1
	}
	if n > max {
		n = max
	}
	return n
}

func objectMap(obj runtime.Object) (map[string]any, error) {
	return runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
}

func (p *provider) collect(ctx context.Context, r resource, ns, selector string) ([]map[string]any, error) {
	if ns != "" {
		if err := p.checkNamespace(ns); err != nil {
			return nil, err
		}
		if r.cluster {
			return nil, fmt.Errorf("%s is cluster scoped; omit namespace", r.name)
		}
	}
	scopes := []string{""}
	var err error
	if !r.cluster {
		scopes, err = p.namespaces(ctx, ns)
		if err != nil {
			return nil, err
		}
	}
	var result []map[string]any
	for _, scope := range scopes {
		opts := metav1.ListOptions{LabelSelector: selector, Limit: 500}
		for {
			obj, err := p.listResource(ctx, r, scope, opts)
			if err != nil {
				return nil, err
			}
			items, err := meta.ExtractList(obj)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				a, err := meta.Accessor(item)
				if err != nil {
					return nil, err
				}
				if !r.cluster && !p.policy.allowed(a.GetNamespace()) {
					continue
				}
				if r.name == "namespaces" && !p.policy.allowed(a.GetName()) {
					continue
				}
				m, err := objectMap(item)
				if err != nil {
					return nil, err
				}
				result = append(result, m)
			}
			a, err := meta.ListAccessor(obj)
			if err != nil {
				return nil, err
			}
			opts.Continue = a.GetContinue()
			if opts.Continue == "" {
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return str(result[i], "metadata", "namespace")+"/"+str(result[i], "metadata", "name") < str(result[j], "metadata", "namespace")+"/"+str(result[j], "metadata", "name")
	})
	return result, nil
}

func (p *provider) list(ctx context.Context, _ tool.Caller, in listInput) (string, error) {
	r, err := resolveKind(in.Kind)
	if err != nil {
		return "", err
	}
	if _, err = labels.Parse(in.LabelSelector); err != nil {
		return "", fmt.Errorf("invalid label_selector: %w", err)
	}
	items, err := p.collect(ctx, r, in.Namespace, in.LabelSelector)
	if err != nil {
		return "", err
	}
	limit := clamp(in.Limit, 100, 500)
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAMESPACE\tNAME\t%s\tAGE\n", strings.Join(tableHeaders(r.name), "\t"))
	for _, m := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", display(str(m, "metadata", "namespace")), display(str(m, "metadata", "name")), strings.Join(tableCells(m, r.name), "\t"), age(str(m, "metadata", "creationTimestamp")))
	}
	w.Flush()
	if truncated {
		fmt.Fprintf(&b, "... truncated to %d objects\n", limit)
	}
	return b.String(), nil
}

func (p *provider) get(ctx context.Context, _ tool.Caller, in getInput) (string, error) {
	r, err := resolveKind(in.Kind)
	if err != nil {
		return "", err
	}
	if err = validName(in.Name, false); err != nil {
		return "", err
	}
	if in.Namespace != "" {
		if err = p.checkNamespace(in.Namespace); err != nil {
			return "", err
		}
	}
	if !r.cluster && in.Namespace == "" {
		return "", fmt.Errorf("namespace is required")
	}
	if r.cluster && in.Namespace != "" {
		return "", fmt.Errorf("%s is cluster scoped; omit namespace", r.name)
	}
	if r.name == "namespaces" {
		if err = p.checkNamespace(in.Name); err != nil {
			return "", err
		}
	}
	obj, err := p.getResource(ctx, r, in.Namespace, in.Name)
	if err != nil {
		return "", err
	}
	m, err := objectMap(obj)
	if err != nil {
		return "", err
	}
	m["apiVersion"], m["kind"] = apiVersion(r.name), r.kind
	sanitize(m, r.name, p.cfg.ShowConfigMapData)
	data, err := yaml.Marshal(m)
	if err != nil {
		return "", err
	}
	events, err := p.recentEvents(ctx, in.Namespace, false, "1h", 50, in.Name, r.kind)
	if err != nil {
		return "", err
	}
	return string(data) + "\nEVENTS\n" + events, nil
}

func apiVersion(kind string) string {
	switch kind {
	case "deployments", "statefulsets", "daemonsets", "replicasets":
		return "apps/v1"
	case "jobs", "cronjobs":
		return "batch/v1"
	case "ingresses":
		return "networking.k8s.io/v1"
	}
	return "v1"
}

var sensitiveEnv = regexp.MustCompile(`(?i)(pass|secret|token|key|credential|auth|dsn|private|cert|database_url)`)

func sanitize(m map[string]any, kind string, showData bool) {
	// Walk object metadata, including template metadata, to remove embedded snapshots.
	var clean func(any)
	clean = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if md, ok := x["metadata"].(map[string]any); ok {
				delete(md, "managedFields")
				if a, ok := md["annotations"].(map[string]any); ok {
					delete(a, "kubectl.kubernetes.io/last-applied-configuration")
				}
			}
			for _, v := range x {
				clean(v)
			}
		case []any:
			for _, v := range x {
				clean(v)
			}
		}
	}
	clean(m)
	if kind == "configmaps" && !showData {
		for _, key := range []string{"data", "binaryData"} {
			if values, ok := m[key].(map[string]any); ok {
				for k, v := range values {
					n := len(fmt.Sprint(v))
					if key == "binaryData" {
						var b []byte
						raw, _ := json.Marshal(v)
						if json.Unmarshal(raw, &b) == nil {
							n = len(b)
						}
					}
					values[k] = fmt.Sprintf("<%d bytes hidden>", n)
				}
			}
		}
	}
	var spec map[string]any
	switch kind {
	case "pods":
		spec, _ = m["spec"].(map[string]any)
	case "deployments", "statefulsets", "daemonsets", "replicasets", "jobs":
		spec, _, _ = unstructured.NestedMap(m, "spec", "template", "spec")
	case "cronjobs":
		spec, _, _ = unstructured.NestedMap(m, "spec", "jobTemplate", "spec", "template", "spec")
	}
	if spec == nil {
		return
	}
	for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
		containers, _ := spec[field].([]any)
		for _, c := range containers {
			cm, _ := c.(map[string]any)
			env, _ := cm["env"].([]any)
			for _, e := range env {
				em, _ := e.(map[string]any)
				name, _ := em["name"].(string)
				if _, ok := em["value"]; ok && sensitiveEnv.MatchString(name) {
					em["value"] = "[REDACTED]"
				}
			}
		}
	}
	switch kind {
	case "pods":
		m["spec"] = spec
	case "cronjobs":
		_ = unstructured.SetNestedMap(m, spec, "spec", "jobTemplate", "spec", "template", "spec")
	default:
		_ = unstructured.SetNestedMap(m, spec, "spec", "template", "spec")
	}
}

func str(m map[string]any, fields ...string) string {
	v, _, _ := unstructured.NestedFieldNoCopy(m, fields...)
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func num(m map[string]any, fields ...string) int64 {
	v, _, _ := unstructured.NestedFieldNoCopy(m, fields...)
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func desired(m map[string]any) int64 {
	if _, ok, _ := unstructured.NestedFieldNoCopy(m, "spec", "replicas"); !ok {
		return 1
	}
	return num(m, "spec", "replicas")
}
