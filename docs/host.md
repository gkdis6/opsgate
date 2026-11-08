# Host provider

[한국어](ko/host.md)

The optional host provider brings opsgate's flow (alert, choices, approve,
applied) to a server without Kubernetes: systemd units and Docker or Compose
containers on the machine opsgate runs on. It is off by default and registers
nothing until you enable it and list the units or containers it may see.

Use it on a VPS or home server where you would otherwise SSH in to run
`systemctl restart` or `docker restart`. Do not enable it inside a Kubernetes
pod: `mode: kubernetes` refuses it, and opsgate warns if it finds itself in a
pod anyway.

## Configuration

```yaml
providers:
  host:
    enabled: true
    poll_interval: 30s            # opsgate watch polling, 10s–5m
    systemd:
      units: ["app-*.service", nginx.service]   # visible units (path.Match globs)
      restart: ["app-*.service"]                # units a person may restart
    docker:
      endpoint: unix:///run/docker-proxy/docker.sock   # or http://127.0.0.1:2375
      containers: [web, "compose:shop/*"]       # names, or compose:<project>/<service>
      restart: ["compose:shop/*"]
```

- A restart needs the target in both the visible list and the restart list.
- `compose:<project>/<service>` matches the `com.docker.compose.project` and
  `com.docker.compose.service` labels that Compose sets.
- The Docker endpoint must be a unix socket or plain HTTP on a loopback address.
  Point it at a socket proxy, not at the Docker socket itself (see below).
- `mode: kubernetes` with `providers.host.enabled: true` is a configuration error.

## Tools

| Tool | Kind | What it does |
| --- | --- | --- |
| `host_units` | read | Load, active and sub state, last result, restart count and state change time of the visible units |
| `host_unit_logs` | read | Masked journal tail of one visible unit (`lines`, default 100, max 500) |
| `host_restart_unit` | write | `systemctl --no-ask-password restart -- <unit>` after approval |
| `host_containers` | read | Image, state, health, exit code, restart count and Compose labels of the visible containers |
| `host_container_logs` | read | Masked, demultiplexed log tail of one visible container |
| `host_restart_container` | write | `POST /containers/{id}/restart?t=10` after approval; the plan shows the full container ID and the restart is refused if the container was recreated since |

Both restarts are catalog actions, so they can be offered as links, on the
choices page and by the analyzer. They have no undo.

Names are validated before use: unit names must look like `name.service` (or
another unit type), container names like `[A-Za-z0-9][A-Za-z0-9_.-]*`. Commands
run with fixed argv, never through a shell, with a 15s timeout and a 4 MiB
output cap. Logs are masked before they are cut to the requested length.

## Choices and alerts

`/approve/host-options/{unit|container}/{name}` shows the current choices after
sign-in, like the Kubernetes choices page:

| Target | Offered when | Action |
| --- | --- | --- |
| systemd unit | it failed, or it has restarted | `host_restart_unit` |
| Docker container | it is unhealthy, restarting or exited with a non-zero code, or it has restarted | `host_restart_container` |

With the host provider enabled, `opsgate watch` polls every `poll_interval` and
sends a warning when a unit fails (`UnitFailed`) or restarts (`UnitRestarted`),
or a container becomes unhealthy (`ContainerUnhealthy`), exits with an error
(`ContainerExited`) or restarts (`ContainerRestarted`). The first poll only
records the current state, so starting watch does not repeat old problems. A
recreated container (new ID) starts a new history.

Host alerts use `host` as both cluster and namespace, so a
[watch group](watch.md#groups) with `namespaces: [host]` routes them. With
`watch.approval_url` they carry a **Choose an action** link, and with
`watch.inbox` they reach the approval inbox and push like pod warnings. The
optional analyzer does not investigate host alerts yet; its tool allowlist covers
Kubernetes and notes only.

## Privileges

opsgate runs as an unprivileged `opsgate` account. Nothing in the host provider
needs root, sudo or extra capabilities.

**systemd.** Reading unit state needs no privilege. Reading journals needs the
`systemd-journal` group. A restart goes through systemd's D-Bus API, which asks
polkit; install [`deploy/host/50-opsgate.rules`](../deploy/host/50-opsgate.rules)
and list the exact units the `opsgate` account may restart. The rule allows the
`restart` verb on those units only. Because no setuid program is involved, the
service keeps `NoNewPrivileges=yes`; see
[`deploy/host/opsgate.service`](../deploy/host/opsgate.service) and
[`opsgate-watch.service`](../deploy/host/opsgate-watch.service).

**Docker.** Access to the Docker socket is equivalent to root on the host: it can
start a privileged container that mounts `/`. Never give opsgate the Docker
socket. Run a socket proxy that forwards only these calls, and point
`providers.host.docker.endpoint` at it:

| Method | Path (optionally prefixed with `/v1.xx`) | Used by |
| --- | --- | --- |
| GET | `/containers/json` | listing |
| GET | `/containers/{id or name}/json` | inspect |
| GET | `/containers/{id}/logs` | `host_container_logs` |
| POST | `/containers/{id}/restart` | `host_restart_container` |

Any proxy that filters by method and path works; for example
[wollomatic/socket-proxy](https://github.com/wollomatic/socket-proxy) takes one
regular expression per method. Check the flags against the version you install;
an illustrative configuration:

```sh
socket-proxy \
  -proxysocketendpoint=/run/docker-proxy/docker.sock -proxysocketendpointfilemode=0660 \
  '-allowGET=^(/v[0-9.]+)?/containers/(json|[A-Za-z0-9][A-Za-z0-9_.-]*/(json|logs))$' \
  '-allowPOST=^(/v[0-9.]+)?/containers/[0-9a-f]{64}/restart$'
```

Give the proxy socket to a group that the `opsgate` account belongs to, and
test that `docker run` through the proxy is refused before relying on it.

## Security notes

- The allowlists, not the AI or the alert, decide what may be restarted. Rules
  and AI suggestions only pick from the catalog.
- A person approves every restart in the browser (or in the client, if you
  opted out of web approval). Approval windows apply as configured.
- Unit and container output is untrusted host data: it is masked and size
  limited, and clients are told not to follow instructions in it.
- One instance is one trust group. If the same opsgate also serves Kubernetes,
  `ops:read` covers both.
