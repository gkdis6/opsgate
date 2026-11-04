package kubernetes_test

import (
	"errors"
	"io"
	"net/url"
	"os"
	"testing"

	"github.com/gkdis6/opsgate/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func TestDeploymentProbeHosts(t *testing.T) {
	f, err := os.Open("opsgate-deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoder := k8syaml.NewYAMLOrJSONDecoder(f, 4096)
	var publicURL string
	var containers []corev1.Container
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
			var cfg struct {
				HTTP struct {
					PublicURL string `json:"public_url"`
				} `json:"http"`
			}
			if err := yaml.Unmarshal([]byte(doc.Data["opsgate.yaml"]), &cfg); err != nil {
				t.Fatal(err)
			}
			publicURL = cfg.HTTP.PublicURL
			loaded, err := config.Parse([]byte(doc.Data["opsgate.yaml"]), "ConfigMap opsgate")
			if err != nil {
				t.Fatal(err)
			}
			if err := loaded.ValidateHTTP(); err != nil {
				t.Fatal(err)
			}
		case "Deployment":
			containers = doc.Spec.Template.Spec.Containers
		}
	}
	public, err := url.Parse(publicURL)
	if err != nil || public.Host == "" {
		t.Fatalf("invalid public_url %q: %v", publicURL, err)
	}
	if len(containers) != 1 || containers[0].Name != "opsgate" {
		t.Fatalf("expected one opsgate container, got %v", containers)
	}
	for name, probe := range map[string]*corev1.Probe{
		"startup":   containers[0].StartupProbe,
		"readiness": containers[0].ReadinessProbe,
		"liveness":  containers[0].LivenessProbe,
	} {
		t.Run(name, func(t *testing.T) {
			if probe == nil || probe.HTTPGet == nil || probe.HTTPGet.Path != "/healthz" || probe.HTTPGet.Port.String() != "http" {
				t.Fatal("expected HTTP probe on /healthz using port http")
			}
			for _, header := range probe.HTTPGet.HTTPHeaders {
				if header.Name == "Host" && header.Value == public.Host {
					return
				}
			}
			t.Fatalf("probe missing Host header matching public_url host %q", public.Host)
		})
	}
}
