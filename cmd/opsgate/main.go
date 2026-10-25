// Command opsgate is an MCP server for safe, audited server operations.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/approval"
	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/authn"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/devrunner"
	"github.com/gkdis6/opsgate/internal/httpserver"
	"github.com/gkdis6/opsgate/internal/oauth"
	"github.com/gkdis6/opsgate/internal/server"
	"github.com/gkdis6/opsgate/internal/tool"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("opsgate: ")
	log.SetOutput(os.Stderr) // stdout belongs to the stdio transport

	if len(os.Args) > 1 && os.Args[1] == "dev-runner" {
		if err := runDevRunner(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}

	cfgPath := flag.String("config", "", "path to opsgate.yaml")
	transport := flag.String("transport", "stdio", "transport: stdio or http")
	version := flag.Bool("version", false, "print version and exit")
	genToken := flag.Bool("gen-token", false, "print a random static token and its sha256, then exit")
	flag.Parse()

	switch {
	case *version:
		fmt.Println(server.Version)
		return
	case *genToken:
		if err := printToken(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(*cfgPath, *transport); err != nil {
		log.Fatal(err)
	}
}

func run(cfgPath, transport string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
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
	env.Confirm = am
	o.Routes = append(o.Routes, am.Routes)

	s, err := server.New(cfg, env)
	if err != nil {
		return err
	}
	o.Server = s
	log.Printf("serving %s on %s", cfg.ResourceURL(), cfg.HTTP.Listen)
	return httpserver.Serve(ctx, cfg, httpserver.Handler(cfg, o))
}

func printToken() error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tok := "og_st_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(tok))
	fmt.Printf("token:  %s\nsha256: %s\n", tok, hex.EncodeToString(sum[:]))
	return nil
}

func runDevRunner(args []string) error {
	fs := flag.NewFlagSet("dev-runner", flag.ContinueOnError)
	path := fs.String("config", "", "path to dev-runner.yaml (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return fmt.Errorf("usage: opsgate dev-runner -config <file>")
	}
	cfg, err := config.LoadDevRunner(*path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return devrunner.Serve(ctx, cfg)
}
