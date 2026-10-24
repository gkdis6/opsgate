// Command opsgate is an MCP server for safe, audited server operations.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/server"
	"github.com/gkdis6/opsgate/internal/tool"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("opsgate: ")
	log.SetOutput(os.Stderr) // stdout belongs to the stdio transport

	cfgPath := flag.String("config", "", "path to opsgate.yaml")
	transport := flag.String("transport", "stdio", "transport: stdio")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *version {
		fmt.Println(server.Version)
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
		s, err := server.New(cfg, env)
		if err != nil {
			return err
		}
		return s.Run(ctx, &mcp.StdioTransport{})
	default:
		return fmt.Errorf("unknown transport %q", transport)
	}
}
