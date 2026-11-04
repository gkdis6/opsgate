package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
)

func envPod() *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "public", UID: "pod-uid", Labels: map[string]string{"app": "web"}, Annotations: map[string]string{"zone": "east"}}, Spec: corev1.PodSpec{NodeName: "node1", ServiceAccountName: "app", Containers: []corev1.Container{{Name: "app", Image: "registry/app:1.4"}}}, Status: corev1.PodStatus{PodIP: "10.1.2.3", HostIP: "10.0.0.1"}}
}

func envMapRef(name, key string, optional *bool) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key, Optional: optional}}
}

func envCheckActions(t *testing.T, p *provider) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, a := range p.client.(*fake.Clientset).Actions() {
		if a.GetResource().Resource == "secrets" {
			t.Fatal("Secret API called", a)
		}
		if a.GetNamespace() != "public" {
			t.Fatal("unexpected namespace", a)
		}
		if a.GetVerb() != "get" {
			t.Fatal("unexpected action", a)
		}
		if a.GetResource().Resource == "configmaps" {
			counts[a.(kt.GetAction).GetName()]++
		}
	}
	for name, count := range counts {
		if count > 1 {
			t.Fatalf("ConfigMap %s fetched %d times", name, count)
		}
	}
	return counts
}

func envRead(t *testing.T, p *provider, in envInput) string {
	t.Helper()
	out, err := p.env(context.Background(), tool.Caller{}, in)
	if err != nil {
		t.Fatal(err)
	}
	envCheckActions(t, p)
	return out
}

func envContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestEnvEffectiveValues(t *testing.T) {
	for _, show := range []bool{false, true} {
		t.Run(fmt.Sprint(show), func(t *testing.T) {
			pod := envPod()
			pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{
				{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "db-cred"}}},
				{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "base"}}},
				{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}}},
				{Prefix: "APP_", ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}}},
			}
			pod.Spec.Containers[0].Env = []corev1.EnvVar{
				{Name: "DB_HOST", Value: "db.prod.svc"},
				{Name: "DB_PORT", ValueFrom: envMapRef("config", "DB_PORT", nil)},
				{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "db-cred"}, Key: "password"}}},
				{Name: "ALIAS", ValueFrom: envMapRef("config", "API_TOKEN", nil)},
				{Name: "LOCAL_PASSWORD", Value: "never-print-literal"},
				{Name: "DB_URL", Value: "postgres://$(DB_HOST):$(DB_PORT)/app"},
				{Name: "COMBINED", Value: "$(DB_PASSWORD)/$(ALIAS)/$(LOCAL_PASSWORD)"},
				{Name: "ESCAPE", Value: "$$(DB_HOST)/$(UNKNOWN)/$$$(DB_HOST)"},
				{Name: "COPY", Value: "$(ESCAPE)"},
				{Name: "FIRST", Value: "$(LATER)"}, {Name: "LATER", Value: "later"},
				{Name: "AGAIN", Value: "one"}, {Name: "AGAIN", Value: "$(AGAIN)-two"},
			}
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "public"}, Data: map[string]string{"DB_PORT": "5432", "LOG_LEVEL": "debug", "API_TOKEN": "never-print-map"}}
			base := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "base", Namespace: "public"}, Data: map[string]string{"DB_HOST": "old-host", "LOG_LEVEL": "info"}}
			p := testProvider(pod, cm, base)
			p.cfg.ShowConfigMapData = show
			out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
			envContains(t, out, "container: app (image: registry/app:1.4)", "DB_HOST = db.prod.svc (literal) (overrides ConfigMap base via envFrom", "(overrides ConfigMap base via envFrom", "DB_PASSWORD = <hidden> (Secret db-cred, key password)", "ALIAS = <hidden>", "COMBINED = <hidden>/<hidden>/<hidden>", "ESCAPE = $(DB_HOST)/$(UNKNOWN)/$db.prod.svc", "COPY = $(DB_HOST)/$(UNKNOWN)/$db.prod.svc", "FIRST = $(LATER)", "AGAIN = one-two", "expanded from", "(all keys of Secret db-cred, not shown", "Files baked into the container image are not visible here.")
			if show {
				envContains(t, out, "DB_PORT = 5432", "DB_URL = postgres://db.prod.svc:5432/app", `LOG_LEVEL = debug (ConfigMap config via envFrom, prefix "")`, "APP_LOG_LEVEL = debug")
			} else {
				envContains(t, out, "DB_PORT = <4 bytes hidden; show_configmap_data is off>", "DB_URL = postgres://db.prod.svc:<hidden>/app")
			}
			for _, hidden := range []string{"never-print", "old-host", " = info"} {
				if strings.Contains(out, hidden) {
					t.Fatal(out)
				}
			}
			if strings.Count(out, "\n  DB_HOST = ") != 1 || strings.Count(out, "\n  LOG_LEVEL = ") != 1 {
				t.Fatal(out)
			}
			if strings.Index(out, "\n  LOG_LEVEL") > strings.Index(out, "\n  DB_HOST") {
				t.Fatal("effective order", out)
			}
			if counts := envCheckActions(t, p); len(counts) != 2 {
				t.Fatal(counts)
			}
		})
	}
}

func TestEnvFieldsAndWorkloads(t *testing.T) {
	pod := envPod()
	for _, field := range []string{"metadata.name", "metadata.namespace", "metadata.uid", "spec.nodeName", "spec.serviceAccountName", "status.podIP", "status.hostIP", "metadata.labels['app']", "metadata.annotations['zone']", "status.unknown"} {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{Name: fmt.Sprintf("FIELD_%d", len(pod.Spec.Containers[0].Env)), ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: field}}})
	}
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{Name: "CPU_LIMIT", ValueFrom: &corev1.EnvVarSource{ResourceFieldRef: &corev1.ResourceFieldSelector{Resource: "limits.cpu"}}}, corev1.EnvVar{Name: "MEMORY", ValueFrom: &corev1.EnvVarSource{ResourceFieldRef: &corev1.ResourceFieldSelector{Resource: "requests.memory", ContainerName: "other"}}})
	template := corev1.PodTemplateSpec{Spec: pod.Spec}
	for _, tc := range []struct {
		kind string
		obj  runtime.Object
	}{
		{"po", pod},
		{"deploy", &appsv1.Deployment{ObjectMeta: pod.ObjectMeta, Spec: appsv1.DeploymentSpec{Template: template}}},
		{"sts", &appsv1.StatefulSet{ObjectMeta: pod.ObjectMeta, Spec: appsv1.StatefulSetSpec{Template: template}}},
		{"ds", &appsv1.DaemonSet{ObjectMeta: pod.ObjectMeta, Spec: appsv1.DaemonSetSpec{Template: template}}},
		{"rs", &appsv1.ReplicaSet{ObjectMeta: pod.ObjectMeta, Spec: appsv1.ReplicaSetSpec{Template: template}}},
		{"job", &batchv1.Job{ObjectMeta: pod.ObjectMeta, Spec: batchv1.JobSpec{Template: template}}},
		{"cronjob", &batchv1.CronJob{ObjectMeta: pod.ObjectMeta, Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: template}}}}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			out := envRead(t, testProvider(tc.obj), envInput{Namespace: "public", Name: "web", Kind: tc.kind})
			envContains(t, out, "<resource limits.cpu, container app> (resourceFieldRef)", "<resource requests.memory, container other>", "<field status.unknown>")
			if tc.kind == "po" {
				for i, want := range []string{"web", "public", "pod-uid", "node1", "app", "10.1.2.3", "10.0.0.1", "web", "east"} {
					envContains(t, out, fmt.Sprintf("FIELD_%d = %s (field", i, want))
				}
			} else {
				envContains(t, out, "FIELD_0 = <field metadata.name>", "FIELD_5 = <field status.podIP>", "FIELD_7 = <field metadata.labels['app']>")
			}
		})
	}
}

func TestEnvMetadataKeyRedaction(t *testing.T) {
	for _, metadata := range []string{"annotations", "labels"} {
		for _, tc := range []struct {
			key    string
			hidden bool
		}{
			{"password", true},
			{"DB_PASSWORD", true},
			{"dbpassword", true},
			{"example.com/token", true},
			{"session_id", true},
			{"COOKIE", true},
			{"app", false},
			{"password_file", false},
			{"monkey", false},
		} {
			t.Run(metadata+"/"+tc.key, func(t *testing.T) {
				pod := envPod()
				values := pod.Annotations
				if metadata == "labels" {
					values = pod.Labels
				}
				values[tc.key] = "hunter2"
				field := "metadata." + metadata + "['" + tc.key + "']"
				pod.Spec.Containers[0].Env = []corev1.EnvVar{
					{Name: "CONFIG", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: field}}},
					{Name: "COPY", Value: "prefix-$(CONFIG)-suffix"},
					{Name: "LATER", Value: "$(COPY)"},
				}
				p := testProvider(pod)
				out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
				want := "hunter2"
				if tc.hidden {
					want = redact.Mask
					if strings.Contains(out, "hunter2") {
						t.Errorf("metadata value leaked: %s", out)
					}
				}
				envContains(t, out, "CONFIG = "+want+" (field "+field+")", "COPY = prefix-"+want+"-suffix", "LATER = prefix-"+want+"-suffix")
			})
		}
	}
}

func TestEnvMissingReferences(t *testing.T) {
	for _, optional := range []bool{false, true} {
		pod := envPod()
		pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "missing"}, Optional: &optional}}}
		pod.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "MISSING_MAP", ValueFrom: envMapRef("missing", "a", &optional)}, {Name: "MISSING_ENTRY", ValueFrom: envMapRef("config", "absent", &optional)}, {Name: "BINARY", ValueFrom: envMapRef("config", "bin", nil)}}
		p := testProvider(pod, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "public"}, BinaryData: map[string][]byte{"bin": []byte("never-print-binary")}})
		out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
		envContains(t, out, "ConfigMap missing not found", "MISSING_MAP = <ConfigMap not found>", "MISSING_ENTRY = <key not found>", "BINARY = <18 bytes; binary data>")
		if optional {
			envContains(t, out, "ConfigMap missing not found (optional)", "key absent (optional)")
		}
		if strings.Contains(out, "never-print") {
			t.Fatal(out)
		}
	}
}

func TestEnvMounts(t *testing.T) {
	for _, show := range []bool{false, true} {
		t.Run(fmt.Sprint(show), func(t *testing.T) {
			pod := envPod()
			items := []corev1.KeyToPath{{Key: "app.yaml", Path: "nested/app.yaml"}, {Key: "password", Path: "credentials"}, {Key: "absent", Path: "missing"}, {Key: "binary", Path: "bin"}}
			secretItems := []corev1.KeyToPath{{Key: "password", Path: "db"}}
			pod.Spec.Volumes = []corev1.Volume{
				{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}, Items: items}}},
				{Name: "projected", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}, Items: items}}, {Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "db-cred"}, Items: secretItems}}, {DownwardAPI: &corev1.DownwardAPIProjection{}}, {ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token"}}}}}},
				{Name: "secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "db-cred", Items: secretItems}}},
				{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}},
				{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/private/host"}}},
				{Name: "down", VolumeSource: corev1.VolumeSource{DownwardAPI: &corev1.DownwardAPIVolumeSource{}}},
				{Name: "csi", VolumeSource: corev1.VolumeSource{CSI: &corev1.CSIVolumeSource{Driver: "example.csi"}}},
			}
			for _, vol := range pod.Spec.Volumes {
				pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: vol.Name, MountPath: "/" + vol.Name})
			}
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "public"}, Data: map[string]string{"app.yaml": "db: db.prod.svc", "password": "never-print-sensitive", "unmounted": "never-print-unmounted"}, BinaryData: map[string][]byte{"binary": []byte("never-print-binary")}}
			p := testProvider(pod, cm)
			p.cfg.ShowConfigMapData = show
			out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
			envContains(t, out, "/config (ConfigMap config, read-only)", "nested/app.yaml (15 bytes)", "credentials (21 bytes)\n      <hidden>", "missing (key absent not found)", "/projected (projected, read-only)", "Secret db-cred; keys not shown", "db (declared key password; content not read)", "/secret (Secret db-cred) keys not shown", "PersistentVolumeClaim data", "emptyDir", "hostPath /private/host", "downwardAPI", "csi driver example.csi")
			if show {
				envContains(t, out, "db: db.prod.svc", "binary data; size only")
			} else {
				envContains(t, out, "content hidden; show_configmap_data is off")
				if strings.Contains(out, "db: db.prod.svc") {
					t.Fatal(out)
				}
			}
			if strings.Contains(out, "never-print") || strings.Contains(out, "unmounted") {
				t.Fatal(out)
			}
		})
	}
}

func TestEnvSubPath(t *testing.T) {
	for _, projected := range []bool{false, true} {
		for _, items := range [][]corev1.KeyToPath{nil, {{Key: "a", Path: "dir/a"}, {Key: "b", Path: "dir/b"}}} {
			pod := envPod()
			source := corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}, Items: items}}
			if projected {
				source = corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}, Items: items}}}}}
			}
			pod.Spec.Volumes = []corev1.Volume{{Name: "config", VolumeSource: source}}
			sub := "a"
			if items != nil {
				sub = "dir/a"
			}
			pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "config", MountPath: "/file", SubPath: sub}}
			p := testProvider(pod, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "public"}, Data: map[string]string{"a": "selected-content", "b": "never-print-other-file"}})
			p.cfg.ShowConfigMapData = true
			out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
			envContains(t, out, sub+" (16 bytes)", "selected-content")
			if strings.Contains(out, "never-print") {
				t.Fatal(out)
			}
		}
	}
}

func TestEnvContentLimitsAndRedaction(t *testing.T) {
	pod := envPod()
	pod.Spec.Volumes = []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}}}}}
	pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "config", MountPath: "/config"}}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "public"}, Data: map[string]string{}}
	for i := range 10 {
		cm.Data[fmt.Sprintf("file-%02d", i)] = strings.Repeat("x", 9000)
	}
	p := testProvider(pod, cm)
	p.cfg.ShowConfigMapData = true
	out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
	envContains(t, out, "[truncated: 8192 of 9000 redacted bytes shown", "file-09 (9000 bytes)", "size only; 64 KiB ConfigMap content limit reached")
	if strings.Count(out, "x") != 64<<10 {
		t.Fatal("content budget", strings.Count(out, "x"))
	}
	cm.Data = map[string]string{"config": strings.Repeat("a", 8180) + " ghp_abcdefghijklmno"}
	pod.Annotations["external"] = "ghp_abcdefghijklmno"
	pod.Spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "VISIBLE", Value: "ghp_abcdefghijklmno"},
		{Name: "EXTERNAL", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.annotations['external']"}}},
		{Name: "PREFIXED", Value: "prefix$(EXTERNAL)"},
	}
	p = testProvider(pod, cm)
	p.cfg.ShowConfigMapData = true
	out = envRead(t, p, envInput{Namespace: "public", Name: "web"})
	if strings.Contains(out, "ghp_") {
		t.Fatal(out)
	}
	envContains(t, out, "[REDACTED]")
}

func TestEnvValidationAndSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     envInput
		want   string
		denied bool
		reads  int
	}{
		{"denied", envInput{Namespace: "private", Name: "web"}, "namespace is not allowed", true, 0},
		{"missing namespace", envInput{Name: "web"}, "invalid name", false, 0},
		{"bad namespace", envInput{Namespace: "a/b", Name: "web"}, "invalid name", false, 0},
		{"missing name", envInput{Namespace: "public"}, "invalid name", false, 0},
		{"bad name", envInput{Namespace: "public", Name: "a/b"}, "invalid name", false, 0},
		{"bad container", envInput{Namespace: "public", Name: "web", Container: "a/b"}, "invalid name", false, 0},
		{"unsupported kind", envInput{Namespace: "public", Name: "web", Kind: "secrets"}, "unsupported", true, 0},
		{"non workload", envInput{Namespace: "public", Name: "web", Kind: "cm"}, "unsupported", true, 0},
		{"unknown container", envInput{Namespace: "public", Name: "web", Container: "unknown"}, "available containers: app, init", false, 1},
		{"all", envInput{Namespace: "public", Name: "web"}, "", false, 1},
		{"init", envInput{Namespace: "public", Name: "web", Container: "init"}, "", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := envPod()
			pod.Spec.InitContainers = []corev1.Container{{Name: "init"}}
			p := testProvider(pod)
			out, err := p.env(context.Background(), tool.Caller{}, tc.in)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || errors.Is(err, tool.ErrDenied) != tc.denied {
					t.Fatal(out, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				envContains(t, out, "container: init")
				if strings.Contains(out, "container: app") != (tc.in.Container == "") {
					t.Fatal(out)
				}
			}
			envCheckActions(t, p)
			if len(p.client.(*fake.Clientset).Actions()) != tc.reads {
				t.Fatal(p.client.(*fake.Clientset).Actions())
			}
		})
	}
}

func TestEnvClusterRegistration(t *testing.T) {
	for _, cluster := range []string{"kind", "prod", "", "unknown"} {
		t.Run(cluster, func(t *testing.T) {
			cfg, clients := clusterFixture()
			cfg.Clusters[0].ShowConfigMapData = true
			for name, client := range clients {
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Env: []corev1.EnvVar{{Name: "HOST", ValueFrom: envMapRef("config", "HOST", nil)}}}}}}
				if err := client.(*fake.Clientset).Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, "apps"); err != nil {
					t.Fatal(err)
				}
				if err := client.(*fake.Clientset).Tracker().Add(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "apps"}, Data: map[string]string{"HOST": name + ".svc"}}); err != nil {
					t.Fatal(err)
				}
			}
			cs := clusterSession(t, cfg, clients, nil)
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "k8s_env", Arguments: map[string]any{"cluster": cluster, "namespace": "apps", "name": "web"}})
			if err != nil {
				t.Fatal(err)
			}
			out := result.Content[0].(*mcp.TextContent).Text
			if cluster == "" || cluster == "unknown" {
				if !result.IsError {
					t.Fatal(out)
				}
				envContains(t, out, "configured clusters: kind, prod")
			} else {
				if result.IsError {
					t.Fatal(out)
				}
				envContains(t, out, "cluster: "+cluster)
				if cluster == "kind" {
					envContains(t, out, "HOST = kind.svc")
				} else {
					envContains(t, out, "HOST = <8 bytes hidden; show_configmap_data is off>")
				}
			}
			for name, client := range clients {
				actions := client.(*fake.Clientset).Actions()
				if (len(actions) > 0) != (cluster == name) {
					t.Fatal(name, actions)
				}
				for _, a := range actions {
					if a.GetResource().Resource == "secrets" || a.GetNamespace() != "apps" {
						t.Fatal(a)
					}
				}
			}
		})
	}
}

func TestEnvSecretFromCollision(t *testing.T) {
	pod := envPod()
	pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{
		{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}}},
		{Prefix: "DB_", SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "db"}}},
	}
	pod.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "CONNECTION", Value: "$(DB_HOST)/$(LOG_LEVEL)"}, {Name: "DB_HOST", Value: "explicit"}, {Name: "AFTER", Value: "$(DB_HOST)"}}
	p := testProvider(pod, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "public"}, Data: map[string]string{"DB_HOST": "maybe-overridden", "LOG_LEVEL": "debug"}})
	p.cfg.ShowConfigMapData = true
	out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
	envContains(t, out, "CONNECTION = <hidden>/debug", "may be overridden by Secret db via envFrom", "DB_HOST = explicit", "AFTER = explicit")
	if strings.Contains(out, "maybe-overridden") {
		t.Fatal(out)
	}
}

func TestEnvSharedConfigMapCache(t *testing.T) {
	pod := envPod()
	pod.Spec.Volumes = []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}}}}}
	pod.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "DB_HOST", ValueFrom: envMapRef("config", "host", nil)}}
	pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "config", MountPath: "/config"}}
	pod.Spec.InitContainers = []corev1.Container{pod.Spec.Containers[0]}
	pod.Spec.InitContainers[0].Name = "init"
	p := testProvider(pod, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "public"}, Data: map[string]string{"host": "db.svc"}})
	p.cfg.ShowConfigMapData = true
	out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
	if strings.Count(out, "DB_HOST = db.svc") != 2 || envCheckActions(t, p)["config"] != 1 {
		t.Fatal(out)
	}
}

func TestEnvConfigMapReadFailure(t *testing.T) {
	pod := envPod()
	optional := true
	pod.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "HOST", ValueFrom: envMapRef("config", "host", &optional)}}
	p := testProvider(pod)
	failure := errors.New("ConfigMap read failed")
	p.client.(*fake.Clientset).PrependReactor("get", "configmaps", func(kt.Action) (bool, runtime.Object, error) { return true, nil, failure })
	out, err := p.env(context.Background(), tool.Caller{}, envInput{Namespace: "public", Name: "web"})
	if out != "" || !errors.Is(err, failure) {
		t.Fatal(out, err)
	}
	envCheckActions(t, p)
}

func TestEnvMultilineValueStaysOnOneLine(t *testing.T) {
	pod := envPod()
	pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}}}}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "public"}, Data: map[string]string{"app.yaml": "db:\n  host: db\n"}}
	p := testProvider(pod, cm)
	p.cfg.ShowConfigMapData = true
	out := envRead(t, p, envInput{Namespace: "public", Name: "web"})
	envContains(t, out, `app.yaml = "db:\n  host: db\n" (ConfigMap config via envFrom`)
}
