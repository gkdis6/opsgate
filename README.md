# opsgate

[한국어](README.ko.md) · [Client setup](docs/clients.md) · [Security](SECURITY.md)

opsgate is an open-source Go MCP server that lets AI clients inspect Linux and Kubernetes and request narrowly authorized operations. Providers start disabled, allowlists start empty, and writes require both explicit policy and human approval. Run with an unprivileged OS account and narrow Kubernetes RBAC. There is no shell or arbitrary command tool; internal commands use fixed argv with exec.CommandContext. Configuration and credentials must remain under trusted ownership.

Features include stdio and Streamable HTTP, Protected Resource Metadata, builtin OAuth 2.1 with GitHub/OIDC login, external JWT or static-token authentication, read tools, four approved write tools, redaction and JSONL auditing. Release infrastructure provides GoReleaser archives, cosign keyless signatures, SBOMs, provenance attestations, a container image and hardened systemd/Kubernetes deployment templates. See [deployment](docs/deploy.md).

**Development status:** stages 1–4 are implemented and pre-release; they have not yet been security-reviewed by third parties. Logs and events are attacker-controlled data and may contain prompt injection. Redaction is best effort.

## Stdio quick start

Host inspection requires Linux with systemd/journald and /proc. Kubernetes uses client-go and restricted credentials. Stdio trusts the launching OS account.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest
# Or build from a checkout (Go 1.26+):
make build
```

```yaml
stdio:
  scopes: ["ops:read"]
providers:
  host:
    enabled: true
    units: ["ssh.service", "nginx*"]
```

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

Save the YAML as `/path/opsgate.yaml` and replace binary/config paths with absolute paths. A source build writes `bin/opsgate`. Without a config, both providers are disabled. See [opsgate.example.yaml](opsgate.example.yaml) for every setting and [client setup](docs/clients.md) for Desktop, Cursor and SSH.

## HTTP quick start with Google

Create a Google OIDC web application and register **`https://mcp.example.com/oauth/callback`** as its exact redirect URI. Replace the origin, client ID and verified email below. Provision the secret file and a private writable state directory before starting.

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

Terminate TLS at a reverse proxy forwarding `/mcp`, `/.well-known/*`, `/oauth/*` and `/approve/*`. Connect claude.ai or ChatGPT with a custom OAuth connector pointing to `https://mcp.example.com/mcp`; DCR registers the client automatically. See [clients](docs/clients.md) and [deployment](docs/deploy.md).

## Tool contract

Read tools require `ops:read` and are registered only when their provider is enabled. Host units and Kubernetes namespaces are filtered by configuration; namespace denies win. Kubernetes has no Secrets tool. Read-only metadata and logs can still disclose sensitive information.

| Tool | Purpose | Main arguments, defaults and limits |
| --- | --- | --- |
| `host_overview` | Host metrics and configured disk paths | None |
| `host_services` | Allowlisted systemd units | None |
| `host_service_status` | Allowlisted unit status | Required `unit`; missing suffix becomes `.service`; normalized name at most 256 bytes |
| `host_logs` | Allowlisted journald logs | Required `unit` (same rules as status); `lines=100`, clamped to 1–`max_log_lines` (default cap 500); `since=""` (no time filter), positive duration including `d` (at most 128 bytes) or RFC3339; `priority=""` (no filter), emerg/alert/crit/err/warning/notice/info/debug or 0–7; `grep=""`, literal case-insensitive substring, at most 4096 bytes |
| `host_top_processes` | Process usage from /proc, sampled over 500ms; names only, no argv or environment | `sort="cpu"` (cpu or mem); `limit=15`, clamped to 1–50 |
| `k8s_overview` | Node health and allowed namespace workloads/events | None |
| `k8s_list` | List supported resources | Required `kind`; `namespace=""` (all allowed namespaces); `label_selector=""` (no filter), Kubernetes label selector syntax; `limit=100`, clamped to 1–500 |
| `k8s_get` | Sanitized object YAML and up to 50 related events from the last hour | Required `kind`, `name`; `namespace` required for namespaced kinds, omitted for cluster-scoped kinds |
| `k8s_logs` | Pod logs; registered only with `allow_logs=true` | Required `namespace`, `pod`; `container` may be omitted only if exactly one regular/init/ephemeral container exists; `tail=200`, clamped to 1–`max_log_lines` (default cap 500); `since=""` (no time filter), positive Go duration up to 168h; `previous=false`; `grep=""`, literal case-insensitive substring; stream capped at 1 MiB |
| `k8s_events` | Namespace events | `namespace=""` (all allowed namespaces); `warnings_only=true`; `since="1h"`, positive Go duration up to 168h; `limit=50`, clamped to 1–500 |
| `host_service_restart` | Restart an allowed systemd unit | Required `unit` (same normalization and 256-byte limit as status); `restart_units` gate |
| `k8s_rollout_restart` | Restart a workload by updating its pod template | Required `kind` (deployment/statefulset/daemonset), `namespace`, `name`; `rollout_restart` gate |
| `k8s_scale` | Set workload replicas | Required `kind` (deployment/statefulset), `namespace`, `name`, `replicas` (0–`max_replicas`, default cap 10); `scale` gate |
| `k8s_delete_pod` | Delete a specific pod with UID precondition | Required `namespace`, `name`; `delete_pod` gate |

For read-tool numeric limits, zero selects the default; other out-of-range values are clamped. Write replica counts are validated, not clamped. All returned text is subject to `server.max_output_bytes` (default 65536) and calls have cooperative deadlines (`server.tool_timeout`, default 30s).

Kubernetes namespace inputs must be DNS-1123 labels; object `name`, `pod` and `container` inputs are validated as DNS-1123 subdomains.

Supported `kind` values for `k8s_list` and `k8s_get` are case-insensitive (aliases in parentheses): pods (po, pod), deployments (deploy, deployment), statefulsets (sts, statefulset), daemonsets (ds, daemonset), replicasets (rs, replicaset), jobs (job), cronjobs (cronjob), services (svc, service), ingresses (ing, ingress), configmaps (cm, configmap), persistentvolumeclaims (pvc, persistentvolumeclaim), persistentvolumes (pv, persistentvolume), nodes (no, node), namespaces (ns, namespace).

Nodes, PersistentVolumes and Namespaces are cluster-scoped: omit `namespace`. Namespace allow/deny rules filter Namespace objects by name, but do not restrict Nodes or PersistentVolumes, including nodes shown by `k8s_overview`. Namespaced resources and events are filtered by namespace; empty allowlists allow none. Use RBAC to constrain cluster-scoped access.

Use [read-only RBAC](deploy/kubernetes/rbac-readonly.yaml) as a starting point and review its effective permissions before deployment. It creates a ServiceAccount in the pre-existing `opsgate` namespace, a ClusterRole and a ClusterRoleBinding granting cluster-wide get/list/watch for supported resources, events and pod logs, excluding Secrets. Use namespace RoleBindings for narrower access.

Kubeconfig `exec` and `auth-provider` credential plugins are rejected; use credentials that do not require plugins, such as a short-lived ServiceAccount token. Protect kubeconfig ownership and permissions.

## Redaction and object sanitization

The shared `internal/redact` layer masks PEM private keys, Authorization/Proxy-Authorization headers, Basic/Bearer credentials, JWT-shaped tokens, URL passwords, and recognized token patterns for AWS access key IDs, GitHub, GitLab, Slack, Stripe live keys, OpenAI/Anthropic-style keys, Google API keys and Telegram bots. Sensitive `:`/`=` assignments and structured argument keys include password/passwd/pwd, secret/client_secret, token/access_token/refresh_token, api_key/apikey/access_key/secret_key/private_key, credential/credentials, auth_token, session_id, cookie and set-cookie, including underscore-prefixed variants such as `db_password` (key matching is case-insensitive). Structured argument maps and slices are traversed recursively. Success text, errors and audit arguments are redacted. This is **best effort**: unfamiliar formats and secrets embedded in arbitrary data can escape.

`k8s_get` additionally removes `managedFields` and `kubectl.kubernetes.io/last-applied-configuration` from metadata, including templates. ConfigMap `data` and `binaryData` retain keys but replace values with byte-count placeholders unless `show_configmap_data=true`. Literal environment `value` fields are replaced with `[REDACTED]` when the variable name contains pass, secret, token, key, credential, auth, dsn, private, cert or database_url (case-insensitive). This applies to regular, init and ephemeral containers in Pods and supported workload templates, including CronJobs. Other literal values remain visible unless matched by shared redaction; `valueFrom` references remain visible and are not resolved.

## Authentication and human approval

`builtin` delegates login to GitHub or OIDC (including Google), then issues opaque MCP tokens. It supports authorization code with PKCE S256 only, Dynamic Client Registration (DCR), per-client consent, refresh rotation and reuse revocation. The issuer is `http.public_url`; the resource is `<public_url>/mcp`. Optional state persistence retains client registrations and token hashes across restarts; protect its directory and backups, and use one process per state file.

Builtin OIDC users may match a verified `email:<address>` or an exact, case-sensitive immutable `oidc:<sub>` from the configured issuer. If both match, `oidc:<sub>` takes precedence and needs no email claim. Google email subjects require `gmail.com`/`googlemail.com`, or an `hd` claim matching the email domain (Google Workspace); `email_verified` alone is insufficient for other Google addresses. For the Google example above, use a Workspace address or configure `oidc:<sub>`.

OAuth GET routes reject request bodies; POST body reads have a 10-second deadline. Authorization errors stay on a local page before consent; Deny on the consent page may redirect. At DCR capacity, registrations older than 10 minutes that never issued tokens and have no outstanding authorization are evicted oldest first. Pending authorizations are limited to 20 per client and 1000 overall, evicting the oldest affected entry; browser approval logins have a separate pool of 100.

`external` validates JWT signatures using issuer discovery and JWKS, with issuer, audience, expiry and allowed algorithm checks. Issuer, discovery and JWKS URLs require HTTPS, except loopback HTTP. Audience defaults to the MCP resource URL. Scopes come from `scope` or `scp`; an optional `claim:<value>` user allowlist further restricts identities and intersects their scopes. `static` accepts pre-shared bearer tokens configured by SHA-256 digest; generate a token and digest with `opsgate -gen-token`. Store the raw token only in the client's secret storage. Static mode provides no DCR or OAuth login.

Read tools require `ops:read`. Writes require their own action allowlist, `ops:write`, underlying OS/RBAC permission and human confirmation. HTTP access additionally requires `ops:read`, so remote writers need both scopes. Namespace allow/deny rules still apply to Kubernetes writes. Read unit allowlists do not authorize restarts.

The server validates inputs and builds a side-effect-free plan before confirmation. Clients supporting MCP elicitation show an in-client approval prompt, including multi round-trip input requests across protocol versions. Otherwise, `approval.web: true` with builtin auth returns an approval link and `approval_id`. The human opens it, signs in upstream as the same user and reviews the exact operation. After approval, retry the same tool with exactly the same arguments plus `approval_id`. Approvals expire, are single-use and bind the user, tool, arguments and plan. Without a confirmation channel, writes are denied; stdio supports elicitation only.

A compromised client UI can auto-approve elicitation. Prompt injection can still make a model request writes: humans must read the summary before approving. See [SECURITY.md](SECURITY.md).

## Configuration reference

Defaults below come from `config.Default()`. YAML is loaded over defaults with strict known-field checking; unknown keys fail. HTTP, authentication and approval settings are included below. Empty slices are shown as `[]` (some are nil in Go).

| YAML key | Default | Meaning |
| --- | --- | --- |
| `server.name` | `opsgate` | MCP server name |
| `server.audit_log` | `""` | Empty writes to stderr; otherwise JSONL path |
| `server.max_output_bytes` | `65536` | Tool output byte limit; must be positive |
| `server.tool_timeout` | `30s` | Per-call timeout; must be positive |
| `stdio.user` | `local` | Audit identity label; OS user is the trust boundary |
| `stdio.scopes` | `[ops:read]` | Accepted scopes: ops:read, ops:write; write does not enable actions |
| `providers.host.enabled` | `false` | Enable host provider |
| `providers.host.units` | `[]` | Unit glob allowlist; empty allows none |
| `providers.host.disk_paths` | `[/]` | Mount points for overview |
| `providers.host.max_log_lines` | `500` | Journal lines per call cap |
| `providers.kubernetes.enabled` | `false` | Enable Kubernetes provider |
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
| `auth.builtin.users[].subject` | `""` | github:login, email:verified-email or oidc:sub (exact OIDC subject) |
| `auth.builtin.users[].scopes` | `[]` | Grants: ops:read and optionally ops:write |
| `auth.builtin.upstream.provider` | `""` | github or oidc |
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
| `approval.ttl` | `10m` | Pending browser approval lifetime |
| `approval.web` | `false` | Browser fallback; requires builtin authentication |
| `providers.host.restart_units` | `[]` | Separate restart unit glob allowlist; empty disables tool |
| `providers.host.command_prefix` | `[]` | Write argv prefix, e.g. [/usr/bin/sudo, -n]; first element absolute executable, remaining paths/flags |
| `providers.kubernetes.rollout_restart` | `[]` | namespace/name globs; empty disables tool |
| `providers.kubernetes.scale` | `[]` | namespace/name globs; empty disables tool |
| `providers.kubernetes.delete_pod` | `[]` | namespace/name globs; empty disables tool |
| `providers.kubernetes.max_replicas` | `10` | Scale maximum; nonnegative, zero permits only zero replicas |

Log limits are provider caps; keep them positive. All four write tools accept optional `approval_id` for the browser retry. Write tools are registered only with nonempty action allowlists. Configuration changes require a restart.

## Audit format

The shared wrapper writes one JSON object per handled tool call, appended to `server.audit_log`, or stderr when empty. New files are created with mode 0600 (subject to umask); existing permissions are retained. Symlinks, non-regular files and group/world-writable files are rejected. Stdout is reserved for MCP messages.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"host_service_status","args":{"unit":"ssh.service"},"outcome":"ok","duration_ms":12,"output_bytes":120}
```

Fields of `audit.Event`: `time` (RFC3339 timestamp with optional fractional seconds), `user`, `transport`, `tool`, optional redacted `args`, `outcome` (`ok`, `error`, `denied`, `pending`), optional redacted `error`, `duration_ms`, and `output_bytes` (returned success text size; zero on error). Write events additionally include optional redacted `plan` (the summary shown to the human) and `approval` (the confirmation method, `elicitation` or `web`; may also be present on a refusal or pending call). `pending` means browser approval is outstanding; its returned instruction text is counted in `output_bytes`. Planning and applying each receive a separate tool timeout; human confirmation may take longer. Tool output bodies are not stored. Audit write failures are reported to stderr without failing the tool call; audit is not tamper-proof. Protect and rotate logs, which may still contain sensitive information.

## Development and license

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [Apache-2.0](LICENSE), copyright 2026 The opsgate Authors.
