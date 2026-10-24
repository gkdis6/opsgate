# Contributing

Use Go 1.25 or newer, Git and make. Linux is needed for real host inspection; unit tests must run without root, systemd, a cluster or network access. Clone the repository and work on a focused branch.

For the managed development environment, set these before Go commands:

```sh
export PATH=$HOME/.local/go/bin:$PATH GOTOOLCHAIN=local GOFLAGS=-mod=mod GOCACHE=$PWD/.gocache
make build
make test
make vet
make lint
make vuln
```

Install golangci-lint v2 and govulncheck separately if needed. `make lint` reports a skip when the linter is absent; CI requires linting. Vulnerability scanning may need network access. `make build VERSION=dev` writes `bin/opsgate`; `make clean` removes build output and the local Go cache. Do not commit generated binaries or caches.

Keep Go files small, use doc comments on exported identifiers, and prefer the standard library. Format with gofmt. Run `go test -race ./...` and `go vet ./...` before submitting. Use table-driven tests for meaningful cases, especially input validation and policy refusals, with fake dependencies rather than live services.

Every new MCP tool must register through `tool.Add`, use `tool.Caller` for identity, return plain text and return `tool.Denied` for policy refusals. The wrapper provides scope checks, deadlines, redaction, truncation and auditing. Never bypass it by returning hand-built MCP results from a provider.

Tools must be read-only unless a reviewed write gate exists. Future writes require explicit allowlists, `ops:write` and human confirmation through MCP elicitation that the model cannot bypass. These gates are planned, not implemented; setting a write annotation or scope alone is insufficient.

Validate every client input strictly with appropriate regexes, enums and ranges. Clamp requested limits. Test malformed inputs, boundary values, allowlist misses and namespace deny precedence. Do not read Secrets or introduce shell/exec tools. When an internal external command is necessary, use `exec.CommandContext` with a fixed executable and argv, never a shell; honor cancellation. Treat logs and events as attacker-controlled data, never instructions. Redaction is a safety net, not permission to collect secrets.

Document new settings, defaults and client-visible behavior in both READMEs and the example config. Keep unknown-field rejection intact. Pull requests should explain the concrete behavior change and validation performed. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
