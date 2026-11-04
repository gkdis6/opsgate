package config

import "testing"

func TestHTTPExampleConfig(t *testing.T) {
	c, err := Load("../../deploy/systemd/opsgate.http.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateHTTP(); err != nil {
		t.Fatal(err)
	}
}

func TestDevRunnerExampleConfig(t *testing.T) {
	if _, err := LoadDevRunner("../../deploy/dev-runner.example.yaml"); err != nil {
		t.Fatal(err)
	}
}
