# opsgate

opsgate gives MCP clients constrained access to Linux and Kubernetes, with explicit policy, human approval for changes, and audit records.

[한국어](README.ko.md) · [Client setup](docs/clients.md) · [Configuration](docs/configuration.md) · [Tools](docs/tools.md) · [Security](SECURITY.md)

An open-source Go MCP server for clients such as Claude, ChatGPT and Cursor. It supports stdio and authenticated Streamable HTTP, builtin OAuth with GitHub/OIDC login, external JWT verification and static bearer tokens. Linux host and Kubernetes inspection are the core providers. Obsidian vault access and coding tasks through a separate dev runner are optional, experimental providers.

Providers start disabled. Host/Kubernetes action allowlists start empty, vault writes are off, and coding task starts require explicit dev enablement and `dev:run`. Nine tools use human approval: `host_service_restart`, `k8s_rollout_restart`, `k8s_scale`, `k8s_delete_pod`, `obsidian_create`, `obsidian_append`, `obsidian_replace`, `obsidian_trash`, and `dev_task_start`. Each is registered only when its provider/action gate permits it, subject to server tool policy. `dev_task_cancel` is an explicit low-risk exception that stops an owned task without another approval (runner admins may stop any task).

**Release status:** these features are implemented and preparing for the first public release; third-party security review is pending. Redaction is best effort, and tool output can contain prompt injection. Read [the trust model](SECURITY.md) before deploying.

## Stdio quick start

Build with Go 1.26+:

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest
# Or from a checkout:
make build
```

Save this as `/path/opsgate.yaml`:

```yaml
stdio:
  scopes: [ops:read]
providers:
  host:
    enabled: true
    units: [ssh.service, "nginx*"]
```

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

Replace paths with absolute paths. A source build writes `bin/opsgate`. Host inspection uses Linux systemd/journald and `/proc`; stdio trusts the launching OS account. Run unprivileged and protect configuration ownership. Without configuration, all providers are disabled. See [client setup](docs/clients.md) for Desktop, Cursor and SSH, and [the complete read-only example](opsgate.example.yaml).

## HTTP quick start with passkeys (no identity provider)

The simplest remote setup needs no Google, GitHub or other identity provider: people sign in with a passkey (Face ID, Touch ID, Windows Hello, a phone or a security key).

```yaml
http:
  listen: 127.0.0.1:8787
  public_url: https://mcp.example.com
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: passkey
    users:
      - subject: passkey:alice
        scopes: [ops:read]
```

On first start opsgate logs a one-time setup link (valid 24 hours) for every user without a passkey. Open it on your phone or computer and press **Create passkey**. Later links come from `sudo -u opsgate opsgate passkey invite -config /etc/opsgate/opsgate.yaml -subject passkey:alice`. Connecting a client then shows **Sign in with passkey** instead of an external login.

## HTTP quick start with Google

Create a Google OIDC web application with the exact redirect URI `https://mcp.example.com/oauth/callback`. Replace the origin, client ID and verified Workspace email below (Gmail/Googlemail or an immutable `oidc:<sub>` identity also work). Provision the private writable state directory and protected client-secret file first.

```yaml
http:
  listen: 127.0.0.1:8787
  public_url: https://mcp.example.com
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: oidc
      issuer: https://accounts.google.com
      client_id: YOUR_GOOGLE_CLIENT_ID
      client_secret_file: /etc/opsgate/google-client-secret
    users:
      - subject: email:operator@example.com
        scopes: [ops:read]
approval:
  web: true
providers:
  host:
    enabled: true
    units: [myapp.service]
```

```sh
opsgate -transport http -config /etc/opsgate/opsgate.yaml
claude mcp add --transport http opsgate https://mcp.example.com/mcp
```

Terminate TLS at a trusted reverse proxy preserving Host and forwarding `/mcp`, `/.well-known/*`, `/oauth/*`, `/approve/*` and `/admin/clients`. The public Host must match `public_url` (including any explicit port); other hosts receive 421, with a local-loopback exception for loopback listeners. The SDK localhost check is replaced by this check, so a loopback backend behind a Host-preserving proxy works. See [deployment](docs/deploy.md) for probes and templates. Claude/ChatGPT OAuth connectors can use builtin Dynamic Client Registration; configure redirect-host policy before public exposure. This minimal example leaves the host list empty, accepting any otherwise valid redirect host.

## Policy and approval

One instance is one trust group: `ops:read` grants read access across every enabled provider, including servers, cluster resources and notes. `server.tools` controls which tools exist; user/token `tools` globs narrow calls further. Use separate instances and OS accounts for production operations, personal notes and coding agents with different trust levels. Kubernetes namespace rules supplement RBAC and do not restrict Nodes or PersistentVolumes. No Secrets tool or arbitrary shell/command tool is exposed; configured dev agents can run commands within their own permissions and sandbox.

Host/Kubernetes and vault writes need `ops:write` plus their provider action gate and OS/RBAC permission. All dev tools need `dev:run`; HTTP additionally requires `ops:read`. The server plans an operation before requesting human approval. Elicitation uses the client UI; builtin browser approval signs in the same user and binds a single-use decision to the exact arguments and plan. Set `approval.require_web: true` with `approval.web: true` to require browser approval; this denies stdio writes. A compromised client can accept its own elicitation prompts.

Approval windows are off by default. An opt-in window covers a user/tool pair across clients and targets until expiry and lives only in memory. Do not enable windows for `dev_task_start`. Worktrees are not a sandbox; task ownership relies on the trusted socket peer's asserted user. Large note previews are truncated with an omitted-line count, and an external edit between a vault update's optimistic check and rename can be overwritten.

Arguments default to 16 KiB; note content tools allow 528 KiB of JSON with 256 KiB per content/replacement input, and `dev_task_start` allows 48 KiB with a 32 KiB prompt. Summaries allow `max(64 KiB, argument limit + 16 KiB)`. Provider output above 4 MiB is omitted; other output is redacted before truncation to the configured cap (default 64 KiB). Calls and write phases have cooperative deadlines, and audit records include denials and approvals. See [tools](docs/tools.md), [configuration and audit reference](docs/configuration.md), [dev setup](docs/dev.md) and [security limitations](SECURITY.md).

## Development and license

See [CONTRIBUTING.md](CONTRIBUTING.md), [deployment and release verification](docs/deploy.md), and [the roadmap](docs/roadmap.md). Licensed under [Apache-2.0](LICENSE), copyright 2026 The opsgate Authors.
