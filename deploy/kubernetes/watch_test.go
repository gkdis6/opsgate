package kubernetes_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gkdis6/opsgate/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestWatchManifest(t *testing.T) {
	f, err := os.Open("opsgate-watch.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoder := k8syaml.NewYAMLOrJSONDecoder(f, 4096)
	var cfgText string
	var spec appsv1.DeploymentSpec
	for {
		var doc struct {
			Kind string                `json:"kind"`
			Data map[string]string     `json:"data"`
			Spec appsv1.DeploymentSpec `json:"spec"`
		}
		if err := decoder.Decode(&doc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		switch doc.Kind {
		case "ConfigMap":
			cfgText = doc.Data["opsgate.yaml"]
		case "Deployment":
			spec = doc.Spec
		}
	}
	path := filepath.Join(t.TempDir(), "opsgate.yaml")
	if err := os.WriteFile(path, []byte(cfgText), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateWatch(); err != nil {
		t.Fatal(err)
	}
	if spec.Replicas == nil || *spec.Replicas != 1 || spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatal("watch must run as a single replica with the Recreate strategy")
	}
	pod := spec.Template.Spec
	if len(pod.Containers) != 1 || len(pod.Containers[0].Args) == 0 || pod.Containers[0].Args[0] != "watch" {
		t.Fatalf("unexpected containers: %v", pod.Containers)
	}
	if pod.SecurityContext == nil || pod.SecurityContext.FSGroup == nil || *pod.SecurityContext.FSGroup != *pod.SecurityContext.RunAsUser {
		t.Fatal("fsGroup must let the container user read the HMAC secret")
	}
}
