# opsgate

[한국어](README.ko.md) · [Client setup](docs/clients.md) · [Security](SECURITY.md)

opsgate is an open-source Go MCP server for helping AI clients inspect a Linux server safely. Its security model is least privilege, explicit allowlists and deny by default: providers start disabled, unit and namespace allowlists start empty, and stdio uses the launching OS user's permissions. Run as an unprivileged user with narrow Kubernetes RBAC.

The v0.1 contract is read-only inspection, without shell, arbitrary execution or Kubernetes Secrets access. Calls go through a shared wrapper for scope checks, cooperative context deadlines, best-effort redaction, output truncation and JSONL auditing. Logs and events are untrusted data and can contain prompt injection; these controls cannot guarantee a client ignores malicious instructions.

**Development status:** v0.1 is implemented and pre-release, including host and Kubernetes providers, audit writing and redaction. It has not yet been security-reviewed by third parties. Review the implementation before production use. See the [roadmap](docs/roadmap.md) for future work.

## Quick start

Only stdio is supported. Host inspection requires Linux with systemd/journald and /proc; Kubernetes inspection uses client-go and suitably restricted credentials.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest
# Or build from a checkout (Go 1.25+):
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

## v0.1 tool contract

All tools require `ops:read` and are registered only when their provider is enabled. Host units and Kubernetes namespaces are filtered by configuration; namespace denies win. Kubernetes has no Secrets tool. Read-only metadata and logs can still disclose sensitive information.

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

For numeric limits, zero selects the default; other out-of-range values are clamped. All returned text is subject to `server.max_output_bytes` (default 65536) and calls have cooperative deadlines (`server.tool_timeout`, default 30s).

Kubernetes namespace inputs must be DNS-1123 labels; object `name`, `pod` and `container` inputs are validated as DNS-1123 subdomains.

Supported `kind` values for `k8s_list` and `k8s_get` are case-insensitive (aliases in parentheses): pods (po, pod), deployments (deploy, deployment), statefulsets (sts, statefulset), daemonsets (ds, daemonset), replicasets (rs, replicaset), jobs (job), cronjobs (cronjob), services (svc, service), ingresses (ing, ingress), configmaps (cm, configmap), persistentvolumeclaims (pvc, persistentvolumeclaim), persistentvolumes (pv, persistentvolume), nodes (no, node), namespaces (ns, namespace).

Nodes, PersistentVolumes and Namespaces are cluster-scoped: omit `namespace`. Namespace allow/deny rules filter Namespace objects by name, but do not restrict Nodes or PersistentVolumes, including nodes shown by `k8s_overview`. Namespaced resources and events are filtered by namespace; empty allowlists allow none. Use RBAC to constrain cluster-scoped access.

Use [read-only RBAC](deploy/kubernetes/rbac-readonly.yaml) as a starting point and review its effective permissions before deployment. It creates a ServiceAccount in the pre-existing `opsgate` namespace, a ClusterRole and a ClusterRoleBinding granting cluster-wide get/list/watch for supported resources, events and pod logs, excluding Secrets. Use namespace RoleBindings for narrower access.

Kubeconfig `exec` and `auth-provider` credential plugins are rejected; use credentials that do not require plugins, such as a short-lived ServiceAccount token. Protect kubeconfig ownership and permissions.

## Redaction and object sanitization

The shared `internal/redact` layer masks PEM private keys, Authorization/Proxy-Authorization headers, Basic/Bearer credentials, JWT-shaped tokens, URL passwords, and recognized token patterns for AWS access key IDs, GitHub, GitLab, Slack, Stripe live keys, OpenAI/Anthropic-style keys, Google API keys and Telegram bots. Sensitive `:`/`=` assignments and structured argument keys include password/passwd/pwd, secret/client_secret, token/access_token/refresh_token, api_key/apikey/access_key/secret_key/private_key, credential/credentials, auth_token, session_id, cookie and set-cookie, including underscore-prefixed variants such as `db_password` (key matching is case-insensitive). Structured argument maps and slices are traversed recursively. Success text, errors and audit arguments are redacted. This is **best effort**: unfamiliar formats and secrets embedded in arbitrary data can escape.

`k8s_get` additionally removes `managedFields` and `kubectl.kubernetes.io/last-applied-configuration` from metadata, including templates. ConfigMap `data` and `binaryData` retain keys but replace values with byte-count placeholders unless `show_configmap_data=true`. Literal environment `value` fields are replaced with `[REDACTED]` when the variable name contains pass, secret, token, key, credential, auth, dsn, private, cert or database_url (case-insensitive). This applies to regular, init and ephemeral containers in Pods and supported workload templates, including CronJobs. Other literal values remain visible unless matched by shared redaction; `valueFrom` references remain visible and are not resolved.

## Configuration reference

Defaults below come from `config.Default()`. YAML is loaded over defaults with strict known-field checking; unknown keys fail. Parent mappings are `server`, `stdio`, `providers`, `providers.host` and `providers.kubernetes`. Empty slices are shown as `[]` (some are nil in Go).

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

Log limits are provider caps; keep them positive. Current config validation checks positive output size and timeout, and recognized scopes. `stdio.user` is a configured label, not an authentication credential. Granting `ops:write` does not implement write tools or confirmation.

## Audit format

The shared wrapper writes one JSON object per handled tool call, appended to `server.audit_log`, or stderr when empty. New files are created with mode 0600 (subject to umask); existing permissions are retained. Symlinks, non-regular files and group/world-writable files are rejected. Stdout is reserved for MCP messages.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"host_service_status","args":{"unit":"ssh.service"},"outcome":"ok","duration_ms":12,"output_bytes":120}
```

Fields of `audit.Event`: `time` (RFC3339 timestamp with optional fractional seconds), `user`, `transport`, `tool`, optional redacted `args`, `outcome` (`ok`, `error`, `denied`), optional redacted `error`, `duration_ms`, and `output_bytes` (returned success text size; zero on error). Tool output bodies are not stored. Audit write failures are reported to stderr without failing the tool call; audit is not tamper-proof. Protect and rotate logs, which may still contain sensitive information.

## Development and license

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [Apache-2.0](LICENSE), copyright 2026 The opsgate Authors.
