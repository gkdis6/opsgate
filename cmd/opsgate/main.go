// Command opsgate is an MCP server for safe, audited server operations.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/approval"
	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/authn"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/httpserver"
	"github.com/gkdis6/opsgate/internal/oauth"
	"github.com/gkdis6/opsgate/internal/provider/host"
	"github.com/gkdis6/opsgate/internal/provider/kube"
	"github.com/gkdis6/opsgate/internal/server"
	"github.com/gkdis6/opsgate/internal/tool"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("opsgate: ")
	log.SetOutput(os.Stderr) // stdout belongs to the stdio transport
	os.Exit(cli(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(cfgPath, transport string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if transport == "" {
		transport = "stdio"
		if cfg.Mode != "" {
			transport = "http"
		}
	}
	al, err := audit.Open(cfg.Server.AuditLog)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	defer al.Close()

	env := &tool.Env{
		Audit:          al,
		MaxOutputBytes: cfg.Server.MaxOutputBytes,
		Timeout:        cfg.Server.ToolTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch transport {
	case "stdio":
		if cfg.Approval.RequireWebDefaulted {
			cfg.Approval.RequireWeb = false
		}
		env.Fallback = &tool.Caller{User: cfg.Stdio.User, Transport: "stdio", Scopes: cfg.Stdio.Scopes}
		env.Confirm = approval.New(cfg.Approval, "", nil, al) // elicitation only
		s, err := server.New(cfg, env)
		if err != nil {
			return err
		}
		return s.Run(ctx, &mcp.StdioTransport{})
	case "http":
		return runHTTP(ctx, cfg, env)
	default:
		return fmt.Errorf("unknown transport %q", transport)
	}
}

func runHTTP(ctx context.Context, cfg config.Config, env *tool.Env) error {
	if err := cfg.ValidateHTTP(); err != nil {
		return err
	}
	o := httpserver.Options{}
	var browser approval.BrowserAuth
	switch cfg.Auth.Mode {
	case config.AuthBuiltin:
		as, err := oauth.New(cfg.Auth.Builtin, cfg.Issuer(), cfg.ResourceURL())
		if err != nil {
			return fmt.Errorf("oauth: %w", err)
		}
		defer as.Close()
		o.Verifier = as.VerifyWithTools
		o.AuthorizationServers = []string{cfg.Issuer()}
		o.Routes = append(o.Routes, as.Routes)
		browser = as
	case config.AuthExternal:
		v, err := authn.External(ctx, cfg.Auth.External, cfg.ResourceURL())
		if err != nil {
			return fmt.Errorf("external auth: %w", err)
		}
		o.Verifier = v
		o.AuthorizationServers = []string{cfg.Auth.External.Issuer}
	case config.AuthStatic:
		v, err := authn.Static(cfg.Auth.Static)
		if err != nil {
			return fmt.Errorf("static auth: %w", err)
		}
		o.Verifier = v
	}

	if !cfg.Approval.Web {
		browser = nil
	}
	am := approval.New(cfg.Approval, cfg.HTTP.PublicURL, browser, env.Audit)
	if err := am.EnableInboxNotify(cfg.Approval.InboxNotify); err != nil {
		return fmt.Errorf("approval inbox notify: %w", err)
	}
	if err := am.EnablePush(cfg.Approval.Push); err != nil {
		return fmt.Errorf("approval push: %w", err)
	}
	defer am.Close()
	env.Confirm = am
	am.SetDirect(env.Direct)
	if browser != nil {
		env.UndoURL = undoURL(cfg.HTTP.PublicURL, env)
	}
	o.Routes = append(o.Routes, am.Routes)

	s, err := server.New(cfg, env)
	if err != nil {
		return err
	}
	am.SetOptions(env.Remediation)
	am.SetHostOptions(env.HostOptions)
	o.Server = s
	if !cfg.Approval.RequireWeb {
		log.Printf("warning: a compromised client can approve its own writes through elicitation; approval.require_web: true avoids this where web approval is available")
	}
	log.Printf("serving %s on %s", cfg.ResourceURL(), cfg.HTTP.Listen)
	return httpserver.Serve(ctx, cfg, httpserver.Handler(cfg, o))
}

func printToken(out io.Writer) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := "og_st_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(tok))
	_, err := fmt.Fprintf(out, "token:  %s\nsha256: %s\n", tok, hex.EncodeToString(sum[:]))
	return err
}

// runWatch watches Kubernetes and delivers alerts by webhook or email. It
// serves no MCP clients, so it can run under its own account and kubeconfig.
func (c *application) runWatch(args []string) error {
	fs := c.flags("watch")
	path := fs.String("config", "", "path to opsgate.yaml (required)")
	if err := c.parse(fs, args); err != nil {
		return err
	}
	if *path == "" {
		return usageError("missing required flag -config")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := cfg.ValidateWatch(); err != nil {
		return err
	}
	w := cfg.Watch
	dispatchers, err := watchDispatchers(w)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Keep delivery alive until watcher callbacks have stopped.
	deliveryCtx, cancelDeliveries := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelDeliveries()
	var groups []alert.Group
	for _, g := range w.Groups {
		groups = append(groups, alert.Group{Name: g.Name, Namespaces: g.Namespaces, Outputs: g.Outputs, ProblemsOnly: g.ProblemsOnly, Batch: g.Batch})
	}
	router := alert.NewRouter(groups, dispatchers)
	var deliveries sync.WaitGroup
	deliveries.Go(func() { router.Run(deliveryCtx) })
	emit := func(a alert.Alert) {
		base := w.ApprovalURL
		if base == "" {
			base = w.Inbox.URL
		}
		addApprovalLink(&a, base)
		router.Emit(a)
	}
	err = runWatchers(ctx, cfg, emit)
	stop()
	cancelDeliveries()
	deliveries.Wait()
	return err
}

// runWatchers runs the Kubernetes and host watchers that are enabled until ctx
// ends or one of them fails, which stops the others.
func runWatchers(ctx context.Context, cfg config.Config, emit func(alert.Alert)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	start := func(run func(context.Context) error) {
		wg.Go(func() {
			if err := run(ctx); err != nil {
				once.Do(func() { firstErr = err })
				cancel()
			}
		})
	}
	if cfg.Providers.Kubernetes.Enabled {
		start(func(ctx context.Context) error {
			return kube.Watch(ctx, cfg.WatchClusters(), cfg.Watch.ProblemsOnly, emit)
		})
	}
	if cfg.Providers.Host.Enabled {
		start(func(ctx context.Context) error { return host.Watch(ctx, cfg.Providers.Host, emit) })
	}
	wg.Wait()
	return firstErr
}

// addApprovalLink adds live choices and retains the legacy restart link.
func addApprovalLink(a *alert.Alert, base string) {
	if kind := alert.HostKind(*a); kind != "" {
		if base != "" && a.Severity == alert.Warning {
			if link, err := action.HostOptionsURL("", kind, a.Name); err == nil {
				a.OptionsURL = strings.TrimRight(base, "/") + link
			}
		}
		return
	}
	if base != "" && a.Severity == alert.Warning && a.Kind == "Pod" && a.Namespace != "" && a.Workload != nil {
		a.OptionsURL, _ = action.OptionsURL("", a.Cluster, a.Namespace, a.Name, a.Workload.Kind+"/"+a.Workload.Name)
		if a.OptionsURL != "" {
			a.OptionsURL = strings.TrimRight(base, "/") + a.OptionsURL
		}
	}
	if base != "" && a.Restart != nil {
		a.Restart.URL = approval.RestartURL(base, a.Cluster, a.Restart.Kind, a.Restart.Namespace, a.Restart.Name)
	}
}

// watchDispatchers loads credentials before any watchers start.
func watchDispatchers(w config.Watch) ([]*alert.Dispatcher, error) {
	var result []*alert.Dispatcher
	if n := w.Inbox; n.URL != "" {
		secret, err := alert.ReadSecret(n.SecretFile)
		if err != nil {
			return nil, fmt.Errorf("watch.inbox: %w", err)
		}
		sender := alert.NewNoticeSender(alert.Options{URL: strings.TrimRight(n.URL, "/") + "/approvals/notify", Secret: secret, PrivateOnly: strings.HasPrefix(strings.ToLower(n.URL), "http:")})
		result = append(result, alert.New(alert.Options{Sender: sender, Batch: time.Second, ChoicesOnly: true, ProblemsOnly: true, Cooldown: w.Cooldown, MaxAlerts: min(w.MaxAlerts, 50)}))
	}
	for _, o := range w.Outputs {
		if o.Language == "" {
			o.Language = w.Language
		}
		sender, err := watchOutputSender(o)
		if err != nil {
			return nil, fmt.Errorf("watch.outputs %s: %w", o.Name, err)
		}
		result = append(result, alert.New(alert.Options{Sender: sender, Batch: w.Batch, ProblemsOnly: o.ProblemsOnly, Cooldown: w.Cooldown, MaxAlerts: w.MaxAlerts}))
	}
	if e := w.Email; e.Host != "" {
		var password string
		if e.PasswordFile != "" {
			b, err := os.ReadFile(e.PasswordFile)
			if err != nil {
				return nil, fmt.Errorf("watch.email.password_file: %w", err)
			}
			password = strings.TrimSpace(string(b))
			if password == "" {
				return nil, fmt.Errorf("watch.email.password_file must not be empty")
			}
		}
		sender := alert.NewEmail(alert.EmailOptions{Host: e.Host, Port: e.Port, Username: e.Username, Password: password, From: e.From, To: e.To})
		result = append(result, alert.New(alert.Options{Sender: sender, Batch: e.Batch, ProblemsOnly: e.ProblemsOnly, Cooldown: w.Cooldown, MaxAlerts: w.MaxAlerts}))
	}
	return result, nil
}

// runPasskeyInvite prints a one-time passkey registration link. It writes
// next to the OAuth state file, so run it as the server's account
// (e.g. sudo -u opsgate opsgate passkey invite ...).
func (c *application) runPasskeyInvite(args []string) error {
	fs := c.flags("passkey invite")
	path := fs.String("config", "", "path to opsgate.yaml (required)")
	subject := fs.String("subject", "", "configured user, e.g. passkey:alice (required)")
	ttl := fs.Duration("ttl", 24*time.Hour, "how long the link stays valid (max 168h)")
	qr := fs.Bool("qr", false, "also draw the link as a QR code on stderr to open it on a phone (default on a terminal)")
	if err := c.parse(fs, args); err != nil {
		return err
	}
	if *path == "" || *subject == "" {
		return usageError("required flags: -config and -subject")
	}
	if *ttl <= 0 || *ttl > 168*time.Hour {
		return usageError("-ttl must be greater than zero and at most 168h")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := cfg.ValidateHTTP(); err != nil {
		return err
	}
	link, err := oauth.CreatePasskeyInvite(cfg.Auth.Builtin, cfg.Issuer(), *subject, *ttl)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(c.stdout, link); err != nil {
		return err
	}
	c.printQR(link, *qr)
	return nil
}

// undoURL links an undo to this server's approval page. Only catalog actions
// that are enabled for links qualify; a plain http origin (local testing)
// falls back to a relative link that the approval page can still use.
func undoURL(base string, env *tool.Env) func(tool.Undo) string {
	return func(u tool.Undo) string {
		if env.Direct(u.Tool) == nil {
			return ""
		}
		link, err := action.URL(base, u.Tool, u.Args, "rule")
		if err != nil {
			link, err = action.URL("", u.Tool, u.Args, "rule")
		}
		if err != nil {
			return ""
		}
		return link
	}
}
