# opsgate

[한국어](README.ko.md) · [Client setup](docs/clients.md) · [Security](SECURITY.md)

opsgate is an open-source Go MCP server for helping AI clients inspect a Linux server safely. Its security model is least privilege, explicit allowlists and deny by default: providers start disabled, unit and namespace allowlists start empty, and stdio uses the launching OS user's permissions. Run as an unprivileged user with narrow Kubernetes RBAC.

The v0.1 contract is read-only inspection, without shell, arbitrary execution or Kubernetes Secrets access. Calls go through a shared wrapper for scope checks, cooperative context deadlines, best-effort redaction, output truncation and JSONL auditing. Logs and events are untrusted data and can contain prompt injection; these controls cannot guarantee a client ignores malicious instructions.

**Development status:** v0.1 is in progress. In this checkout provider registration, audit writing and redaction are TODO stubs. The tool table below describes the v0.1 contract, not working capabilities of these stubs. Do not rely on this snapshot for production security. See the [roadmap](docs/roadmap.md) for future work.

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

All tools require `ops:read`. Host units and Kubernetes namespaces are filtered by configuration; namespace denies win. ConfigMap values are hidden by default and sensitive environment values are intended to be redacted. Kubernetes has no Secrets tool. Read-only metadata and logs can still disclose sensitive information.

| Tool | Purpose |
| --- | --- |
| `host_overview` | Host metrics and configured disk paths |
| `host_services` | Allowlisted systemd units |
| `host_service_status` | Allowlisted unit status |
| `host_logs` | Allowlisted journald logs |
| `host_top_processes` | Process usage from /proc |
| `k8s_overview` | Allowed namespace overview |
| `k8s_list` | List supported resources |
| `k8s_get` | Inspect a supported resource; no Secrets |
| `k8s_logs` | Pod logs when enabled |
| `k8s_events` | Namespace events |

Use [read-only RBAC](deploy/kubernetes/rbac-readonly.yaml) as a starting point and review its effective permissions before deployment. The referenced manifest is absent from this checkout.

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

The audit contract is one JSON object per call, appended to `server.audit_log`, or stderr when empty. New files are intended to use mode 0600. Stdout is reserved for MCP messages. Audit writing is currently a stub in this checkout.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"host_overview","outcome":"ok","duration_ms":12,"output_bytes":120}
```

Fields: `time` (RFC3339 timestamp), `user`, `transport`, `tool`, optional redacted `args`, `outcome` (`ok`, `error`, `denied`), optional redacted `error`, `duration_ms`, and `output_bytes` (returned success text size; zero on error). Tool output bodies are not stored. Audit write failures are intended to be reported to stderr; audit is not tamper-proof. Protect and rotate logs, which may still contain sensitive information.

## Development and license

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [Apache-2.0](LICENSE), copyright 2026 The opsgate Authors.
