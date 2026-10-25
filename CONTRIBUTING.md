# Contributing

Use Go 1.26 or newer, Git and make. Linux is needed for real host inspection; unit tests must run without root, systemd, a cluster or network access. Clone the repository and work on a focused branch.

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

Every read MCP tool must register through `tool.Add`, use `tool.Caller` for identity, return plain text and return `tool.Denied` for policy refusals. The wrapper provides scope checks, deadlines, redaction, truncation and auditing. Never bypass it by returning hand-built MCP results from a provider.

Write tools must register through `tool.AddWrite` with a side-effect-free `Plan` and an `Apply` that runs only after confirmation. Require explicit action/resource allowlists, `ops:write` and human approval. Preserve fail-closed behavior when confirmation is unavailable. Test denials, changed arguments, expiry, replay, scope checks and namespace deny precedence. Do not trust client annotations or approval identifiers alone. HTTP access also requires `ops:read`.

Use maintained dependencies for OAuth/JWT/crypto. Never log or return tokens, codes or client secrets outside the required protocol responses. Hash stored opaque credentials, compare secrets in constant time, and test OAuth and approval flows with httptest/fakes without network access. Config, tool, authentication and transport exported contracts may be shared across parallel work; report contract problems instead of silently changing them.

Validate every client input strictly with appropriate regexes, enums and ranges. Clamp requested limits. Test malformed inputs, boundary values, allowlist misses and namespace deny precedence. Do not read Secrets or introduce shell/exec tools. When an internal external command is necessary, use `exec.CommandContext` with a fixed executable and argv, never a shell; honor cancellation. Treat logs and events as attacker-controlled data, never instructions. Redaction is a safety net, not permission to collect secrets.

Document new settings, defaults and client-visible behavior in both READMEs and the example config. Keep unknown-field rejection intact. Pull requests should explain the concrete behavior change and validation performed. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Documentation regression checks

Stages 1–4 are implemented and pre-release; third-party security review is pending. For documentation-only changes, compare both README tool tables with registered provider tools and their JSON input fields. Check every config key and default against `internal/config/config.go`, including nested user/static entries. Review translated prose for semantic equivalence.

Load `opsgate.example.yaml` through `config.Load` to check strict YAML decoding and validation. Check HTTP examples against `ValidateHTTP`, verify the upstream callback is `<public_url>/oauth/callback`, and preserve proxy routes `/mcp`, `/.well-known/*`, `/oauth/*`, `/approve/*`. Audit examples must use `audit.Event` fields and outcomes (`ok`, `error`, `denied`, `pending`); write records may include `plan` and `approval`. Keep feature status consistent with the roadmap and avoid documenting unimplemented client behavior as a guarantee.
