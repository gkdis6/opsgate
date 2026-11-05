# Tool reference

[한국어](ko/tools.md)

Kubernetes and note read tools require `ops:read`; dev tools require `dev:run`. Tools are registered only when their provider is enabled. Kubernetes namespaces are filtered by configuration; namespace denies win. Kubernetes has no Secrets tool. Read-only metadata and logs can still disclose sensitive information.

| Tool | Purpose | Main arguments, defaults and limits |
| --- | --- | --- |
| `k8s_clusters` | Configured clusters in order: name, context, protection, namespace/log/write policy and reachability | None; discovery timeout 2s per cluster |
| `k8s_overview` | Node health and allowed namespace workloads/events | `cluster` (see selection rules below) |
| `k8s_list` | List supported resources | Required `kind`; `namespace=""` (all allowed namespaces); `label_selector=""` (no filter), Kubernetes label selector syntax; `limit=100`, clamped to 1–500 |
| `k8s_get` | Sanitized object YAML and up to 50 related events from the last hour | Required `kind`, `name`; `namespace` required for namespaced kinds, omitted for cluster-scoped kinds |
| `k8s_env` | Container environment and mounted configuration, with value sources | Required `namespace`, `name`; `kind="pod"` (pod, deployment, statefulset, daemonset, replicaset, job, cronjob and their aliases); `container` defaults to all regular/init containers; ConfigMap values require `show_configmap_data=true`; mounted text capped at 8 KiB per key and 64 KiB per call |
| `k8s_service_check` | Service host name DNS configuration and ready backends; configuration check only (no DNS queries or connections, NetworkPolicies not evaluated) | Required `namespace` (client namespace), `host` (lowercase DNS labels, max 253 bytes; IP addresses rejected); optional `port` (Service port number or name), `pod` (DNS settings in the client namespace). Assumes `cluster.local`; accepts explicit Service names with other domains. Shows resolver candidates, Service, up to 500 selector pods and EndpointSlices; suggests same-name Services in allowed namespaces (up to 10). |
| `k8s_logs` | Pod logs; registered if any cluster has `allow_logs=true` | Required `namespace`, `pod`; `container` may be omitted only if exactly one regular/init/ephemeral container exists; `tail=200`, clamped to 1–`max_log_lines` (default cap 500); `since=""` (no time filter), positive Go duration up to 168h; `previous=false`; `grep=""`, literal case-insensitive substring; stream capped at 1 MiB |
| `k8s_events` | Namespace events | `namespace=""` (all allowed namespaces); `warnings_only=true`; `since="1h"`, positive Go duration up to 168h; `limit=50`, clamped to 1–500 |
| `k8s_rollout_restart` | Restart a workload by updating its pod template | Required `kind` (deployment/statefulset/daemonset), `namespace`, `name`; `rollout_restart` gate |
| `k8s_scale` | Set workload replicas | Required `kind` (deployment/statefulset), `namespace`, `name`, `replicas` (0–`max_replicas`, default cap 10); `scale` gate |
| `k8s_delete_pod` | Delete a specific pod with UID precondition | Required `namespace`, `name`; `delete_pod` gate |
| `note_search` | Literal note content/filename search with snippets and scanned counts | Required `query` (1–4096 UTF-8 bytes); optional `folder`, `tag`, `case_sensitive=false`; `limit=20`, max 100 |
| `note_read` | Note text and file/line metadata | Required `path`, `.md` optional; `offset_line=1`, `max_lines=400`, max 2000 |
| `note_list` | Folders first, then .md/.canvas/.base files | `folder=""` (vault root), `recursive=false`, `limit=200`, max 1000; reports truncation |
| `note_recent` | Notes ordered by modification time | `limit=20`, max 100; `days=7`, max 365 |
| `note_links` | Outgoing wikilinks/embeds/relative Markdown links and backlinks | Required `path`; exact path or unique basename resolution; reports unresolved targets |
| `note_tags` | Frontmatter/inline tag counts or matching notes | Optional `tag`; includes nested tags; ignores fenced code and headings |
| `note_create` | Create a note and missing parent folders | Required `path`, `content`; `allow_write=true`, ops:write and human approval required |
| `note_append` | Append at end or under a unique exact heading | Required `path`, `content`; optional `heading` (at most 1024 bytes); same write gate |
| `note_replace` | Exact substring replacement | Required `path`, nonempty `old_text`, `new_text`; `expected_count=1` must match; same write gate |
| `note_trash` | Retain a note in local .trash with collision suffixes | Required `path`; same write gate; never permanently deletes |
| `dev_projects` | Coding projects and agent allowlists (`dev:run`) | None; includes runner limits |
| `dev_task_start` | Start an agent after human approval (`dev:run`) | Required `project`, `agent`, `prompt`; optional `approval_id`; prompt up to 32 KiB (MCP arguments 48 KiB, approval summary 64 KiB) |
| `dev_task_status` | Own task state and final message (runner admins may access all tasks) (`dev:run`) | Required `id` |
| `dev_task_log` | Untrusted task log page (`dev:run`) | Required `id`; `offset=0` (0–20 MiB), `limit=65536`, maximum 262144 bytes; raw `next_offset` |
| `dev_task_diff` | Untrusted stat and patch against pinned base (`dev:run`) | Required `id`; maximum 256 KiB before MCP output cap |
| `dev_task_list` | Retained tasks newest first (`dev:run`) | `limit=20`, range 1..100; final messages omitted |
| `dev_task_cancel` | Cancel an owned task without new approval (runner admins may cancel any task) (`dev:run`) | Required `id`; changes state, terminates running process group |

Note search, tags and links operate on masked text.

Note paths use vault-relative forward slashes, preserve Unicode bytes, and are limited to 1024 bytes. Excluded paths behave as missing. Note text is untrusted data. Note numeric inputs outside the documented ranges are rejected. Safe in-vault symlinks are supported with cycle prevention. Scans are bounded to 100000 entries and 128 directory levels; links/backlinks each return at most 1000 results. Write previews include a diff (new content for create), current hash and resulting hash, with explicit truncation near 8 KiB. Apply rechecks the approved hash, size and modification time. Creates publish atomically without overwriting racing creators; updates use a synced temporary file and rename. On Linux, trash atomically moves the source to the approved .trash destination without overwriting an existing entry, then validates the moved note. If a replacement or edit raced with the move, the operation reports an error with the recovery path in .trash; restore it if needed. If the destination appeared after planning, the move fails and leaves the source intact. On Linux, the source and .trash must share a filesystem supporting renameat2 with RENAME_NOREPLACE; otherwise the move fails safely. On other platforms, trash retains a durable approved copy before checking and removing the source; an external replacement between that final check and removal can still be lost. Use versioned backups or sync history on those platforms. Existing trailing-newline policy is preserved. Each content/replacement input is capped at 256 KiB. The create, append and replace tools allow 528 KiB of raw JSON arguments, including escaping; trash retains the default 16 KiB limit. Updates use an optimistic state check followed by rename: an external edit between the check and rename can be overwritten. Use versioned backups or sync history. Large create previews are truncated too, with an explicit omitted-line count.

For Kubernetes read-tool numeric limits, zero selects the default; other out-of-range values are clamped. Write replica counts are validated, not clamped. All returned text is subject to `server.max_output_bytes` (default 65536) and calls have cooperative deadlines (`server.tool_timeout`, default 30s).

Kubernetes namespace inputs must be DNS-1123 labels; object `name`, `pod` and `container` inputs are validated as DNS-1123 subdomains.

Supported `kind` values for `k8s_list` and `k8s_get` are case-insensitive (aliases in parentheses): pods (po, pod), deployments (deploy, deployment), statefulsets (sts, statefulset), daemonsets (ds, daemonset), replicasets (rs, replicaset), jobs (job), cronjobs (cronjob), services (svc, service), ingresses (ing, ingress), configmaps (cm, configmap), persistentvolumeclaims (pvc, persistentvolumeclaim), persistentvolumes (pv, persistentvolume), nodes (no, node), namespaces (ns, namespace).

Nodes, PersistentVolumes and Namespaces are cluster-scoped: omit `namespace`. Namespace allow/deny rules filter Namespace objects by name, but do not restrict Nodes or PersistentVolumes, including nodes shown by `k8s_overview`. Namespaced resources and events are filtered by namespace; empty allowlists allow none. Use RBAC to constrain cluster-scoped access.

Use [read-only RBAC](../deploy/kubernetes/rbac-readonly.yaml) as a starting point and review its effective permissions before deployment. It creates a ServiceAccount in the pre-existing `opsgate` namespace, a ClusterRole and a ClusterRoleBinding granting cluster-wide get/list/watch for supported resources, events, pod logs and `discovery.k8s.io` EndpointSlices, excluding Secrets. Use namespace RoleBindings for narrower access.

Kubeconfig `exec` and `auth-provider` credential plugins are rejected; use credentials that do not require plugins, such as a short-lived ServiceAccount token. Protect kubeconfig ownership and permissions.

Every Kubernetes tool except `k8s_clusters` accepts `cluster`. It is required with several configured clusters and optional with one. Unknown names and missing required names are denied with the configured names listed. `k8s_overview` also selects one cluster; it does not fan out. Call `k8s_clusters` to choose a target and state its name to the user before writing; never infer it from earlier output.

Every Kubernetes tool's output starts with `cluster: <name>`. `k8s_clusters` uses this line for each cluster and never exposes kubeconfig paths, server URLs or credentials. Discovery failures use sanitized diagnostics to avoid leaking connection details. An unreachable cluster does not fail the other results. Log and write tools are registered if any cluster enables them; calls to a cluster that disables them are denied. Namespace rules, ConfigMap visibility, log limits, write allowlists and replica limits all come from the selected cluster. Protected clusters require fresh human approval for every write.

## Redaction and object sanitization

For `k8s_logs`, grep matches masked text.

The shared `internal/redact` layer masks PEM private keys, Authorization/Proxy-Authorization headers, Basic/Bearer credentials, JWT-shaped tokens, URL passwords, and recognized token patterns for AWS access key IDs, GitHub, GitLab, Slack, Stripe live keys, OpenAI/Anthropic-style keys, Google API keys, Telegram bots, and opsgate `og_at_`, `og_rt_`, and `og_st_` tokens (43 base64url characters after the prefix). Sensitive `:`/`=` assignments and structured argument keys include password/passwd/pwd, secret/client_secret, token/access_token/refresh_token, api_key/apikey/access_key/secret_key/private_key, credential/credentials, auth_token, session_id, cookie, set-cookie and invite, including underscore-prefixed variants such as `db_password` (key matching is case-insensitive). Structured argument maps and slices are traversed recursively. Success text, errors and audit arguments are redacted. This is **best effort**: unfamiliar formats and secrets embedded in arbitrary data can escape.

`k8s_get` additionally removes `managedFields` and `kubectl.kubernetes.io/last-applied-configuration` from metadata, including templates. ConfigMap `data` and `binaryData` retain keys but replace values with byte-count placeholders unless `show_configmap_data=true`. Literal environment `value` fields are replaced with `[REDACTED]` when the variable name contains pass, secret, token, key, credential, auth, dsn, private, cert or database_url (case-insensitive). This applies to regular, init and ephemeral containers in Pods and supported workload templates, including CronJobs. Other literal values remain visible unless matched by shared redaction; `valueFrom` references remain visible and are not resolved.

`k8s_env` resolves same-namespace ConfigMaps once per call, applies environment overrides and single-pass `$(VAR)` expansion, and shows declared mount items and `subPath` files. Sensitive environment/key names and Secret values stay hidden; Secret APIs are never called, so Secret `envFrom` keys cannot be enumerated and possible collisions are marked. Pod downward fields are resolved; workload fields and resource references remain placeholders. Binary files show sizes only, `subPathExpr` is reported without resolution, and files baked into images are not visible.

`k8s_env` masks downward API label and annotation values before resolution or expansion when their keys match the `k8s_get` sensitive-key rule, and `k8s_list` denies label selectors on keys matching that same rule.

## Shared limits

All eight approval-gated tools accept optional `approval_id` for browser retries; omit it on the first call. Dev task IDs are 20 lowercase base32 characters. Task status, log, diff, list and cancellation enforce requester ownership, with runner admins permitted access to all tasks.

Read tools and `dev_task_cancel` accept up to 16 KiB of raw JSON arguments. Write arguments default to 16 KiB; `note_create`, `note_append` and `note_replace` allow 528 KiB, and `dev_task_start` allows 48 KiB. JSON escaping counts toward these limits. Approval summaries are bounded by `max(64 KiB, argument limit + 16 KiB)`: 64 KiB for default tools and dev starts, 544 KiB for note content tools. Redacted display arguments must fit the tool's argument limit. Note previews have a separate approximately 8 KiB display bound.

The shared wrapper omits provider output above 4 MiB entirely, then redacts text before truncating it to `server.max_output_bytes` (default 64 KiB). Errors follow the same path. Smaller provider-specific collection limits still apply. Deadlines are cooperative; write planning and applying each receive a separate `server.tool_timeout` (default 30s), excluding human waiting time.
