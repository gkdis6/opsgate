package config

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"time"
)

// Validate checks settings that do not depend on the transport.
func (c Config) Validate() error {
	switch c.Mode {
	case "", ModeKubernetes:
	default:
		return fmt.Errorf("mode must be %s, got %q", ModeKubernetes, c.Mode)
	}
	if err := checkToolGlobs("server.tools.allow", c.Server.Tools.Allow); err != nil {
		return err
	}
	if err := checkToolGlobs("server.tools.deny", c.Server.Tools.Deny); err != nil {
		return err
	}
	if err := c.validateRegistrationPolicy(); err != nil {
		return err
	}
	if c.Approval.Window != 0 && (c.Approval.Window < time.Minute || c.Approval.Window > 12*time.Hour) {
		return fmt.Errorf("approval.window must be zero or between 1m and 12h")
	}
	for _, pattern := range c.Approval.WindowTools {
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("approval.window_tools: invalid glob: %w", err)
		}
	}
	if c.Server.MaxOutputBytes <= 0 {
		return fmt.Errorf("server.max_output_bytes must be positive")
	}
	if c.Server.ToolTimeout <= 0 {
		return fmt.Errorf("server.tool_timeout must be positive")
	}
	if err := checkScopes("stdio.scopes", c.Stdio.Scopes); err != nil {
		return err
	}
	for _, u := range append(append([]User{}, c.Auth.Builtin.Users...), c.Auth.External.Users...) {
		if err := checkToolGlobs("auth users "+u.Subject+" tools", u.Tools); err != nil {
			return err
		}
		if err := checkScopes("auth users "+u.Subject, u.Scopes); err != nil {
			return err
		}
	}
	for _, t := range c.Auth.Static {
		if err := checkToolGlobs("auth.static "+t.Name+" tools", t.Tools); err != nil {
			return err
		}
		if err := checkScopes("auth.static "+t.Name, t.Scopes); err != nil {
			return err
		}
	}
	if err := c.Providers.Kubernetes.validate(); err != nil {
		return err
	}
	return c.Watch.validate(c.Providers.Kubernetes)
}

// ValidateHTTP checks the settings required to serve over HTTP. It refuses
// configurations that would expose tools without authentication.
func (c Config) ValidateHTTP() error {
	if err := c.Validate(); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(c.HTTP.Listen)
	if err != nil {
		return fmt.Errorf("http.listen: %w", err)
	}
	u, err := url.Parse(c.HTTP.PublicURL)
	if err != nil || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("http.public_url must be an origin like https://mcp.example.com")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return fmt.Errorf("http.public_url must use https (http is allowed only for localhost)")
	}
	switch c.Auth.Mode {
	case AuthBuiltin, AuthExternal, AuthStatic:
	case "":
		return fmt.Errorf("auth.mode is required for the HTTP transport (listen %s)", host)
	default:
		return fmt.Errorf("auth.mode: unknown mode %q", c.Auth.Mode)
	}
	if c.Approval.RequireWeb && !c.Approval.Web {
		return fmt.Errorf("approval.require_web requires approval.web")
	}
	if c.Approval.Web && c.Auth.Mode != AuthBuiltin {
		return fmt.Errorf("approval.web requires auth.mode builtin")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func checkScopes(where string, scopes []string) error {
	for _, s := range scopes {
		if !KnownScope(s) {
			return fmt.Errorf("%s: unknown scope %q", where, s)
		}
	}
	return nil
}

func checkToolGlobs(where string, patterns []string) error {
	for _, pattern := range patterns {
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("%s: invalid glob %q: %w", where, pattern, err)
		}
	}
	return nil
}
