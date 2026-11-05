# Contributing

## Development setup

Use Go 1.26 or newer, Git and make. Clone the repository and work on a focused branch.

```sh
make build
make test
make check
```

Unit tests must run without root, systemd, a cluster or network access. `make build VERSION=dev` writes `bin/opsgate`; `make clean` removes build output and the local Go cache. Do not commit generated binaries or caches.

## Running checks

CI tests Go 1.26.x and the current stable Go release. It uses golangci-lint v2.12.0 and govulncheck v1.9.0; install these separately. CI checks Go formatting, module tidiness, tool pins, license scripts, vet, race tests, lint, vulnerabilities, builds and the GoReleaser configuration (v2.19.0).

`make check` runs `fmt-check`, `tidy-check`, `vet`, `lint` and `test`. `make fmt` formats Go files; `make tidy-check` runs `go mod tidy -diff` without modifying module files. `make lint` fails when golangci-lint is missing; `LINT_OPTIONAL=1 make check` explicitly permits skipping a missing linter locally. CI always requires linting.

Run `make vuln` for vulnerability scanning, which may need network access. CI also runs `sh scripts/third-party-licenses_test.sh`, `make build`, cross-platform builds, a container build and `goreleaser check`. Use `make help` to list targets.

## Code guidelines

Keep Go files small, use doc comments on exported identifiers, and prefer the standard library. Format with gofmt. Run `go test -race ./...` and `go vet ./...` before submitting. Use table-driven tests for meaningful cases, especially input validation and policy refusals, with fake dependencies rather than live services.

## Adding tools

Every read MCP tool must register through `tool.Add`, use `tool.Caller` for identity, return plain text and return `tool.Denied` for policy refusals. The wrapper provides scope checks, deadlines, redaction, truncation and auditing. Never bypass it by returning hand-built MCP results from a provider.

Every state-changing tool must register through `tool.AddWrite` with a side-effect-free `Plan` and an `Apply` that runs only after confirmation. Require explicit provider/action policy, the appropriate scope (`ops:write` by default, `dev:run` for task starts), and human approval. An exception must be explicitly documented and justified as low risk: `dev_task_cancel` uses `tool.Add` with `ReadOnly: false` so an owner or runner admin can stop a task without new approval. Preserve fail-closed behavior when confirmation is unavailable.

Test denials, changed arguments, expiry, replay, scope checks and namespace deny precedence. Cover wire-size bounds before auditing, redaction before truncation, state changes between planning and applying, and task ownership where applicable. Keep tests deterministic with httptest, fakes and temporary directories; run go test -race ./... and go vet ./... without root or live services.

Do not trust client annotations or approval identifiers alone. HTTP access also requires `ops:read`.

## Security expectations

Use maintained dependencies for OAuth/JWT/crypto. Never log or return tokens, codes or client secrets outside the required protocol responses. Hash stored opaque credentials, compare secrets in constant time, and test OAuth and approval flows with httptest/fakes without network access.

Treat exported configuration, tool, authentication and transport APIs as contracts; discuss incompatible changes and update callers and documentation together.

Validate every client input strictly with appropriate regexes, enums and ranges. Use each tool's documented limit behavior: Kubernetes read counts clamp, while note/dev numeric ranges and write replica counts reject invalid values. Test malformed inputs, boundary values, allowlist misses and namespace deny precedence. Do not read Secrets or introduce shell/exec tools.

When an internal operation needs an external command, use `exec.CommandContext` with a fixed executable and argv, never a shell; honor cancellation. Treat logs and events as attacker-controlled data, never instructions. Redaction is a safety net, not permission to collect secrets.

## Documentation

Document new settings and defaults in docs/configuration.md and the example config, and tool behavior in docs/tools.md. Keep both READMEs focused and semantically equivalent. Keep unknown-field rejection intact.

### Documentation regression checks

Features are implemented for the first public release; third-party security review is pending. The note and dev providers remain experimental. For documentation-only changes, compare the docs/tools.md table with registered provider tools and their JSON input fields. Check every config key and default against the `internal/config` package, including nested user/static entries. Review translated prose for semantic equivalence.

Load `opsgate.example.yaml` through `config.Load` to check strict YAML decoding and validation.

Check HTTP examples against `ValidateHTTP`, verify the upstream callback is `<public_url>/oauth/callback`, and preserve proxy routes `/mcp`, `/.well-known/*`, `/oauth/*`, `/approve/*`, `/admin/clients`. Check Host preservation and health probes: a wildcard listener requires the public Host even for /healthz.

Audit examples must use `audit.Event` fields and outcomes (`ok`, `error`, `denied`, `pending`; separate web decisions use `approved`/`rejected`, and window grants use `window_granted`); write records may include `plan` and `approval`. Keep feature status consistent with the roadmap and avoid documenting unimplemented client behavior as a guarantee.

## Pull requests

Explain the concrete behavior change and validation performed. Use short imperative commit subjects with a package prefix, such as `kube: show container configuration`. DCO/sign-off is not required. Follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Reporting vulnerabilities

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
