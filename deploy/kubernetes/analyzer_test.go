package kubernetes_test

import (
	"errors"
	"io"
	"os"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestAnalyzerServiceAccountTokenOnlyInReader(t *testing.T) {
	f, err := os.Open("opsgate-analyzer.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoder := k8syaml.NewYAMLOrJSONDecoder(f, 4096)
	for {
		var doc struct {
			Kind string                `json:"kind"`
			Spec appsv1.DeploymentSpec `json:"spec"`
		}
		if err := decoder.Decode(&doc); errors.Is(err, io.EOF) {
			t.Fatal("no analyzer Deployment")
		} else if err != nil {
			t.Fatal(err)
		}
		if doc.Kind != "Deployment" {
			continue
		}
		spec := doc.Spec.Template.Spec
		if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
			t.Fatal("pod automatically mounts API credentials")
		}
		var credentials *corev1.Volume
		for i := range spec.Volumes {
			v := &spec.Volumes[i]
			if v.Projected != nil {
				for _, source := range v.Projected.Sources {
					if source.ServiceAccountToken != nil {
						credentials = v
					}
				}
			}
		}
		if credentials == nil {
			t.Fatal("reader has no projected API credentials")
		}
		token, ca, namespace := false, false, false
		for _, source := range credentials.Projected.Sources {
			if v := source.ServiceAccountToken; v != nil {
				token = v.Path == "token" && v.ExpirationSeconds != nil && *v.ExpirationSeconds <= 3600
			}
			if v := source.ConfigMap; v != nil {
				for _, item := range v.Items {
					ca = ca || v.Name == "kube-root-ca.crt" && item.Key == "ca.crt" && item.Path == "ca.crt"
				}
			}
			if v := source.DownwardAPI; v != nil {
				for _, item := range v.Items {
					namespace = namespace || item.Path == "namespace" && item.FieldRef != nil && item.FieldRef.FieldPath == "metadata.namespace"
				}
			}
		}
		if !token || !ca || !namespace {
			t.Fatal("incomplete in-cluster credential projection")
		}
		reader := false
		for _, c := range spec.Containers {
			for _, mount := range c.VolumeMounts {
				if mount.Name == credentials.Name {
					if c.Name != "reader" || !mount.ReadOnly || mount.MountPath != "/var/run/secrets/kubernetes.io/serviceaccount" {
						t.Fatalf("credentials exposed to %s", c.Name)
					}
					reader = true
				}
			}
		}
		if !reader {
			t.Fatal("reader credential mount missing")
		}
		return
	}
}
