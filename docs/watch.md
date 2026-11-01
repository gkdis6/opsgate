# Kubernetes watch alerts

[한국어](ko/watch.md)

`opsgate watch` is a separate long-running process that watches Kubernetes
and posts alerts to a webhook, for example an n8n workflow that forwards them
to chat. It serves no MCP clients and has no tools: an AI client still
investigates through the usual read tools after a person sees the alert.
Running it apart from the MCP server means stdio users can use it too, and it
can run under its own account and kubeconfig.

```sh
opsgate watch -config /etc/opsgate/opsgate.yaml
```

It reads `providers.kubernetes` for clusters and namespace policy and
`watch` for delivery:

```yaml
watch:
  webhook_url: https://n8n.example.com/webhook/opsgate-watch
  allow_private_http: false  # true also allows http to private receivers
  secret_file: /etc/opsgate/watch-secret   # optional HMAC key, >= 16 bytes
  clusters: []          # default: every configured cluster
  problems_only: false  # true sends warnings only
  batch: 30s            # gather alerts into one POST (1s–10m)
  cooldown: 15m         # repeat warnings for one object are counted, not sent (0–24h)
  max_alerts: 50        # alerts per POST (1–500); the rest count as dropped
```

`webhook_url` must use https, or http to a loopback address, and must not
contain credentials or a fragment. With `allow_private_http: true`, plain http to a private
network is also accepted, such as a Service name
(`http://n8n.n8n.svc.cluster.local:5678/...`) or a private IP address. Every
connection must then reach a private or loopback address after DNS
resolution, and proxy settings are ignored so the checked address is the
receiver's. A public IP literal is still refused. Alerts cross that network
unencrypted. A secret query key in
the URL is never logged.

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
      "suppressed": 2
    }
  ],
  "dropped": 0,
  "text": "[warning] default Pod apps/web-7d9f8-abcde: container app restarted (3 restarts): OOMKilled, exit code 137 (+2 similar)"
}
```

`text` can be forwarded to chat as is. `suppressed` counts repeats folded into
the alert: the same alert repeated within one batch, or a warning repeated
during its cooldown. Info alerts are never held back by the cooldown. Repeats
within one batch are merged and keep the latest summary. Summaries are
sanitized like tool output: control characters are removed, known secret
patterns are masked and text is cut at 300 characters. Messages from the
cluster are still untrusted data.

With `secret_file`, every POST carries:

```text
X-Opsgate-Signature: sha256=<hex HMAC-SHA256(secret, raw body)>
```

Verify the signature against the raw body, then reject batches whose signed
`sent_at` is old to limit replays. Receivers such as n8n can check this with
their built-in HMAC node and no custom code. Redirects
are not followed. Network errors and 5xx responses are retried twice, after 1s
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

Outside the cluster, `deploy/systemd/opsgate-watch.service` runs it on a host
with the same hardening as the server, using a kubeconfig.

Watch errors (for example missing RBAC) are logged and retried by client-go.
Restarting the watcher resets the baseline, so changes made while it was down
are not reported.
