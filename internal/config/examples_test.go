package config

import (
	"os"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDevRunnerExampleConfig(t *testing.T) {
	if _, err := LoadDevRunner("../../deploy/dev-runner.example.yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestKubernetesExampleApproval(t *testing.T) {
	f, err := os.Open("../../deploy/kubernetes/opsgate-deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	for {
		var doc struct {
			Kind string            `yaml:"kind"`
			Data map[string]string `yaml:"data"`
		}
		if err := dec.Decode(&doc); err != nil {
			t.Fatal(err)
		}
		if doc.Kind != "ConfigMap" {
			continue
		}
		c, err := Parse([]byte(doc.Data["opsgate.yaml"]), "ConfigMap")
		if err != nil {
			t.Fatal(err)
		}
		if !c.Approval.RequireWeb {
			t.Fatal("Kubernetes example must inherit web-only approval")
		}
		if err := c.ValidateHTTP(); err != nil {
			t.Fatal(err)
		}
		return
	}
}
