# Tool reference

[한국어](ko/tools.md)

Kubernetes and note read tools require `ops:read`. Tools are registered only when their provider is enabled. Kubernetes namespaces are filtered by configuration; namespace denies win. Kubernetes has no Secrets tool. Read-only metadata and logs can still disclose sensitive information.

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
| `action_link` | Read-only: validate a catalog action with its current Plan and return approval URL, label and summary; never applies | Required `tool`, `arguments` object; optional `source` (`ai` default, or `rule`); `ops:read`; registered when an enabled catalog action and per-cluster `action_approval_url` exist |
| `k8s_remediation_options` | Read-only current-state catalog choices for image pull failures, failing/stalled rollouts, repeated restarts/OOM and stuck/unreachable controller-owned pods; fixed English/Korean reasons, `source=rule` | Required `namespace`, `name` (Pod); optional `workload=kind/name` hint for a gone Pod. Only enabled, allowlisted actions with `action_approval_url`; up to 5 image choices plus restart/delete (7 total). Empty `actions` plus `hint` when none fit; [rules](approval.md#live-choices-per-failure). |
| `k8s_rollout_restart` | Restart a workload by updating its pod template | Required `kind` (deployment/statefulset/daemonset), `namespace`, `name`; `rollout_restart` gate |
| `k8s_scale` | Set workload replicas | Required `kind` (deployment/statefulset), `namespace`, `name`, `replicas` (0–`max_replicas`, default cap 10); `scale` gate; the result offers an undo link back to the previous count ([undo](approval.md#undo)) |
| `k8s_delete_pod` | Delete a specific pod with UID precondition | Required `namespace`, `name`; `delete_pod` gate; links require a controller owner; MCP warns for an unowned pod |
| `k8s_restore_image` | Restore a Deployment container's image observed in a ready/running container of a ready Pod owned by a non-deleting ReplicaSet; only the image changes and configuration is not rolled back | Required `namespace`, `name` (Deployment), `container`, `expected_image` (exact current image), `image` (exact source image); optional `revert: true` accepts an image from this Deployment's ReplicaSet history that is not verified healthy (used by undo); `restore_image` gate; rechecked before patching; no approval window; the result offers an undo link ([undo](approval.md#undo)) |
| `host_units` | Host provider: state of allowlisted systemd units | None; see [host provider](host.md) |
| `host_unit_logs` | Host provider: masked journal tail of one unit | Required `unit`; `lines=100`, max 500 |
| `host_restart_unit` | Host provider: restart one systemd unit through systemd and polkit | Required `unit`; must match `systemd.units` and `systemd.restart`; catalog action, no undo |
| `host_containers` | Host provider: state of allowlisted Docker containers | None |
| `host_container_logs` | Host provider: masked, demultiplexed log tail of one container | Required `container`; `lines=100`, max 500 |
| `host_restart_container` | Host provider: restart one Docker container | Required `container`; must match `docker.containers` and `docker.restart`; plan binds the full container ID; catalog action, no undo |
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

Image restore checks the source Pod controller UID, Pod/container readiness and running state, and exact spec image. Status image references must match after Docker Hub/default-tag normalization; a SHA-256/SHA-512 runtime imageID is required and digest-pinned images must match it. Source checks repeat before apply; namespace scans above 500 ReplicaSets or 500 Pods fail closed. Readiness does not guarantee application health, and registry tags remain mutable.

Note search, tags and links operate on masked text.

Note paths use vault-relative forward slashes, preserve Unicode bytes, and are limited to 1024 bytes. Excluded paths behave as missing. Note text is untrusted data. Note numeric inputs outside the documented ranges are rejected. Safe in-vault symlinks are supported with cycle prevention. Scans are bounded to 100000 entries and 128 directory levels; links/backlinks each return at most 1000 results. Write previews include a unified diff (against `/dev/null` for create), current hash and resulting hash, with explicit truncation near 8 KiB. Apply rechecks the approved hash, size and modification time. Creates publish atomically without overwriting racing creators; updates use a synced temporary file and rename. On Linux, trash atomically moves the source to the approved .trash destination without overwriting an existing entry, then validates the moved note. If a replacement or edit raced with the move, the operation reports an error with the recovery path in .trash; restore it if needed. If the destination appeared after planning, the move fails and leaves the source intact. On Linux, the source and .trash must share a filesystem supporting renameat2 with RENAME_NOREPLACE; otherwise the move fails safely. On other platforms, trash retains a durable approved copy before checking and removing the source; an external replacement between that final check and removal can still be lost. Use versioned backups or sync history on those platforms. Existing trailing-newline policy is preserved. Each content/replacement input is capped at 256 KiB. The create, append and replace tools allow 528 KiB of raw JSON arguments, including escaping; trash retains the default 16 KiB limit. Updates use an optimistic state check followed by rename: an external edit between the check and rename can be overwritten. Use versioned backups or sync history. Large create previews are truncated too, with an explicit omitted-line count.

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

Web approval runs the original call once. All approval-gated tools accept optional `approval_id` to read a stored outcome; omit it on the first call. Retrying the same arguments never re-applies. `approval.wait` defaults to 25s with web approval (maximum 55s; zero disables waiting); a waiting call receives the execution result directly.

When web approval is enabled, read-only `approval_status` (`ops:read`) takes required `approval_id` and returns the requester’s `tool`, `target`, `status` (`pending`, `running`, `applied`, `not_applied`, `rejected`) and `result` when available. Other users receive the same answer as unknown or expired IDs. Tool policy, audit and output limits apply. Outcomes remain in memory until original expiry and at least 10 minutes after execution.

Read tools accept up to 16 KiB of raw JSON arguments. Write arguments default to 16 KiB; `note_create`, `note_append` and `note_replace` allow 528 KiB. JSON escaping counts toward these limits. Approval summaries are bounded by `max(64 KiB, argument limit + 16 KiB)`: 64 KiB for default tools, 544 KiB for note content tools. Redacted display arguments must fit the tool's argument limit. Note previews have a separate approximately 8 KiB display bound.

The shared wrapper omits provider output above 4 MiB entirely, then redacts text before truncating it to `server.max_output_bytes` (default 64 KiB). Errors follow the same path. Smaller provider-specific collection limits still apply. Deadlines are cooperative; write planning and applying each receive a separate `server.tool_timeout` (default 30s), excluding human waiting time.

`action_link` supports only restart, image restore, scale and controller-owned pod
deletion. It checks server registration, the caller's tool policy, typed catalog
arguments and live target allowlists without requiring `ops:write`. It rejects
extra arguments (including `source` inside `arguments` or `approval_id`) and
masked values. Links use `/approve/action/{tool}`; approving still requires the
browser user's write scope and a fresh review. See [approval](approval.md#catalog-action-links).

The remediation tool retains `actions`, `label`, `url`, `container`,
`current_image` and `image`. It adds `tool`, `label_ko`, `reason`, `reason_ko`,
`hint` and `pod_gone`. `source` now consistently means `rule`; the old
ReplicaSet source name is in `source_replicaset`. Rule selection never writes.
Candidate verification and workload scans refuse partial lists and more than
500 objects per namespace. The approving browser independently checks write
scope, user tool policy and allowlists, then follows plan → approval → re-plan
→ apply → audit.
