# Configuration reference

[한국어](ko/configuration.md)

Defaults below come from `config.Default()`; a `mode` changes some of them (see [Modes](#modes-and-opsgate-init)). YAML is loaded over defaults with strict known-field checking; unknown keys fail. HTTP, authentication and approval settings are included below. Empty slices are shown as `[]` (some are nil in Go).

## Modes and `opsgate init`

`mode` names where opsgate runs as a service and fills in defaults for it.
Values written in the file always win. Without a mode the defaults below apply
and the transport defaults to stdio.

| Key | `mode: host` (systemd service) | `mode: kubernetes` (pod) |
| --- | --- | --- |
| `-transport` default | `http` | `http` |
| `http.listen` | `127.0.0.1:8787` | `0.0.0.0:8787` |
| `auth.mode` | `builtin` | `builtin` |
| `auth.builtin.upstream.provider` | `passkey` | `passkey` |
| `auth.builtin.state_file` | `/var/lib/opsgate/oauth.json` | `/var/lib/opsgate/oauth.json` |
| `approval.web` | `true` while `auth.mode` is builtin | `true` while `auth.mode` is builtin |
| `server.audit_log` | `/var/log/opsgate/audit.jsonl` | `""` (stderr, for the cluster's log collection) |
| `providers.host.enabled` | `true` | `false`; enabling it is an error because a pod sees its container, not the node |
| `providers.kubernetes.enabled` | `false` | `true`, with the pod's ServiceAccount (`kubeconfig: ""`) |

Namespaces, units, users and write targets are never defaulted.
`opsgate init` asks for them, plus the mode, public URL and sign-in (passkey,
Google or GitHub), and writes a short mode-based file with mode 0600. It refuses
to overwrite a file without `-force`. Every question has a flag (`opsgate init
-h`); without a terminal, or with `-yes`, unanswered questions take their
defaults, and the public URL and user must be given as flags. The result is
checked as the HTTP server would check it before it is written.

`opsgate config show -config <file>` prints the effective configuration,
mode defaults included, as YAML that opsgate loads unchanged.

| YAML key | Default | Meaning |
| --- | --- | --- |
| `mode` | `""` | Empty, `host` or `kubernetes`; see above |
| `server.name` | `opsgate` | MCP server name |
| `server.audit_log` | `""` | Empty writes to stderr; otherwise JSONL path |
| `server.max_output_bytes` | `65536` | Tool output byte limit; must be positive |
| `server.tool_timeout` | `30s` | Cooperative call timeout; separate planning/applying deadlines for writes; positive |
| `stdio.user` | `local` | Audit identity label; OS user is the trust boundary |
| `stdio.scopes` | `[ops:read]` | Accepted scopes: ops:read, ops:write, dev:run; write does not enable actions |
| `providers.host.enabled` | `false` | Enable host provider |
| `providers.host.units` | `[]` | Unit glob allowlist; empty allows none |
| `providers.host.disk_paths` | `[/]` | Mount points for overview |
| `providers.host.max_log_lines` | `500` | Journal lines per call cap |
| `providers.kubernetes.enabled` | `false` | Enable Kubernetes provider |
| `providers.kubernetes.clusters` | omitted | Ordered connection/policy entries; mutually exclusive with single-cluster fields |
| `providers.kubernetes.name` / `clusters[].name` | `default` / required | Name clients select with the cluster argument |
| `providers.kubernetes.protected` / `clusters[].protected` | `false` | Require fresh approval on every write; no approval windows |
| `providers.kubernetes.kubeconfig` | `""` | Empty: in-cluster, then $KUBECONFIG, then ~/.kube/config |
| `providers.kubernetes.context` | `""` | Context override; empty uses selected context |
| `providers.kubernetes.namespaces` | `[]` | Namespace glob allowlist; empty allows none; * allows all |
| `providers.kubernetes.deny_namespaces` | `[]` | Deny globs override allow globs |
| `providers.kubernetes.allow_logs` | `false` | Enable pod logs |
| `providers.kubernetes.show_configmap_data` | `false` | False exposes keys only, hides values |
| `providers.kubernetes.max_log_lines` | `500` | Pod log lines per call cap |
| `http.listen` | `127.0.0.1:8787` | Backend TCP address |
| `http.public_url` | `""` | Required HTTP origin; HTTPS except loopback HTTP; resource is origin + /mcp |
| `auth.mode` | `""` | Required for HTTP: builtin, external or static |
| `auth.builtin.state_file` | `""` | Optional 0600 persistence for clients and token hashes; empty loses state on restart |
| `auth.builtin.users` | `[]` | Allowed upstream users; empty denies everyone |
| `auth.builtin.users[].subject` | `""` | passkey:name, github:login, email:verified-email or oidc:sub (exact OIDC subject) |
| `auth.builtin.users[].scopes` | `[]` | Grants: ops:read, ops:write, dev:run |
| `auth.builtin.upstream.provider` | `""` | passkey (no identity provider; needs no client ID or secret), github or oidc |
| `auth.builtin.upstream.issuer` | `""` | Required OIDC issuer, e.g. https://accounts.google.com |
| `auth.builtin.upstream.client_id` | `""` | Upstream application client ID |
| `auth.builtin.upstream.client_secret_file` | `""` | Secret file; use this or client_secret_env |
| `auth.builtin.upstream.client_secret_env` | `""` | Environment variable name containing the secret |
| `auth.builtin.access_token_ttl` | `1h` | Access token lifetime |
| `auth.builtin.refresh_token_ttl` | `720h` | Refresh token lifetime |
| `auth.builtin.max_clients` | `100` | Dynamic Client Registration capacity |
| `auth.external.issuer` | `""` | Trusted issuer for discovery and JWKS |
| `auth.external.audience` | `""` | Empty resolves to public_url + /mcp |
| `auth.external.user_claim` | `sub` | String claim identifying the user |
| `auth.external.users` | `[]` | Empty trusts token scopes; otherwise restrict users and intersect token/granted scopes |
| `auth.external.users[].subject` | `""` | claim:value for the configured user_claim |
| `auth.external.users[].scopes` | `[]` | Maximum permitted scopes for this identity |
| `auth.static` | `[]` | Configured bearer tokens; no OAuth login in this mode |
| `auth.static[].name` | `""` | Token identity/audit label |
| `auth.static[].sha256` | `""` | Hex SHA-256 digest, never the raw token |
| `auth.static[].scopes` | `[]` | Token grants; include ops:read for HTTP access |
| `approval.ttl` | `10m` | Pending approval/input lifetime |
| `approval.web` | `false` | Browser fallback; requires builtin authentication |
| `approval.require_web` | `false` | Require browser approval; requires `approval.web`; denies stdio writes |
| `approval.window` | `0s` | Optional user/tool auto-approval lifetime; 1m–12h when enabled |
| `approval.window_tools` | `[]` | `path.Match` tool-name globs eligible for windows; empty allows none |
| `providers.host.restart_units` | `[]` | Separate restart unit glob allowlist; empty disables tool |
| `providers.host.command_prefix` | `[]` | Write argv prefix, e.g. [/usr/bin/sudo, -n]; first element absolute executable, remaining paths/flags |
| `providers.kubernetes.rollout_restart` | `[]` | namespace/name globs; empty disables tool |
| `providers.kubernetes.scale` | `[]` | namespace/name globs; empty disables tool |
| `providers.kubernetes.delete_pod` | `[]` | namespace/name globs; empty disables tool |
| `providers.kubernetes.max_replicas` | `10` | Scale maximum; nonnegative, zero permits only zero replicas |
| `providers.note.enabled` | `false` | Enable the note provider for a Markdown folder or Obsidian vault; no note app or CLI needed |
| `providers.note.vault` | `""` | Absolute existing vault directory; all access confined with os.Root |
| `providers.note.exclude` | `[]` | Additional vault-relative path.Match globs matching a path or any parent; dot-prefixed segments and sync-conflict copies always excluded |
| `providers.note.allow_write` | `false` | Register four note write tools; each requires ops:write and human approval |
| `providers.note.max_note_bytes` | `1048576` | Positive per-note size limit for reads/writes; oversized notes skipped in content scans |
| `providers.dev.enabled` | `false` | Enable dev provider; all tools require dev:run |
| `providers.dev.runner_socket` | `""` | Absolute Unix socket path of the separate dev-runner |
| `server.tools.allow` / `server.tools.deny` | `[]` / `[]` | Tool-name `path.Match` globs; empty allow permits all offered tools, deny wins; excluded tools are absent from tools/list |
| `auth.builtin.users[].tools` | `[]` | Optional tool-name globs narrowing a user's calls; empty adds no limit |
| `auth.external.users[].tools` | `[]` | Optional tool-name globs narrowing a mapped user's calls; empty adds no limit |
| `auth.static[].tools` | `[]` | Optional tool-name globs narrowing this token's calls; empty adds no limit |
| `auth.builtin.allowed_redirect_hosts` | `[]` | Exact redirect hostnames (no ports); localhost covers loopback IPs/any port; supported private schemes use e.g. cursor:; empty accepts any otherwise valid redirect host |
| `auth.builtin.unknown_redirect_hosts` | `""` | With a host list: empty/deny rejects unlisted destinations; approve holds clients pending admin review |
| `auth.builtin.admins` | `[]` | Allowed upstream subjects who manage clients at /admin/clients; must be in users; required for approve mode |
| `auth.builtin.notify_url` | `""` | Optional JSON POST for pending registrations; HTTPS or loopback HTTP, no URL credentials/fragments |

Server policy patterns must match a tool offered by an enabled provider after its action gates, or startup fails. Per-user/token tool patterns are syntax-checked and enforced on calls; they do not filter tools/list. Scopes, server policy, per-user tool policy and provider resource allowlists all apply. Empty tool allowlists add no restriction, unlike empty provider resource allowlists. Configuration changes require a restart.

All providers are disabled by default. Enabling the host provider exposes overview/process tools even with no allowed units; enabling Kubernetes permits cluster-scoped reads subject to RBAC. `ops:read` covers all enabled read providers, including the optional experimental note provider. `dev:run` covers dev tools, with HTTP additionally requiring `ops:read`. See [the trust model](../SECURITY.md).

Note writes run as the server account, so notes it creates or rewrites are owned by that account. Grant it access with ACLs rather than ownership, and keep the vault owner able to replace those files (write access to their directories). A sync tool running as the vault owner cannot chmod files it does not own: in Syncthing, set the vault folder's Ignore Permissions option, or permission-only changes from other devices fail with "operation not permitted".

See [the complete example](../opsgate.example.yaml) for a read-only starting point and separate commented write opt-ins. The [tool reference](tools.md#shared-limits) describes argument, approval and output limits.

## Kubernetes clusters

The existing single-cluster form remains valid: put `kubeconfig`, `context` and policy fields directly under `providers.kubernetes`. Its `name` defaults to `default`; `protected` defaults to `false`. For several clusters, use `clusters` instead. Each entry takes the same connection and policy fields and requires a unique `name` matching `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$` (1–63 characters). Do not combine `clusters` with single-cluster fields, apart from numeric defaults. An enabled provider rejects `clusters: []`. In `clusters` entries, an omitted or zero `max_log_lines` or `max_replicas` means 500 or 10; leave `scale` empty to disable scaling on a cluster.

```yaml
providers:
  kubernetes:
    enabled: true
    clusters:
      - name: kind
        kubeconfig: /etc/opsgate/kind.yaml
        context: kind-kind
        namespaces: [apps]
        allow_logs: true
        rollout_restart: [apps/web]
        scale: [apps/web]
        max_replicas: 3
      - name: prod
        kubeconfig: /etc/opsgate/prod.yaml
        protected: true
        namespaces: [apps]
        deny_namespaces: [kube-system]
        allow_logs: true
        # Empty write allowlists keep production read-only.
```

`protected: true` requires fresh human approval for every write, even if the user has an approval window. Policy settings apply only to their own cluster. Logs and write tools appear if any cluster enables them; calls targeting a cluster that disables them are denied. With several clusters, pass `cluster` on every call except `k8s_clusters`. With one cluster, it may be omitted, but an explicit name must match. Use `k8s_clusters` to review policy and reachability before choosing a target; state the target cluster to the user before a write.

### Managed clusters (EKS, GKE, AKS)

opsgate refuses kubeconfig credential plugins (`exec` and `auth-provider`, including `aws eks get-token`) because they can run programs from a kubeconfig. Use a dedicated ServiceAccount and a separate kubeconfig with a token. From an administrator context pointing at the intended cluster, apply the existing [read-only RBAC](../deploy/kubernetes/rbac-readonly.yaml). It grants cluster-wide reads without Secrets; use namespace RoleBindings for narrower access.

```sh
kubectl create namespace opsgate
kubectl apply -f deploy/kubernetes/rbac-readonly.yaml
umask 077
kubectl --kubeconfig=opsgate.kubeconfig config set-cluster managed \
  --server=https://API_ENDPOINT --certificate-authority=cluster-ca.crt --embed-certs=true
kubectl --kubeconfig=opsgate.kubeconfig config set-credentials opsgate \
  --token="$(kubectl -n opsgate create token opsgate --duration=24h)"
kubectl --kubeconfig=opsgate.kubeconfig config set-context managed \
  --cluster=managed --user=opsgate --namespace=opsgate
kubectl --kubeconfig=opsgate.kubeconfig config use-context managed
```

Replace `API_ENDPOINT` and `cluster-ca.crt` with the cluster's API endpoint and CA certificate from your administrator kubeconfig. Skip namespace creation if it already exists. Keep the new kubeconfig private (mode 0600) and point opsgate at it. The API server may shorten the requested token lifetime; rotate the token and restart opsgate before it expires. This kubeconfig contains no credential plugin and needs no cloud CLI at runtime.

## Authentication and human approval

The upstream application callback is exactly `<public_url>/oauth/callback`. See [deployment](deploy.md) for Host-preserving proxies and local probe rules.

`builtin` signs people in with passkeys or delegates login to GitHub or OIDC (including Google), then issues opaque MCP tokens. With passkeys, registration needs a one-time invite: opsgate logs one per user without a passkey at startup (24h), and `opsgate passkey invite -config <file> -subject <user> [-ttl 24h]` issues more; run it as the server account because invites are files next to the state file. It supports authorization code with PKCE S256 only, Dynamic Client Registration (DCR), per-client consent, refresh rotation and reuse revocation. The issuer is `http.public_url`; the resource is `<public_url>/mcp`. Optional state persistence retains client registrations and token hashes across restarts; protect its directory and backups, and use one process per state file. State reads and writes have a 512 MiB limit; an oversized write preserves the previous snapshot and makes the running instance fail closed.

Builtin OIDC users may match a verified `email:<address>` or an exact, case-sensitive immutable `oidc:<sub>` from the configured issuer. If both match, `oidc:<sub>` takes precedence and needs no email claim. Google email subjects require `gmail.com`/`googlemail.com`, or an `hd` claim matching the email domain (Google Workspace); `email_verified` alone is insufficient for other Google addresses. For the Google example in the README, use a Workspace address or configure `oidc:<sub>`.

OAuth GET routes reject request bodies; POST body reads have a 10-second deadline. Authorization errors stay on a local page before consent; Deny on the consent page may redirect. At DCR capacity, registrations older than 10 minutes that never issued tokens and have no outstanding authorization are evicted oldest first. Pending authorizations are limited to 20 per client and 1000 overall, evicting the oldest affected entry; browser approval logins have a separate pool of 100.

`external` validates JWT signatures using issuer discovery and JWKS, with issuer, audience, expiry and allowed algorithm checks. Issuer, discovery and JWKS URLs require HTTPS, except loopback HTTP. JWKS fetch attempts are shared across requests and limited to one per minute, including failed fetches. Cached valid keys continue to work; a new signing key may be rejected until the cooldown expires. Audience defaults to the MCP resource URL. Scopes come from `scope` or `scp`; an optional `claim:<value>` user allowlist further restricts identities and intersects their scopes. `static` accepts pre-shared bearer tokens configured by SHA-256 digest; generate a token and digest with `opsgate -gen-token`. Store the raw token only in the client's secret storage. Static mode provides no DCR or OAuth login.

Host, Kubernetes and note read tools require `ops:read`. Host/Kubernetes writes require their own action allowlist; note writes require `allow_write`. These operations require `ops:write`, underlying OS/RBAC permission and human confirmation. Dev tools instead require `dev:run`, and `dev_task_start` requires human confirmation. HTTP access additionally requires `ops:read`, so remote writers need both scopes. Namespace allow/deny rules still apply to Kubernetes writes. Read unit allowlists do not authorize restarts.

The server validates inputs and builds a side-effect-free plan before confirmation. Clients supporting MCP elicitation show an in-client approval prompt, including multi round-trip input requests across protocol versions. Otherwise, `approval.web: true` with builtin auth returns an approval link and `approval_id`. The human opens it, signs in upstream as the same user and reviews the exact operation. After approval, retry the same tool with exactly the same arguments plus `approval_id`. Approvals expire, are single-use and bind the user, tool, SHA-256 of canonical unredacted arguments (excluding `approval_id`) and plan. Writes are denied if redaction changes the summary, so the human sees an unambiguous target. Kubernetes plans include the target UID; execution uses that invocation’s plan and identity/version preconditions. Without a confirmation channel, writes are denied; stdio supports elicitation only.

Optional time-window approvals require both `approval.window` (1m–12h; `0s` disables the feature) and `approval.window_tools` (a `path.Match` glob allowlist; empty allows no tools). For example, `window: 30m` and `window_tools: [host_service_restart]` let a human choose **Approve + allow for 30m** in the browser, or the optional **Also auto-approve host_service_restart for 30m** checkbox in elicitation. The grant covers that exact user and tool, including later calls from another client or with different arguments or targets. Do not enable windows for `dev_task_start`; each task should receive a separate review. It does not grant scopes, bypass action allowlists, skip input validation or skip planning. Grants live only in memory, expire without renewal on use, are capped at 100, and disappear on restart. Each call still has its own audit record with `approval: window`; grant creation emits a separate `window_granted` event with the source (`web` or `elicitation`), duration and expiry. With `require_web`, only the browser can obtain a grant and stdio writes remain denied.

Approval pages show the plan with diff highlighting, requester, transport, expiry and collapsed arguments. After deciding, return to the chat and ask the assistant to retry; there is no automatic redirect. Approval summaries allow up to `max(64 KiB, argument limit + 16 KiB)`. Raw JSON arguments default to 16 KiB; note content tools allow 528 KiB (256 KiB per content/replacement field), and `dev_task_start` allows 48 KiB for its 32 KiB prompt. JSON escaping counts toward the wire limit.

Set `approval.require_web: true` with `approval.web: true` to require browser approval even when the client supports elicitation; this denies writes over stdio. A compromised client UI can auto-approve elicitation. Prompt injection can still make a model request writes: humans must read the summary before approving. See [SECURITY.md](../SECURITY.md).

Client registration can be restricted with `allowed_redirect_hosts`. With a nonempty list, `unknown_redirect_hosts: deny` rejects other hosts; `approve` keeps such clients pending until an allowlisted administrator approves at `/admin/clients`. Pending clients cannot authorize. `notify_url` optionally receives a JSON POST containing event, client ID/name, redirect hosts and the admin URL; notifications are bounded and best effort. Client names are self-reported. Approval of a registration does not grant user scopes or skip consent. An empty host list accepts any otherwise valid redirect destination and ignores the unknown-host policy.

## Watch alerts

The `watch` section is read only by `opsgate watch`, a separate process; the MCP server ignores it. `opsgate watch` requires `providers.kubernetes.enabled` and `watch.webhook_url`.

| Key | Default | Meaning |
| --- | --- | --- |
| `webhook_url` | empty | https, or http to a loopback address; no credentials or fragment |
| `allow_private_http` | `false` | Also allow http to private receivers (Service names, private IPs); connections must reach private addresses |
| `secret_file` | empty | Optional key (at least 16 bytes) for the `X-Opsgate-Signature` HMAC |
| `clusters` | all | Configured cluster names to watch |
| `problems_only` | `false` | Send warnings only |
| `batch` | `30s` | How long alerts are gathered into one POST (1s–10m) |
| `cooldown` | `15m` | Repeat warnings for one object are counted, not sent (0–24h) |
| `max_alerts` | `50` | Alerts per POST (1–500) |

See [watch alerts](watch.md) for what is reported and the payload.

## Dev runner configuration

The runner uses a separate YAML file, never nested under `providers`. Its configuration is trusted and must be unwritable by the HTTP account. See [setup and risks](dev.md) and [the example](../deploy/dev-runner.example.yaml).

| Runner YAML key | Default | Meaning |
| --- | --- | --- |
| `admins` | `[]` | Exact requester subjects allowed to read/cancel every task; ordinary users access only their own |
| `socket` / `socket_group` | Required | Absolute Unix socket path and allowed peer group |
| `work_dir` | Required | Absolute private state/worktree directory |
| `projects` | `[]` | Allowlist of unique lowercase `name`, absolute `path`, and `base_ref` (default `HEAD`) |
| `agents` | `{}` | Agent names mapped to fixed `command` argv and optional `env` KEY=VALUE pairs; executable path must be absolute |
| `timeout` | `1h` | Positive task timeout |
| `max_concurrent` | `1` | FIFO worker count, 1..8 |
| `max_tasks` | `100` | Positive retained-task cap; remove oldest finished worktrees/records, keep branches |

## Audit format

The shared wrapper writes one JSON object per handled tool call, appended to `server.audit_log`, or stderr when empty. New files are created with mode 0600 (subject to umask); existing permissions are retained. Symlinks, non-regular files and group/world-writable files are rejected. Stdout is reserved for MCP messages.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"host_service_status","args":{"unit":"ssh.service"},"outcome":"ok","duration_ms":12,"output_bytes":120}
```

Fields of `audit.Event`: `time` (RFC3339 timestamp with optional fractional seconds), `user`, `transport`, `tool`, optional redacted `args`, `outcome` (`ok`, `error`, `denied`, `pending`), optional redacted `error`, `duration_ms`, and `output_bytes` (returned success text size; zero on error). Write events additionally include optional redacted `plan` (the summary shown to the human) and `approval` (the confirmation method, `elicitation`, `web` or `window`; may also be present on a refusal or pending call). `pending` means human input is outstanding. Browser retry instructions count in `output_bytes`; elicitation input requests do not return text output. Planning and applying each receive a separate tool timeout; human confirmation may take longer. Web decisions emit separate audit events with `outcome` `approved` or `rejected`, user, tool and only an eight-character `args.approval_id_prefix`. Denied multi round-trip approval verifications emit `denied` events without request state. Tool output bodies are not stored. Audit write failures are reported to stderr without failing the tool call; audit is not tamper-proof. Protect and rotate logs, which may still contain sensitive information.

Window creation emits `window_granted` with the source approval method and `args.window`/`args.expires_at`; later calls using the grant record `approval: window`.

