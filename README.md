<p align="center"><img src="docs/images/logo.png" alt="opsgate mascot: a haetae guardian with an approval check" width="240"></p>

# opsgate

[한국어](README.ko.md)

**Let AI assistants help operate your clusters, without handing them a shell.**

opsgate is an open-source MCP server written in Go. It gives clients such as
Claude, ChatGPT, Claude Code and Cursor a fixed set of operations tools for Kubernetes, with explicit policy, human-approved changes and
an audit log.

> **Status:** features for the first public release are implemented. Changes
> require user authorization, allowed targets and human approval. Read restrictions
> and output redaction reduce sensitive data exposure. Third-party security review
> is pending; the [security policy](SECURITY.md) explains the protections and
> remaining limitations.



## Why opsgate


### No arbitrary commands

Only purpose-built tools exist. There is no shell
tool and no Kubernetes Secrets tool.

### Everything starts off

Providers, write actions and allowlists are disabled
until you enable them in configuration.

### Changes need a person

Write tools first plan the exact change, then wait
for human approval in the browser by default when web approval is available.
Optional approval windows are explained below.

### Remote access is authenticated

A builtin OAuth 2.1 server works with
claude.ai and ChatGPT connectors. Sign in with passkeys, GitHub or OIDC
(Google included), or verify external JWTs.

### Results are data for analysis

AI clients are instructed not to follow
instructions embedded in logs or other results. Returned results are redacted
and size-limited. The audit log records who called each tool and its outcome,
with one JSON object per line.

### It remembers what happened

The optional note provider keeps runbooks and
past incidents in plain Markdown. Assistants are instructed to consult past
records and offer to record the outcome. See [notes](docs/notes.md).



## What it can do

Providers are groups of tools; enable only the groups you need.

| Provider | Read tools | Write tools (approval required) |
| --- | --- | --- |
| Kubernetes | overview, list, get, events, pod logs | rollout restart, scale, delete pod |
| Notes: runbooks and memory, Markdown folder or Obsidian vault *(experimental)* | search, read, list, recent, links, tags | create, append, replace, move to `.trash` |
| Dev *(experimental)* | projects, task status, log, diff, list | start a coding agent (Codex or Claude) in a Git worktree |

Scopes define the kinds of work a user can perform through opsgate: `ops:read` for inspection,
`ops:write` for operations and note changes, and `dev:run` for coding-agent tasks.
Allowlists restrict tools and targets such as Kubernetes workloads.
A scope alone does not enable an operation prohibited by configuration.

Notes can be created only inside the configured notes folder. There is no tool
for creating general files at arbitrary server paths or changing Kubernetes CPU
and memory requests or limits. Those resource settings can be inspected with
Kubernetes detail tools. See the [tool reference](docs/tools.md) for arguments
and limits.



## How it works

<img src="docs/images/architecture.svg" alt="opsgate architecture: client transports, authentication, tool processing, providers and OS account / RBAC trust boundaries" width="830">

The AI client analyzes results and chooses the next operation. opsgate itself
does not run or call an AI model; it checks and executes requested tools.

Kubernetes tools connect to the Kubernetes API using kubeconfig or in-cluster credentials.
A local instance can therefore inspect a remote cluster when network access
and permissions allow it.

Clients connect over local stdio (the client starts opsgate and communicates
through standard input/output) or HTTPS through a TLS reverse proxy. opsgate
checks identity and policy, plans and approves writes, then handles output and
auditing. OS accounts and Kubernetes RBAC constrain access to the providers.

Details: [architecture](docs/architecture.md) · [approval](docs/approval.md) ·
[authentication](docs/authentication.md).

### How approval works

Write tools first prepare a plan showing the target and change. By default,
a person reviews and approves it on a web approval page when web approval is
available. Explicit `approval.require_web: false` opts in to in-client elicitation;
a compromised client can then approve its own writes. External/static auth and
stdio keep elicitation because they have no web channel. An omitted `require_web`
also allows elicitation with `-transport stdio` for mode-based files; explicit
`true` denies stdio writes. One-call approval binds the exact arguments and plan.

Optional approval windows let a person approve a particular tool for a limited
time. During that window, subsequent calls by the same user can skip a new
approval prompt, including calls with different targets or arguments. Permission
and target checks still run on every call. Windows are disabled by default.

<img src="docs/images/approval-flow.svg" alt="Write approval flow: plan, approval window or client / web confirmation, retry verification, Apply and denial exits" width="830">

### Notes: runbooks and memory

With the note provider enabled, assistants are instructed to search past
incidents before acting and offer to record the outcome afterward. The AI client
decides whether to follow this guidance; it is not an enforced workflow. Note
changes require approval, and saved records can inform later investigations. See [notes](docs/notes.md).

<img src="docs/images/note-memory.svg" alt="Notes memory loop: search upstream timeout history, propose a past fix, approve and apply, then record an approved note for next time" width="830">



## Quick start: local (stdio)

Build with Go 1.26 or later:

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest   # or: make build
```

Save a configuration using the current kubeconfig, for example `/path/opsgate.yaml`:

```yaml
stdio:
  scopes: [ops:read]
providers:
  kubernetes:
    enabled: true
    namespaces: [default]
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

`opsgate init` asks for the public URL, who signs in and the namespaces, then writes
a short configuration:

```sh
opsgate init            # writes opsgate.yaml; flags answer ahead, see -h
```

```yaml
mode: kubernetes
http:
  public_url: "https://mcp.example.com"
auth:
  builtin:
    users:
      - subject: "passkey:alice"
        scopes: [ops:read]
    admins: ["passkey:alice"]
    allowed_redirect_hosts: [claude.ai, chatgpt.com, localhost]
    unknown_redirect_hosts: approve
providers:
  kubernetes:
    namespaces: ["*"]
    deny_namespaces: ["kube-system", "kube-public", "kube-node-lease", "opsgate"]
    allow_logs: true
```

The mode fills in the rest: HTTP on `0.0.0.0:8787`,
builtin passkey sign-in, state in `/var/lib/opsgate`, browser approval links and
the pod’s ServiceAccount. `opsgate config show -config opsgate.yaml` prints every
effective value.

Put this file in the `opsgate` ConfigMap of `deploy/kubernetes/opsgate-deployment.yaml` and apply it. The manifest’s sample uses Google sign-in; replacing its config with passkeys still leaves the required `opsgate-upstream` Secret mount. Follow [deployment](docs/deploy.md) for Secret provisioning, image verification, RBAC and probe configuration.

```sh
kubectl apply -f deploy/kubernetes/opsgate-deployment.yaml
```

Run `opsgate -h` to list commands and flags, or `opsgate help <command>` for command help. `opsgate version` (also `-version`) prints the build version. Help and successful commands exit 0; invalid commands, flags or extra arguments exit 2 before loading configuration; operational errors exit 1. Help goes to stdout and errors to stderr.

1. On first start, opsgate logs a one-time setup link (valid 24 hours) for each
   user without a passkey. Open it and press **Create passkey**. Later links:
   `kubectl -n opsgate exec deploy/opsgate -- /opsgate passkey invite -config /etc/opsgate/opsgate.yaml -subject passkey:alice`.
2. Put a TLS ingress or reverse proxy in front that preserves the `Host` header. The public
   host must match `public_url`; other hosts get 421.
3. Connect a client to `https://mcp.example.com/mcp`, for example
   `claude mcp add --transport http opsgate https://mcp.example.com/mcp`, or add
   it as a connector in claude.ai or ChatGPT.

The generated file already limits client redirect hosts
(`allowed_redirect_hosts`); review them before exposing the server. GitHub, Google and other sign-in options are covered
in [authentication](docs/authentication.md). Proxy routes and
release verification are in [deployment](docs/deploy.md).



## Watch alerts

Run `opsgate watch` alongside the MCP server to receive Kubernetes problem and
change notifications. It watches the Kubernetes API and sends alerts directly
to Discord, custom JSON webhooks or SMTP email. n8n is optional. Paste an alert
into an MCP client to investigate the resource with opsgate's read tools.

Add the following to a configuration with the Kubernetes provider enabled:

```yaml
watch:
  outputs:
    - name: discord
      type: discord
      url_file: /etc/opsgate/discord-webhook-url
    - name: custom
      type: webhook
      url: https://alerts.example.com/ingest
      header_files:
        Authorization: /etc/opsgate/alerts-authorization
      body_template: |
        {"message": {{json .Text}}, "sent_at": {{json .SentAt}}, "alerts": {{json .Alerts}}}
```

Keep the destinations you need. Store the Discord channel's incoming webhook URL
in `url_file`; the authorization file contains the full header value, such as
`Bearer TOKEN`. Files are read at startup, so restart watch after changing them.
In Kubernetes, mount these files from Secrets.

```sh
opsgate watch -config opsgate.yaml
```

Each destination has its own queue, cooldown and retries. Discord embeds include
UTC occurrence times, cluster, namespace/resource, reason and summary; long
batches are visibly truncated to fit Discord's limit. Custom templates use the
`json` helper to escape values and must produce valid JSON. Existing
`watch.webhook_url` and `watch.email` settings remain supported alongside outputs.
See [watch alerts](docs/watch.md) for delivery limits, HMAC signatures and all
configuration options.



## Security model in brief

- One instance is one trust group: `ops:read` covers every enabled provider. Use
  separate instances and OS accounts for things with different trust levels,
  such as production operations, personal notes and coding agents.
- Permission checks stack: scopes, server tool policy, per-user tool lists,
  provider allowlists, then OS permissions and Kubernetes RBAC.
- HTTP requires browser approval by default when `approval.web` is enabled.
  Explicit `approval.require_web: false` opts in to elicitation, allowing a
  compromised client to approve its own writes; HTTP startup logs a warning.

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
| [Deployment](docs/deploy.md) | Release verification, Kubernetes, reverse proxy |
| [Dev provider](docs/dev.md) | Running coding agents through the separate runner |
| [Watch alerts](docs/watch.md) | Kubernetes alerts to Discord, custom JSON webhooks and email through a separate `opsgate watch` process |
| [Roadmap](docs/roadmap.md) | What is done and what may come |



## Contributing and license

See [CONTRIBUTING.md](CONTRIBUTING.md), the [Code of Conduct](CODE_OF_CONDUCT.md)
and [changelog](CHANGELOG.md). Licensed under [Apache-2.0](LICENSE),
copyright 2026 The opsgate Authors.
