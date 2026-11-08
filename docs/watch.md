# Kubernetes watch alerts

[한국어](ko/watch.md)

`opsgate watch` is a separate long-running process that watches Kubernetes
and sends alerts by webhook, SMTP email, or both. A webhook can be, for example,
an n8n workflow that forwards them to chat. It serves no MCP clients and has no
tools: an AI client still investigates through the usual read tools after a
person sees the alert.
Running it apart from the MCP server means stdio users can use it too, and it
can run under its own account and kubeconfig.

```sh
opsgate watch -config /etc/opsgate/opsgate.yaml
```

It reads `providers.kubernetes` for clusters and namespace policy and
`watch` for delivery:

```yaml
watch:
  outputs:
    - name: webhook
      type: webhook
      url: https://n8n.example.com/webhook/opsgate-watch
      secret_file: /etc/opsgate/watch-secret  # optional HMAC key, >= 16 bytes
      allow_private_http: false
  email:                           # optional; enabled by host
    host: smtp.example.com
    port: 587                      # default; 465 uses implicit TLS
    username: alerts@example.com   # optional; pair with password_file
    password_file: /etc/opsgate/smtp-password
    from: alerts@example.com
    to: [me@example.com]
    batch: 5m                      # default; 1m–1h
    problems_only: false           # warnings only for email when true
  clusters: []          # default: every configured cluster
  problems_only: false  # true sends warnings only
  batch: 30s            # gather alerts into one POST (1s–10m)
  cooldown: 15m         # repeat warnings for one object are counted, not sent (0–24h)
  max_alerts: 50        # alerts per channel batch (1–500); the rest count as dropped
```

Each output’s `url` must use https, or http to a loopback address, and must not
contain credentials or a fragment. With output-level `allow_private_http: true`, plain http to a private
network is also accepted, such as a Service name
(`http://n8n.n8n.svc.cluster.local:5678/...`) or a
private IP address. Every connection must then reach a private or loopback address after DNS
resolution, and proxy settings are ignored so the checked address is the
receiver's. A public IP literal is still refused. Alerts cross that network
unencrypted. A secret query key in
the URL is never logged.

`watch.webhook_url`, `watch.secret_file` and `watch.allow_private_http` were removed. Configurations containing these keys are rejected. Move them into a `type: webhook` entry in `watch.outputs`, using `url`, `secret_file` and `allow_private_http` on that output. The default webhook JSON body and HMAC signature are preserved.

## Host alerts

With the optional [host provider](host.md) enabled, `opsgate watch` also polls
systemd units and Docker containers and sends `UnitFailed`, `UnitRestarted`,
`ContainerUnhealthy`, `ContainerExited` and `ContainerRestarted` warnings. Their
cluster and namespace are `host`, so groups match them with `host` or `*`.
Kubernetes is optional for watch when the host provider is enabled.

## Groups

Use `watch.groups` to choose destinations, warning filtering and batching by the
alert's namespace. The destinations below refer to an existing `discord` entry
in `watch.outputs`, a configured `watch.inbox`, and a configured `watch.email`:

```yaml
watch:
  groups:
    - name: customer
      namespaces: [bid, chagok, "shop-*"]
      outputs: [discord, inbox]
      problems_only: true
      batch: 1s
    - name: rest
      namespaces: ["*"]
      outputs: [email]
      problems_only: false
      batch: 15m
```

The **first matching group wins**, using Go `path.Match` globs against the whole
namespace (case-sensitive, `*` does not cross `/`). A filtered alert never falls
through to another group. Nodes and other alerts without a namespace match only
a literal `""` or `"*"` pattern; other patterns, including `"**"`, cannot match
them. Put a catch-all last if you want every namespace and cluster-scoped alert.
Groups route already-allowed alerts; they do not widen provider namespace policy.

Without `groups`, every configured output receives alerts as before, with its
existing filters and intervals. An explicitly empty list is rejected. With
groups, only the selected destinations receive alerts. Unmatched alerts are
dropped once and counted using the existing `dropped` summary rendering in a
local `watch unmatched` log every minute, and on shutdown. This summary contains
only counts; unmatched alert content is never sent to a destination.

`watch.problems_only` still limits what the watcher produces. A group's
`problems_only` and each output's `problems_only` are additional filters: any
`true` setting drops info alerts. Inbox still accepts only pod warnings carrying
a choices link; groups do not relax that rule or change the signed notice schema.

The group batch replaces the selected output's interval: use **1s–10m** for
webhook/Discord and inbox, **1m–1h** for email. A group selecting both email and
webhook/inbox must use **1m–10m**. The shortest interval is near-immediate delivery
(`1s`, or `1m` for email); zero is invalid. Without groups, inbox keeps its fixed
1s interval. Each `(group, output)` has separate batches, cooldown state, bounded
queues and retries, so a failing destination or group cannot delay another.
`watch.cooldown` and `watch.max_alerts` still apply (inbox caps batches at 50).
Grouped cooldown caches hold at most 1000 warning keys per pair; when full, new
keys are delivered without tracking cooldown until space is available. Different
groups sharing one output are always sent separately. Discord titles and email
subjects show the group name; default webhook JSON adds `group`, and custom
templates can include `{{json .Group}}`. Without groups the field is omitted.

Configure 1–50 groups, with unique names of 1–64 ASCII letters, digits, `_` or
`-`. Each needs 1–100 valid namespace patterns (up to 253 bytes each), 1–22
unique output names and an explicit valid batch interval. Output names must
exist in `watch.outputs`; `email` and `inbox` refer only to the enabled dedicated
settings and are reserved when groups are used. Unknown or disabled outputs,
duplicate names, invalid globs and out-of-range intervals fail startup.

## Email delivery

Webhook and email are independent: configure either or both. Each channel has
its own batching, cooldown state and retries; a slow or failing channel does
not delay the other. Both use `watch.cooldown` and `watch.max_alerts`.
`watch.batch` controls webhook batching; `watch.email.batch` defaults to `5m`
and accepts `1m` through `1h`. `watch.email.problems_only` filters email only.
`watch.problems_only` prevents info alerts from being produced for every channel.

TLS is mandatory and certificates are verified against `email.host`, with TLS
1.2 or newer. Port `465` uses implicit TLS; every other port requires STARTTLS.
Delivery stops if STARTTLS is unavailable or refused. Credentials and message
content are sent only after TLS is established. There is no insecure mode.

Set `username` and `password_file` together, or omit both for a relay that
requires no authentication. The password file is read at startup, with surrounding
whitespace trimmed; unreadable or empty files stop startup. Use a dedicated
credential, such as an app password, so it can be revoked on its own. Mount it
from a Secret when running in Kubernetes; see the commented example in
`deploy/kubernetes/opsgate-watch.yaml`.

`host` takes a host name without whitespace, a scheme, port or slash. `port`
accepts 1–65535 and defaults to `587`. `from` and each `to` entry must be bare
email addresses, without display names or line breaks; `to` accepts 1–20
addresses. Other email fields require `host`.

Email is plain text, with a quoted-printable body containing the same `text`
and dropped count as the webhook. A single alert uses its text as the subject;
multiple alerts list warning and change counts, then sorted cluster names.
Subjects have control characters removed, are capped at 120 Unicode characters
and use MIME encoding. Email contains no HTML or attachments.

Network and TLS handshake errors and SMTP 4xx replies are retried twice,
after 1s and 4s. SMTP 5xx replies, including rejected credentials, and refused
STARTTLS are not retried. Each exchange is bounded to 30 seconds or the
remaining context deadline. Alerts are not stored: after retries are exhausted,
they are logged as lost, just as with webhook delivery. Shutdown flushes pending
alerts within each channel's shutdown deadline.

Namespace, pod and container names and redacted summaries pass through the
mail provider, which is outside the cluster. Check whether that is appropriate
for the cluster before enabling email.

Worst-case volume is one message per batch interval: a `5m` batch can send up
to 288 messages a day and a `15m` batch up to 96, per group when groups are
configured. Check the account's sending limit, especially if other services
share it.

## What is reported

Objects that exist when the watcher starts are the baseline; only later
transitions are reported. Pods are never reported as created or deleted.

| Kind | Warning | Info (omitted with `problems_only`) |
| --- | --- | --- |
| Pod | Container restarted, with the last termination reason and exit code (`OOMKilled`, `Error`, ...); waiting in `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`, `CreateContainerConfigError`, `CreateContainerError` or `RunContainerError`; phase became `Failed` (e.g. `Evicted`); became unschedulable | |
| Deployment | Rollout stalled (`ProgressDeadlineExceeded`) | Image changed, replicas changed, created, deleted |
| StatefulSet, DaemonSet | | Image changed, replicas changed (StatefulSet), created, deleted |
| Node | Ready became `False` or `Unknown`; memory, disk or PID pressure or network unavailable became `True` | Ready again, cordoned, uncordoned, joined, removed |
| ConfigMap | | Key names changed, added or removed; created, deleted |

`CrashLoopBackOff` is not a separate alert because the restart that precedes it
is already reported. ConfigMap alerts name keys only. The watch cache stores a
digest of each value instead of the value, so values are never sent.

Namespace allow and deny rules apply as they do for the tools. Exact namespace
names are watched one by one. Any glob pattern (including `*`) watches the whole
cluster and filters locally, so namespaces created later are covered. That needs
cluster-wide list/watch RBAC. Nodes are always watched.

### Hints and restart suggestions

Warnings carry a `hint`: fixed advice chosen from the alert type, never text
from the cluster. A pod that fails while it is being deleted is not reported.

For compatibility, an alert also names a workload in `restart` when all of these hold:

- the container has restarted at least 3 times;
- its exit reason is not `OOMKilled`, `Unknown`, `ContainerStatusUnknown` or `Completed`;
- the pod belongs to a Deployment, StatefulSet or DaemonSet, not a Job;
- the watch configuration lists the workload under
  `providers.kubernetes.rollout_restart`. Use the same list as the server.

The watcher only suggests. It has no write permission and nothing acts on the
suggestion. A person asks an MCP client for the restart, and `k8s_rollout_restart`
checks the allowlist again and requires approval as usual.

The restart suggestion appears on the first alert sent after those conditions
hold. The cooldown holds back repeats, so a crash-looping pod usually shows it
about one cooldown after its first alert.

With `watch.approval_url`, `restart.url` also links to the server's approval
page, so a chat button can open it. The link holds no secret. Opening it needs
browser sign-in, the same scope and tool rules as the MCP path, and a fresh plan.
Only pressing Approve restarts the workload, once, and only if the plan is
unchanged. An approval window never covers this path. The server must use web
approval with built-in sign-in. New restart URLs use the shared catalog builder:
`/approve/action/k8s_rollout_restart?...&source=rule`. The page shows “Restart
workload” and “Suggested by: rule”. Historical restart links still work as
aliases. Other catalog actions can be suggested through `action_link` and the
analyzer. The legacy restart conditions above remain conservative; live choices
also offer an OOM restart with an explicit warning that it only buys time.

With `watch.approval_url`, every **pod warning** in an allowed namespace whose
owner resolves to a Deployment, StatefulSet or DaemonSet also carries
`options_url`: a **Choose an action** / **조치 선택** link to
`/approve/options/{cluster}/{namespace}/{pod}?workload=kind/name`. Info alerts,
bare pods and unsupported owners never get it. This needs no restart allowlist
match: the server, after sign-in, computes the applicable enabled/allowlisted
actions from the current cluster state. The watcher only supplies a workload
navigation hint; the server re-verifies live ownership by UID. Image pull errors
can offer image restore; failing rollouts can offer restore before restart;
repeated OOM can offer a temporary restart; stuck or unreachable controller-owned
pods can offer deletion. [Full rules](approval.md#live-choices-per-failure).

Discord embeds, webhook JSON/text and email text include the same choices link,
localized per output. Existing `restart` and `restart.url` fields are preserved
for external n8n/Telegram receivers; `options_url` and `workload` are additive.
Custom webhook templates can use `.OptionsURL` on an alert. A choices link has
no secret, grants no approval and may show no choices if the state recovered or
policy changed. Opening a choice creates a fresh plan; only approval applies it.

## Webhook payload

```json
{
  "source": "opsgate",
  "sent_at": "2026-10-29T03:00:30Z",
  "alerts": [
    {
      "time": "2026-10-29T03:00:12Z",
      "cluster": "default",
      "severity": "warning",
      "kind": "Pod",
      "namespace": "apps",
      "name": "web-7d9f8-abcde",
      "reason": "OOMKilled",
      "summary": "container app restarted (3 restarts): OOMKilled, exit code 137",
      "hint": "The container hit its memory limit. Check the limit and the app's usage; a restart will not prevent a repeat.",
      "suppressed": 2
    }
  ],
  "dropped": 0,
  "text": "[warning] default Pod apps/web-7d9f8-abcde: container app restarted (3 restarts): OOMKilled, exit code 137 (+2 similar)\n  hint: The container hit its memory limit. Check the limit and the app's usage; a restart will not prevent a repeat."
}
```

`text` can be forwarded to chat as is. `suppressed` counts repeats folded into
the alert: the same alert repeated within one batch, or a warning repeated
during its cooldown. Info alerts are never held back by the cooldown. Repeats
within one batch are merged and keep the latest summary. All cluster-controlled alert text, including `reason`, `summary` and hints, is masked before delivery or deduplication. Control characters become spaces; text is limited to 300 Unicode characters plus a truncation ellipsis. Direct JSON and custom templates receive these same sanitized fields; ordinary machine-readable reasons remain unchanged. Messages from the
cluster are still untrusted data.

With an output’s `secret_file`, every POST carries:

```text
X-Opsgate-Signature: sha256=<hex HMAC-SHA256(secret, raw body)>
```

Verify the signature against the raw body, then reject batches whose signed
`sent_at` is old to limit replays. Receivers such as n8n can check this with their built-in HMAC node and no custom code. Redirects
are not followed. Network errors, HTTP 429 and 5xx responses are retried twice, after 1s
and 4s; after that the batch is logged as lost. When the internal queue (1000
alerts) is full, new alerts are counted in `dropped` of the next POST.

## Running it

Run the watcher as a single-replica Deployment:
`deploy/kubernetes/opsgate-watch.yaml` uses the `opsgate` ServiceAccount from
`rbac-readonly.yaml`, an in-cluster configuration and the
`opsgate-watch-hmac` Secret. It needs only `list` and `watch` on pods,
configmaps, deployments, statefulsets, daemonsets and nodes. It never reads
Secrets through the API and makes no writes. Keep `replicas: 1` and the
`Recreate` strategy: every replica watches the whole scope, so two would send
every alert twice. It is not a DaemonSet for the same reason.

```sh
kubectl apply -f deploy/kubernetes/rbac-readonly.yaml
kubectl -n opsgate create secret generic opsgate-watch-hmac --from-literal=key="$(openssl rand -hex 32)"
kubectl apply -f deploy/kubernetes/opsgate-watch.yaml
kubectl -n opsgate logs deployment/opsgate-watch
```

Outside the cluster, `opsgate watch` can also run with a kubeconfig that provides cluster access.

Watch errors (for example missing RBAC) are logged and retried by client-go.
Restarting the watcher resets the baseline, so changes made while it was down
are not reported.

## Direct Discord and custom webhooks

Use `watch.outputs` to deliver directly to Discord or custom JSON endpoints,
without n8n. Destinations run independently alongside `watch.email`. See [configuration](configuration.md#direct-webhook-destinations)
for all fields, template variables, credential files and delivery limits.

```yaml
watch:
  outputs:
    - name: discord
      type: discord
      url_file: /etc/opsgate/discord-webhook-url
    - name: custom
      url: https://alerts.example.com/ingest
      body_template: |
        {"content": {{json .Text}}, "alerts": {{json .Alerts}}}
```

Discord embeds include UTC occurrence times, cluster/resource identity, reasons
and summaries. Mentions are disabled; descriptions beyond 4096 UTF-16 units are
marked as truncated. Lower `watch.max_alerts` or keep a native JSON destination
for complete large batches. Custom templates must render valid JSON (up to 1 MiB).
Use `json` for escaping and do not put quotes around its result. All webhook
formats retry HTTP 429 as well as network errors and 5xx, using bounded backoff.

## Phone alerts through the approval inbox

`opsgate watch` runs separately from the HTTP server, often in another pod.
Configure a dedicated shared HMAC key on both processes (16–4096 bytes after
trimming whitespace; use a random key, separate from other webhook keys):

```yaml
# Watch process; merge into your existing watch configuration.
watch:
  approval_url: https://ops.example.com
  inbox:
    url: https://ops.example.com
    secret_file: /etc/opsgate/inbox/key
    allow_private_http: false
```

```yaml
# HTTP server; merge into a working builtin-auth configuration.
auth:
  builtin:
    users:
      - subject: passkey:owner
        scopes: [ops:read, ops:write]
approval:
  web: true
  inbox_notify:
    secret_file: /etc/opsgate/inbox/key
    subjects: [passkey:owner]
  push:
    enabled: true
    subject: mailto:owner@example.com
    state_file: /var/lib/opsgate/push.json
```

`watch.inbox.url` is an origin without a path, query or credentials. HTTPS is
the default; loopback HTTP and private HTTP with `allow_private_http: true`
follow the same URL and dial-time checks as webhook outputs. An in-cluster
example is `http://opsgate.opsgate.svc:8787`. Keep `approval_url` as the public
HTTPS origin for messenger links; if omitted, choices links use the inbox
origin. The reverse proxy must forward `POST /approvals/notify`. The enabled
signed POST endpoint also accepts private service Host names; browser and MCP
routes retain the configured Host policy.

This independent output has its own nonblocking queue, a **1s** batch
interval (overridden by a group batch when groups are configured), the normal cooldown and webhook retries (up to three attempts,
waiting 1s then 4s; network failures, 429 and 5xx retry). It sends only warning
pod alerts with a **Choose an action** link, never informational events. Each
batch has at most 50 notices (also bounded by `watch.max_alerts`). Other outputs
keep their own queues and intervals; an inbox-only watch is also supported.

The signed JSON contains only `sent_at` and `notices`, each with `cluster`,
`namespace`, `pod`, `workload_kind`, `workload_name`, `reason`, `severity` and
`occurred_at`. It excludes summaries, messages, hints, supplied URLs and other
cluster text. Identifiers are already masked by watch; invalid or masked
navigation targets are skipped. The signature uses the existing
`X-Opsgate-Signature: sha256=<hex HMAC-SHA256 of raw body>` format. Notices do
not create approvals or execute actions.

Enable notifications once in your phone's normal browser at `/approvals`
([instructions](approval.md#approval-inbox-and-notifications)); on iPhone, add
it to the Home Screen first. An alert push opens that inbox, where **Alerts
waiting for a decision** links to live choices. Choose, review and approve
with your passkey there, outside Telegram/Discord's in-app browser. Push is
optional: notices remain visible without it. See [notice limits and security](approval.md#signed-watch-notices).
