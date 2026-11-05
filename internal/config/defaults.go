package config

import (
	"fmt"
	"time"
)

// Default returns the configuration used when no file is given.
func Default() Config {
	return Config{
		Server: Server{
			Name:           "opsgate",
			MaxOutputBytes: 64 << 10,
			ToolTimeout:    30 * time.Second,
		},
		Stdio: Stdio{
			User:   "local",
			Scopes: []string{ScopeRead},
		},
		HTTP: HTTP{
			Listen: "127.0.0.1:8787",
		},
		Auth: Auth{
			Builtin: BuiltinAuth{
				AccessTokenTTL:  time.Hour,
				RefreshTokenTTL: 30 * 24 * time.Hour,
				MaxClients:      100,
			},
			External: ExternalAuth{
				UserClaim: "sub",
			},
		},
		Approval: Approval{
			TTL: 10 * time.Minute,
		},
		Providers: Providers{
			Kubernetes: Kubernetes{
				KubernetesCluster: KubernetesCluster{
					MaxLogLines: 500,
					MaxReplicas: 10,
				},
			},
			Note: Note{
				MaxNoteBytes: 1 << 20,
			},
		},
		Watch: Watch{
			Email:     WatchEmail{Port: 587, Batch: 5 * time.Minute},
			Batch:     30 * time.Second,
			Cooldown:  15 * time.Minute,
			MaxAlerts: 50,
		},
	}
}

// DefaultFor returns the defaults of a deployment mode. A mode serves HTTP
// with builtin passkey sign-in and keeps its state in /var/lib/opsgate.
func DefaultFor(mode string) (Config, error) {
	c := Default()
	c.Mode = mode
	switch mode {
	case "":
		return c, nil
	case ModeKubernetes:
		// Audit events go to stderr for the cluster's log collection.
		c.HTTP.Listen = "0.0.0.0:8787"
		c.Providers.Kubernetes.Enabled = true
	default:
		return c, fmt.Errorf("mode must be %s, got %q", ModeKubernetes, mode)
	}
	c.Auth.Mode = AuthBuiltin
	c.Auth.Builtin.StateFile = "/var/lib/opsgate/oauth.json"
	c.Auth.Builtin.Upstream.Provider = "passkey"
	return c, nil
}
