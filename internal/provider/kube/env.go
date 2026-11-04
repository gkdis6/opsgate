package kube

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type envInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace string `json:"namespace" jsonschema:"Required allowed namespace"`
	Kind      string `json:"kind,omitempty" jsonschema:"Pod or workload kind or alias; default pod; supports deployment, statefulset, daemonset, replicaset, job and cronjob"`
	Name      string `json:"name" jsonschema:"Required object name"`
	Container string `json:"container,omitempty" jsonschema:"Container name; default all regular and init containers"`
}

type envConfigMap struct {
	object *corev1.ConfigMap
	err    error
}

type envView struct {
	p            *provider
	ctx          context.Context
	namespace    string
	maps         map[string]envConfigMap
	out          strings.Builder
	contentBytes int
}

// line redacts cluster strings separately so formatting cannot obscure a secret.
func (v *envView) line(format string, args ...any) {
	for i, arg := range args {
		if s, ok := arg.(string); ok {
			args[i] = redact.String(s)
		}
	}
	fmt.Fprintf(&v.out, format+"\n", args...)
}

func (v *envView) configMap(name string) (*corev1.ConfigMap, error) {
	if cached, ok := v.maps[name]; ok {
		return cached.object, cached.err
	}
	if err := v.p.checkNamespace(v.namespace); err != nil {
		return nil, err
	}
	if err := validName(name, false); err != nil {
		return nil, err
	}
	cm, err := v.p.client.CoreV1().ConfigMaps(v.namespace).Get(v.ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		cm, err = nil, nil
	}
	v.maps[name] = envConfigMap{cm, err}
	return cm, err
}

func (p *provider) env(ctx context.Context, _ tool.Caller, in envInput) (string, error) {
	if err := p.checkNamespace(in.Namespace); err != nil {
		return "", err
	}
	if err := validName(in.Name, false); err != nil {
		return "", err
	}
	if in.Container != "" {
		if err := validName(in.Container, false); err != nil {
			return "", err
		}
	}
	if in.Kind == "" {
		in.Kind = "pod"
	}
	r, err := resolveKind(in.Kind)
	if err != nil || !slices.Contains([]string{"pods", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs"}, r.name) {
		return "", tool.Denied("unsupported container configuration kind")
	}
	obj, err := p.getResource(ctx, r, in.Namespace, in.Name)
	if err != nil {
		return "", err
	}
	m, err := objectMap(obj)
	if err != nil {
		return "", err
	}
	path := []string{"spec"}
	if r.name == "cronjobs" {
		path = []string{"spec", "jobTemplate", "spec", "template", "spec"}
	} else if r.name != "pods" {
		path = []string{"spec", "template", "spec"}
	}
	specMap, _, err := unstructured.NestedMap(m, path...)
	if err != nil {
		return "", err
	}
	var spec corev1.PodSpec
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(specMap, &spec); err != nil {
		return "", err
	}
	var pod *corev1.Pod
	if r.name == "pods" {
		pod = obj.(*corev1.Pod)
	}
	containers := append(slices.Clone(spec.Containers), spec.InitContainers...)
	var names []string
	var selected []corev1.Container
	for _, c := range containers {
		names = append(names, c.Name)
		if in.Container == "" || c.Name == in.Container {
			selected = append(selected, c)
		}
	}
	if in.Container != "" && len(selected) == 0 {
		return "", fmt.Errorf("unknown container; available containers: %s", redact.String(strings.Join(names, ", ")))
	}
	v := &envView{p: p, ctx: ctx, namespace: in.Namespace, maps: map[string]envConfigMap{}}
	for _, c := range selected {
		v.line("container: %s (image: %s)", c.Name, c.Image)
		v.line("ENV")
		if err := v.environment(c, pod); err != nil {
			return "", err
		}
		v.line("MOUNTS")
		if err := v.mounts(c, spec.Volumes); err != nil {
			return "", err
		}
		v.line("")
	}
	v.line("Files baked into the container image are not visible here.")
	return v.out.String(), nil
}

type envValue struct {
	name, value, expansion, source, template, overrides string
}

func optionalEnv(optional *bool) string {
	if optional != nil && *optional {
		return " (optional)"
	}
	return ""
}

func (v *envView) mapValue(cm *corev1.ConfigMap, key string) (string, string) {
	if sensitiveEnv.MatchString(key) {
		return "<hidden>", "<hidden>"
	}
	if data, ok := cm.Data[key]; ok {
		if !v.p.cfg.ShowConfigMapData {
			return fmt.Sprintf("<%d bytes hidden; show_configmap_data is off>", len(data)), "<hidden>"
		}
		value := redact.String(data)
		return value, value
	}
	if data, ok := cm.BinaryData[key]; ok {
		return fmt.Sprintf("<%d bytes; binary data>", len(data)), "<hidden>"
	}
	return "<key not found>", "<hidden>"
}

func (v *envView) environment(c corev1.Container, pod *corev1.Pod) error {
	values := map[string]envValue{}
	var order []string
	set := func(e envValue) {
		if sensitiveEnv.MatchString(e.name) {
			e.value, e.expansion, e.template = "<hidden>", "<hidden>", ""
		}
		if old, ok := values[e.name]; ok {
			e.overrides = old.source
			order = slices.DeleteFunc(order, func(name string) bool { return name == e.name })
		}
		order = append(order, e.name)
		values[e.name] = e
	}
	for _, from := range c.EnvFrom {
		if ref := from.SecretRef; ref != nil {
			v.line("  (all keys of Secret %s, not shown) (prefix %q)%s", ref.Name, from.Prefix, optionalEnv(ref.Optional))
			// Secret keys are unknown, so any earlier matching prefix may collide.
			for name, old := range values {
				if strings.HasPrefix(name, from.Prefix) {
					old.value, old.expansion = "<hidden>", "<hidden>"
					old.source += fmt.Sprintf("; may be overridden by Secret %s via envFrom", ref.Name)
					values[name] = old
				}
			}
		}
		if ref := from.ConfigMapRef; ref != nil {
			cm, err := v.configMap(ref.Name)
			if err != nil {
				return err
			}
			if cm == nil {
				v.line("  ConfigMap %s not found%s", ref.Name, optionalEnv(ref.Optional))
				continue
			}
			keys := make([]string, 0, len(cm.Data))
			for key := range cm.Data {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				value, expansion := v.mapValue(cm, key)
				set(envValue{name: from.Prefix + key, value: value, expansion: expansion, source: fmt.Sprintf("ConfigMap %s via envFrom, prefix %q", ref.Name, from.Prefix)})
			}
		}
	}
	for _, e := range c.Env {
		value := envValue{name: e.Name, source: "literal"}
		if e.ValueFrom == nil {
			value.value = expandEnv(e.Value, values)
			value.expansion = value.value
			if strings.Contains(e.Value, "$(") || strings.Contains(e.Value, "$$") {
				value.template = e.Value
			}
		} else {
			switch ref := e.ValueFrom; {
			case ref.SecretKeyRef != nil:
				value.value, value.expansion = "<hidden>", "<hidden>"
				value.source = fmt.Sprintf("Secret %s, key %s%s", ref.SecretKeyRef.Name, ref.SecretKeyRef.Key, optionalEnv(ref.SecretKeyRef.Optional))
			case ref.ConfigMapKeyRef != nil:
				key := ref.ConfigMapKeyRef
				value.source = fmt.Sprintf("ConfigMap %s, key %s%s", key.Name, key.Key, optionalEnv(key.Optional))
				cm, err := v.configMap(key.Name)
				if err != nil {
					return err
				}
				if cm == nil {
					value.value, value.expansion = "<ConfigMap not found>", "<hidden>"
				} else {
					_, text := cm.Data[key.Key]
					_, binary := cm.BinaryData[key.Key]
					if !text && !binary {
						value.source += "; key not found"
					}
					value.value, value.expansion = v.mapValue(cm, key.Key)
				}
			case ref.FieldRef != nil:
				value.source = "field " + ref.FieldRef.FieldPath
				value.value = redact.String(envField(pod, ref.FieldRef.FieldPath))
				value.expansion = value.value
			case ref.ResourceFieldRef != nil:
				target := ref.ResourceFieldRef.ContainerName
				if target == "" {
					target = c.Name
				}
				value.source = "resourceFieldRef"
				value.value = fmt.Sprintf("<resource %s, container %s>", ref.ResourceFieldRef.Resource, target)
				value.expansion = value.value
			}
		}
		set(value)
	}
	for _, name := range order {
		e := values[name]
		source := e.source
		if e.template != "" {
			source += fmt.Sprintf(", expanded from %q", redact.String(e.template))
		}
		if e.overrides != "" {
			source += ") (overrides " + e.overrides
		}
		value := e.value
		if strings.ContainsAny(value, "\r\n") {
			value = strconv.Quote(value)
		}
		v.line("  %s = %s (%s)", e.name, value, source)
	}
	return nil
}

// Expansion is single-pass: inserted values and escaped dollars are never rescanned.
func expandEnv(raw string, values map[string]envValue) string {
	var out strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] == '$' && i+1 < len(raw) {
			if raw[i+1] == '$' {
				out.WriteByte('$')
				i += 2
				continue
			}
			if raw[i+1] == '(' {
				if end := strings.IndexByte(raw[i+2:], ')'); end >= 0 {
					name := raw[i+2 : i+2+end]
					if value, ok := values[name]; ok {
						out.WriteString(value.expansion)
					} else {
						out.WriteString(raw[i : i+3+end])
					}
					i += 3 + end
					continue
				}
			}
		}
		out.WriteByte(raw[i])
		i++
	}
	return redact.String(out.String())
}

func envField(pod *corev1.Pod, field string) string {
	if pod != nil {
		switch field {
		case "metadata.name":
			return pod.Name
		case "metadata.namespace":
			return pod.Namespace
		case "metadata.uid":
			return string(pod.UID)
		case "spec.nodeName":
			return pod.Spec.NodeName
		case "spec.serviceAccountName":
			return pod.Spec.ServiceAccountName
		case "status.podIP":
			return pod.Status.PodIP
		case "status.hostIP":
			return pod.Status.HostIP
		}
		for prefix, values := range map[string]map[string]string{"metadata.labels['": pod.Labels, "metadata.annotations['": pod.Annotations} {
			if strings.HasPrefix(field, prefix) && strings.HasSuffix(field, "']") {
				key := strings.TrimSuffix(strings.TrimPrefix(field, prefix), "']")
				if redact.SensitiveKey(key) {
					return redact.Mask
				}
				return values[key]
			}
		}
	}
	return "<field " + field + ">"
}
