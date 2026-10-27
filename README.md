# opsgate

[한국어](README.ko.md)

**Let AI assistants help operate your servers, without handing them a shell.**

opsgate is an open-source MCP server written in Go. It gives clients such as
Claude, ChatGPT, Claude Code and Cursor a fixed set of operations tools for Linux
hosts and Kubernetes, with explicit policy, human approval for every change and
an audit log.

> **Status:** feature-complete for the first public release; no third-party
> security review yet. Redaction is best effort and tool output can contain
> prompt injection. Read the [security policy](SECURITY.md) before deploying.

## Why opsgate

- **No arbitrary commands.** Only purpose-built tools exist. There is no shell
  tool and no Kubernetes Secrets tool.
- **Everything starts off.** Providers, write actions and allowlists are disabled
  until you enable them in configuration.
- **Changes need a person.** Write tools first plan the exact change, then wait
  for human approval in the client or in the browser. Approval is bound to the
  exact arguments and plan.
- **Remote access is authenticated.** A builtin OAuth 2.1 server works with
  claude.ai and ChatGPT connectors. Sign in with passkeys, GitHub or OIDC
  (Google included), or verify external JWTs.
- **Output is treated as untrusted.** Results are redacted, size-limited and
  recorded in a JSONL audit log.
- **It remembers what happened.** The optional note provider keeps runbooks and
  past incidents in plain Markdown. Assistants look up how a symptom was handled
  before and, with approval, record what they did. See [notes](docs/notes.md).

## What it can do

| Provider | Read tools | Write tools (approval required) |
| --- | --- | --- |
| Host (systemd, journald, `/proc`) | overview, services, unit status, logs, top processes | restart an allowed unit |
| Kubernetes | overview, list, get, events, pod logs | rollout restart, scale, delete pod |
| Notes: runbooks and memory, Markdown folder or Obsidian vault *(experimental)* | search, read, list, recent, links, tags | create, append, replace, move to `.trash` |
| Dev *(experimental)* | projects, task status, log, diff, list | start a coding agent (Codex or Claude) in a Git worktree |

Each write tool also needs the `ops:write` scope and its own allowlist entry. See
the [tool reference](docs/tools.md) for every argument and limit.

## How it works

```text
 MCP client ──stdio or HTTPS──▶ opsgate
                                 ├─ authenticate (OAuth / JWT / static token)
                                 ├─ check tool policy and scope
                                 ├─ write tools: plan ─▶ human approval ─▶ apply
                                 ├─ redact and size-limit output
                                 └─ audit log (JSONL)
                                       │
                 host · kubernetes · note · dev-runner (separate account)
```

Details: [architecture](docs/architecture.md) · [approval](docs/approval.md) ·
[authentication](docs/authentication.md).

## Quick start: local (stdio)

Build with Go 1.26 or later:

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest   # or: make build
```

Save a configuration, for example `/path/opsgate.yaml`:

```yaml
stdio:
  scopes: [ops:read]
providers:
  host:
    enabled: true
    units: [ssh.service, "nginx*"]
```

Register it with Claude Code (use absolute paths):

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

Stdio runs with the permissions of the account that launches it. Run it
unprivileged. Other clients: [client setup](docs/clients.md).

## Quick start: remote (HTTP with passkeys)

Passkeys need no identity provider: people sign in with Face ID, Touch ID,
Windows Hello, a phone or a security key.

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
approval:
  web: true
providers:
  host:
    enabled: true
    units: [myapp.service]
```

```sh
opsgate -transport http -config /etc/opsgate/opsgate.yaml
```

1. On first start, opsgate logs a one-time setup link (valid 24 hours) for each
   user without a passkey. Open it and press **Create passkey**. Later links:
   `sudo -u opsgate opsgate passkey invite -config /etc/opsgate/opsgate.yaml -subject passkey:alice`.
2. Put a TLS reverse proxy in front that preserves the `Host` header. The public
   host must match `public_url`; other hosts get 421.
3. Connect a client to `https://mcp.example.com/mcp`, for example
   `claude mcp add --transport http opsgate https://mcp.example.com/mcp`, or add
   it as a connector in claude.ai or ChatGPT.

Before exposing the server publicly, restrict client redirect hosts
(`allowed_redirect_hosts`). GitHub, Google and other sign-in options are covered
in [authentication](docs/authentication.md). Proxy routes, systemd units and
release verification are in [deployment](docs/deploy.md).

## Security model in brief

- One instance is one trust group: `ops:read` covers every enabled provider. Use
  separate instances and OS accounts for things with different trust levels,
  such as production operations, personal notes and coding agents.
- Permission checks stack: scopes, server tool policy, per-user tool lists,
  provider allowlists, then OS permissions and Kubernetes RBAC.
- A compromised client can accept its own in-client approval prompts. Set
  `approval.require_web: true` to require browser approval instead.

Full details: [SECURITY.md](SECURITY.md).

## Documentation

| Document | Contents |
| --- | --- |
| [Architecture](docs/architecture.md) | Request flow, packages, design principles |
| [Authentication](docs/authentication.md) | Passkey, GitHub, OIDC, external JWT, static tokens, stdio |
| [Approval](docs/approval.md) | Plan/apply, approval channels, approval windows |
| [Client setup](docs/clients.md) | Claude Code, Claude Desktop, claude.ai, ChatGPT, Cursor |
| [Configuration](docs/configuration.md) | Every configuration key, defaults and audit log format |
| [Tools](docs/tools.md) | Tool arguments, limits, redaction |
| [Notes](docs/notes.md) | Runbooks and incident memory in a Markdown folder |
| [Deployment](docs/deploy.md) | Release verification, systemd, Kubernetes, reverse proxy |
| [Dev provider](docs/dev.md) | Running coding agents through the separate runner |
| [Roadmap](docs/roadmap.md) | What is done and what may come |

## Contributing and license

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [Apache-2.0](LICENSE),
copyright 2026 The opsgate Authors.
