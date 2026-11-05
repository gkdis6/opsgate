# Connecting clients

[한국어](ko/clients.md)

opsgate supports stdio and authenticated Streamable HTTP at `<public_url>/mcp`. The documented features are implemented for the first public release; third-party security review is pending. Use [deployment](deploy.md) to configure TLS, authentication and upstream callbacks.

For stdio, the client launches the binary and exchanges MCP messages through stdin/stdout. Use absolute executable/config paths. Keep stdout free of banners; diagnostics and the default audit destination use stderr.

The process runs with the launching OS user's permissions and configured `stdio.scopes`. `stdio.user` is an audit label. Protect the config and use an unprivileged account. Enable only needed providers and allowlisted namespaces. Kubernetes kubeconfig `exec` and `auth-provider` credential plugins are rejected; use credentials such as a short-lived ServiceAccount token. Namespace policies do not restrict Nodes or PersistentVolumes; review the [RBAC manifest](../deploy/kubernetes/rbac-readonly.yaml) and [security policy](../SECURITY.md).

## Claude Code

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

## Claude Desktop

Merge into `claude_desktop_config.json`, preserving other servers:

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "/path/to/opsgate",
      "args": ["-config", "/path/opsgate.yaml"]
    }
  }
}
```

Use Claude Desktop's developer settings to locate its configuration and restart the app after editing. On macOS, use a Darwin build with a local kubeconfig. There are no Windows builds; use SSH to a Linux machine with kubeconfig access to the cluster.

## Cursor

Merge into the project's `.cursor/mcp.json` or your user-level Cursor `mcp.json`:

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "/path/to/opsgate",
      "args": ["-config", "/path/opsgate.yaml"]
    }
  }
}
```

Only trust project MCP configurations you have reviewed: they can launch programs as your user.

## Over SSH

Install opsgate and its configuration without a mode on a Linux machine with kubeconfig access to the cluster. The stdio command is:

```sh
ssh host /usr/local/bin/opsgate -config /etc/opsgate.yaml
```

For Claude Code:

```sh
claude mcp add opsgate -- ssh -T host /usr/local/bin/opsgate -config /etc/opsgate.yaml
```

Desktop and Cursor can use this server entry:

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "ssh",
      "args": ["-T", "host", "/usr/local/bin/opsgate", "-config", "/etc/opsgate.yaml"]
    }
  }
}
```

Set up host-key verification and noninteractive SSH authentication first. Do not disable host-key checking. Use a dedicated unprivileged remote account and no pseudo-terminal. Remote startup files must not print to stdout. Keep remote command paths fixed and trusted; never interpolate tool input into SSH commands. SSH supplies transport security; this is still a stdio MCP server, not an HTTP endpoint.

## claude.ai and ChatGPT over HTTP

opsgate serves the sessionless MCP protocol `2026-07-28` used by ChatGPT statelessly.

Add a custom connector in the client's connector/integration settings, select OAuth, and enter the full MCP URL, for example `https://mcp.example.com/mcp`. The client uses Protected Resource Metadata and authorization-server discovery; builtin mode supports OAuth Dynamic Client Registration (DCR), so no manual MCP client ID or secret provisioning is needed. A configured redirect-host policy may reject the client or hold it pending review at `/admin/clients`; `notify_url` can notify an administrator. An empty host list accepts any otherwise valid redirect host. The upstream Google/GitHub application credentials belong only on the server. Sign in upstream as an allowlisted user and inspect the consent screen's client, redirect destination and scopes before granting access. Connector availability and setting names depend on the client/account.

Register `<public_url>/oauth/callback` at the upstream identity provider, not the MCP URL. The proxy must forward `/mcp`, `/.well-known/*`, `/oauth/*`, `/passkey/*`, `/passkey.js`, `/approve/*` and `/admin/clients` and support streaming. Preserve the public Host, including any explicit port; other hosts receive 421 except local-loopback requests on a loopback listener. The SDK localhost check is replaced by this validation. External mode depends on the external authorization server's client registration support; static mode does not supply OAuth/DCR.

## Claude Code over HTTP

```sh
claude mcp add --transport http opsgate https://host/mcp
```

Use the client's authentication flow to sign in. Replace `host` with your public HTTPS origin. Keep the stdio setup above if you prefer local or SSH access.

## Static tokens for local HTTP clients

Run `opsgate -gen-token` in a private terminal. It prints a raw token and its SHA-256 digest. Put only the digest in the server configuration:

```yaml
http:
  listen: 127.0.0.1:8787
  public_url: http://127.0.0.1:8787
auth:
  mode: static
  static:
    - name: local-client
      sha256: REPLACE_WITH_GENERATED_HEX_DIGEST
      scopes: [ops:read]
```

Start with `opsgate -transport http -config /path/opsgate.yaml`. Configure a local HTTP client's secret/header facility to send `Authorization: Bearer <raw-token>` to `http://127.0.0.1:8787/mcp`. Never put the token in a URL, committed config or shared logs. Static tokens have no OAuth refresh flow: remove/replace their digest and restart to revoke/rotate them. Keep the listener on loopback; use HTTPS for remote clients.

## Approving writes

Kubernetes writes are registered only with their provider and action allowlist; note writes need the vault provider and `allow_write`. They also need `ops:write`, OS/RBAC permission and human confirmation; HTTP users need `ops:read` too. Clients supporting MCP elicitation display the plan in an in-client prompt, using multi round-trip input requests across protocol versions.

Otherwise, builtin auth with `approval.web: true` returns a browser approval link and `approval_id`. Open the link, sign in as the same upstream user and read the exact summary. After approval, the client retries the same tool and arguments plus `approval_id`. Approval is expiring, single-use and bound to the exact operation. Static/external clients without elicitation cannot write; stdio also requires elicitation. A compromised UI can auto-approve elicitation, and prompt injection can request writes: humans must read the summary.

Dev tools instead require `dev:run`; task starts need approval and owned-task cancellation does not. Runner admins may cancel any task. HTTP still requires `ops:read`. Approval windows are off by default; when enabled and selected by a human, a grant covers one user, tool and target across clients. Do not enable windows for `dev_task_start`. See [dev setup](dev.md) and [the trust model](../SECURITY.md).
