# Authentication

[한국어](ko/authentication.md)

For HTTP, select one `auth.mode`: `builtin`, `external` or `static`. For local use, select the `stdio` transport instead; it has no login and is not an authentication mode.

## Choose a sign-in method

| Option | Identity source | When to choose it | Web approval |
| --- | --- | --- | --- |
| Built-in + passkey | Local WebAuthn credentials, commonly `passkey:alice` | Personal use without an external identity provider | Yes, when enabled |
| Built-in + GitHub | `github:<login>` | Use an existing GitHub identity across devices; configure account protection at GitHub | Yes, when enabled |
| Built-in + OIDC | Explicitly allowed `oidc:<sub>` or verified `email:<address>` | Organization sign-in or Google accounts while opsgate serves OAuth to MCP clients | Yes, when enabled |
| External | String claim from an issuer's JWT access token | An existing authorization server manages client login and tokens | No |
| Static | `static:<name>` | Scripts or clients that can send a configured Bearer header | No |
| stdio | `stdio.user` label; launching OS account is the boundary | Local process integrations and automation | No; elicitation only |

For OAuth-based remote clients such as claude.ai and ChatGPT, choose built-in or a compatible external authorization server. Static mode provides no OAuth login; use it with direct-header clients such as Claude Code, Cursor or scripts. Client setup is described in [Clients](clients.md).

```text
builtin: client -> opsgate OAuth -> passkey / GitHub / OIDC sign-in
                               -> consent -> opsgate access/refresh tokens
external: client -> external authorization server -> JWT -> opsgate JWKS verification
static: client -> configured Bearer token -> opsgate SHA-256 comparison
stdio: client -> local opsgate process -> configured stdio identity/scopes
```

All HTTP modes require `ops:read` at `/mcp`. Additional scopes authorize tools: normally `ops:write` for writes and `dev:run` for coding tools. Scopes do not enable providers or bypass human approval. Optional user/token `tools` globs add restrictions on top of server policy.

With `approval.web: true`, HTTP defaults to web-only approval (`approval.require_web: true`); modes with builtin auth enable web approval automatically. Explicit `approval.require_web: false` opts in to in-client elicitation. A compromised client can then approve its own writes. External/static auth has no web channel and keeps `require_web` false by default; HTTP startup warns when elicitation is allowed.

## Built-in OAuth

opsgate handles client registration (DCR), S256 PKCE, a consent screen, access tokens, rotating refresh tokens and revocation. Users must be listed in `auth.builtin.users`. One `upstream.provider` is active at a time; passkey, GitHub and OIDC cannot be offered together by one process.

This complete minimal configuration loads with `config.Load`. It leaves providers disabled; enable the providers you need separately.

```yaml
http:
  listen: 127.0.0.1:8787
  public_url: https://example.com
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: passkey
    users:
      - subject: passkey:alice
        scopes: [ops:read, ops:write]
approval:
  web: true
```

```sh
opsgate -config opsgate.yaml -transport http
```

Serve public HTTPS through a reverse proxy. The MCP URL is `https://example.com/mcp`, and the built-in issuer is `https://example.com`. Loopback HTTP is allowed for local development. The HTTP listener itself does not terminate TLS.

### Passkey

Passkey sign-in needs no external application, client ID or secret. Use a configured subject such as `passkey:alice`. At startup, users without a registered passkey receive a setup invite in the process log, valid for 24 hours. Treat the invite link as a credential. Keep opsgate's own logs out of tool access, because the link grants passkey registration for that identity.

To add a passkey, run this as the server's OS account:

```sh
opsgate passkey invite -config opsgate.yaml -subject passkey:alice -ttl 24h
```

The command requires `state_file` and writes hashed invite files into `passkey-invites` beside it. The user must be configured and the active provider must be `passkey`. Invites are one-time, default to 24 hours, and can last at most seven days. Registered passkeys are saved in the state file when persistence is configured.

### GitHub

Create a GitHub OAuth App with callback `https://example.com/oauth/callback`. Replace the built-in upstream and users with these settings; keep the HTTP origin and state file from the complete example:

```yaml
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: github
      client_id: example-client-id
      client_secret_env: OPSGATE_GITHUB_SECRET
    users:
      - subject: github:alice
        scopes: [ops:read, ops:write]
```

Set the named environment variable in the server process. The server obtains the GitHub login and uses its lowercase `github:<login>` subject. GitHub performs account authentication; opsgate does not enforce a GitHub 2FA policy.

### OIDC, including Google

Register an OIDC application with callback `https://example.com/oauth/callback`. Replace the upstream and users as follows:

```yaml
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: oidc
      issuer: https://accounts.google.com
      client_id: example-client-id
      client_secret_file: /etc/opsgate/oidc-secret
    users:
      - subject: email:alice@example.com
        scopes: [ops:read]
```

An explicitly allowlisted `oidc:<sub>` takes precedence and does not depend on email availability or ownership. Otherwise the ID token must contain a verified email; the server normalizes it to lowercase and uses `email:<address>`. For Google, email mapping accepts Gmail/Googlemail addresses or a Workspace address whose `hd` matches its domain. A verified third-party mailbox alone is insufficient for that Google email mapping. The example address therefore needs the corresponding Workspace claim when using Google; alternatively configure the user's exact `oidc:<sub>`.

Put upstream secrets in `client_secret_file` or `client_secret_env`, not inline YAML. If both are set, the file takes precedence. Non-passkey upstreams require a nonempty client ID and secret at startup. OIDC discovery and endpoint validation also happen during initialization.

### Client registration and administrators

The following is an optional fragment to merge under the built-in settings:

```yaml
auth:
  builtin:
    allowed_redirect_hosts: [example.com, localhost]
    unknown_redirect_hosts: approve
    admins: [passkey:alice]
    users:
      - subject: passkey:alice
        scopes: [ops:read, ops:write]
```

Every administrator must also be an allowed user. Adjust the administrator subject for your upstream. With a nonempty redirect host list, unlisted redirect hosts are denied by default. `unknown_redirect_hosts: approve` instead keeps the client pending for administrator review at `/admin/clients`. An empty host list accepts any otherwise-valid redirect host. Exact hosts and supported private schemes are accepted; `localhost` also covers loopback IPs and any port. See [Configuration](configuration.md) for registration policy and optional `notify_url` notifications.

This client review is separate from per-operation human approval. Built-in token defaults are one hour for access tokens and 30 days for refresh tokens. `state_file` persists client records, token hashes, revocation/code state and passkeys with mode `0600`. If it is empty, state is lost on restart. Browser sessions are in memory. An incompatible issuer/resource in an existing state file prevents startup. State snapshots are limited to 512 MiB on both reads and writes. An oversized write preserves the previous snapshot and makes the running instance fail closed, like other persistence failures.

## External authorization server

Keep the HTTP settings and replace `auth` with:

```yaml
auth:
  mode: external
  external:
    issuer: https://identity.example.com
    audience: https://example.com/mcp
    user_claim: sub
    users:
      - subject: claim:alice
        scopes: [ops:read, ops:write]
approval:
  web: false
  require_web: false # No web channel; elicitation remains allowed.
```

opsgate discovers the issuer's metadata and JWKS and verifies JWT signature, issuer, audience and expiry. Issuer and JWKS endpoints require HTTPS, except loopback HTTP. The external server owns client registration, login, consent and token issuance; it must provide a flow compatible with the client. Opaque access tokens are not supported by this verifier. JWKS fetch attempts are shared across requests and limited to one per minute, including failed fetches. Cached valid keys continue to work; a new signing key may be rejected until the cooldown expires.

`audience` defaults to `http.public_url` plus `/mcp`; `user_claim` defaults to `sub` and must resolve to a nonempty string. The caller/audit identity is the claim value, such as `alice`; the `claim:` prefix is only for configuration matching.

If `users` is empty, recognized token scopes are trusted. With users configured, only listed identities are accepted and effective scopes are the intersection of token scopes and configured scopes, not an extra grant. `scope` is a space-separated string; otherwise `scp` can be a string or string array. Unknown scopes are ignored. Web approval is unavailable; approved writes need a client that supports elicitation.

## Static bearer tokens

Generate a token and its digest:

```sh
opsgate -gen-token
```

Keep the raw token in the client and copy only its 64-character hex SHA-256 into configuration. This fragment uses a syntactically valid placeholder digest; replace it with the generated digest before use:

```yaml
auth:
  mode: static
  static:
    - name: alice
      sha256: "0000000000000000000000000000000000000000000000000000000000000000"
      scopes: [ops:read]
approval:
  web: false
  require_web: false # No web channel; elicitation remains allowed.
```

Retain the HTTP settings. Send `Authorization: Bearer <raw token>` to `/mcp`. The identity is `static:alice`. Static tokens never expire. To revoke one, remove or replace its digest and restart. Web approval is unavailable, so approved writes need elicitation.

## Local stdio

```yaml
stdio:
  user: alice
  scopes: [ops:read]
```

```sh
opsgate -config opsgate.yaml -transport stdio
```

This fragment loads without HTTP/authentication settings. The transport defaults to stdio, the identity label defaults to `local`, and scopes default to `[ops:read]`. `stdio.user` is a label, not an OS impersonation setting. The launching OS account controls actual resource access. Approved writes need client elicitation. When `require_web` is omitted, `-transport stdio` allows elicitation even for mode-based files because the user starts the process locally. Explicit `approval.require_web: true` still denies stdio writes. `config show` makes the HTTP web-only default explicit; remove that key or set it to `false` before using its output for stdio writes.

## Switch methods

1. Prepare the destination identity provider or token. Retain the HTTP origin where possible. Back up the state file if using built-in persistence.
2. Update `auth.mode` for HTTP, or use `-transport stdio`. For built-in sign-in, set exactly one `auth.builtin.upstream.provider` and its required settings. Configuration is read at startup; restart to apply it.
3. Update subjects and grants in `users`, `admins` and any runner administrator list. Examples are `passkey:alice`, `github:alice`, `email:alice@example.com`, exact `oidc:<sub>`, external configuration `claim:alice` (caller `alice`), and `static:alice`.
4. Set `approval.web: false` and remove `approval.require_web` or set it to `false` when leaving built-in HTTP. Choose elicitation-capable clients for approved writes; a compromised client can approve its own writes through this channel. Stdio has no web channel and allows elicitation when `require_web` is omitted. When returning to built-in HTTP, remove an explicit `require_web: false` to restore the web-only default.
5. Restart and reconnect or reauthorize clients for the new method. Verify identity, scopes and provider/tool policy with a read operation before requesting writes.

Changing provider does not automatically revoke all saved built-in tokens. Existing tokens are checked against the current allowed users and scopes; explicitly remove old subjects/grants if they must stop working. External/static modes do not load the built-in state file. Keeping that file lets registered passkeys be reused when returning to passkey sign-in with the same compatible origin, subject and credentials. Do not delete it merely to change upstreams. Changing the public issuer/resource requires compatible new state and reconnecting clients.

Unknown YAML keys fail in `config.Load`. Further HTTP, authentication and provider checks occur during startup, so successfully loading YAML alone does not guarantee a usable deployment. See [Configuration](configuration.md), [Human approval](approval.md) and [Deployment](deploy.md).
