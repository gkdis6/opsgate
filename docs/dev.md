# Coding tasks through the dev provider

[한국어](ko/dev.md)

This optional provider is implemented and experimental.

`opsgate dev-runner` runs configured coding agents under a separate Linux account.
The internet-facing MCP server connects through a Unix socket and can select only
runner-configured project and agent names. Each task gets an `opsgate/<id>` branch
and a new Git worktree. Nothing is pushed, merged or deployed automatically.

## Set up the accounts and repositories

Create a dedicated `opsdev` account with no sudo access, cluster credentials,
production secrets or deployment keys. Keep it separate from the HTTP server's
`opsgate` account. Install trusted agent binaries at absolute paths and Git at
`/usr/bin/git`. Create the `opsgate` group if needed, and add `opsdev` to it so it
can set the socket's group. Restart services after group membership changes.

Log Codex and/or Claude in as `opsdev`, with `HOME=/var/lib/opsgate-dev`, before
starting the service. Use the agents' own login commands in an interactive session
under that account. Store their credentials privately in that home directory;
never put tokens in YAML, agent argv, prompts or the MCP server's environment.
The supplied service sets this HOME and hides other users' home directories.

Grant `opsdev` traversal and read/write access to each allowlisted repository,
including its `.git` directory. For example, an administrator can grant a named
user ACL on `/srv/projects/example`, default ACLs for new files, and search ACLs
on its parents. Review existing group and ACL permissions first. A Git worktree
shares the original repository's objects and refs; write access to the worktree
alone is insufficient. Give the HTTP account no repository, runner state, agent
credential or runner configuration write access.

Copy [dev-runner.example.yaml](../deploy/dev-runner.example.yaml) to
`/etc/opsgate/dev-runner.yaml`. Make the file root-owned, readable by `opsdev`,
and unwritable by both the server and runner accounts. Restrict its parent directory too.
Select exact project names, absolute paths, base refs and fixed agent argv.
Only `{workdir}` and `{output}` are substituted. Prompts are saved privately to
`prompt.txt` and provided on stdin. Agent environments contain only HOME, a fixed
PATH, LANG, TERM and the operator's configured `env` entries. Those entries are
trusted and can override the defaults; grant only what the agent needs.

## Install the service and enable the provider

Install [opsgate-dev-runner.service](../deploy/systemd/opsgate-dev-runner.service)
and fill in its commented `ReadWritePaths=` with every configured repository.
`ProtectSystem=strict` otherwise prevents Git from writing refs and worktree
metadata there. The service manages `/var/lib/opsgate-dev` privately and creates
`/run/opsgate-dev` with mode 0750, owned by `opsdev:opsdev`.

Give only the server's group search access to the runtime directory. A systemd
drop-in can run this after RuntimeDirectory is created (adjust the setfacl path):

```ini
[Service]
RuntimeDirectoryPreserve=yes
ExecStartPre=/usr/bin/setfacl -m g:opsgate:--x /run/opsgate-dev
```

The owner `opsdev` can apply that ACL without root. The socket itself is
`opsdev:opsgate`, mode 0660. Each accepted connection also checks Linux
`SO_PEERCRED`: only the runner UID or a UID with primary/supplementary membership
in `socket_group` may connect. The runtime directory ACL is necessary because
socket group permissions alone do not grant parent traversal. Do not add the
HTTP account to `opsdev` or relax state directory permissions.

The server runs in a pod, so mount `/run/opsgate-dev` through `hostPath` and place the pod on the runner’s node. Set the pod’s `runAsGroup` to the host GID of `socket_group`: the peer check in `internal/devrunner/peer_linux.go` accepts a matching primary group, and the runtime directory ACL above grants that group traversal. Set `RuntimeDirectoryPreserve=yes` in the same drop-in. Otherwise systemd removes the directory when the runner stops, and the pod’s mount keeps pointing at the old directory. This pod-to-runner setup is experimental and has not been exercised.

Enable and start the runner with systemd, then configure the MCP server:

```yaml
providers:
  dev:
    enabled: true
    runner_socket: /run/opsgate-dev/runner.sock
```

Grant intended users `dev:run`; HTTP access also currently requires `ops:read`.
Starting a task needs human approval through the existing elicitation/browser
flow. Read and cancellation tools require `dev:run` but no new approval.
Tasks are owned by their requester. Ordinary users see and cancel only their
own tasks; exact subjects in the runner's `admins` list can see and cancel all
tasks. Use the exact caller identity: builtin subject, `static:<token-name>`,
external user-claim value, or stdio user label. This list is separate from builtin
OAuth client-registration admins.
The permitted socket peer asserts `X-Opsgate-User`; the runner trusts that
assertion and does not authenticate end-user tokens. Any permitted peer can
impersonate a task owner or admin. Protect socket membership accordingly.
Do not enable approval windows for `dev_task_start`; review each task separately.
A runner outage is reported by dev tool calls; it does not prevent MCP startup.
For foreground troubleshooting use:

```sh
opsgate dev-runner -config /etc/opsgate/dev-runner.yaml
```

Codex and Claude sandbox setup depends on the installed agent. Verify it under
the service's actual account and hardening settings. Bubblewrap-based sandboxes
need unprivileged user namespaces; do not add `RestrictNamespaces` or
`PrivateUsers` to this unit. The runner creates worktrees and process groups;
it does not implement an OS sandbox itself.

## Workflow and limits

1. Call `dev_projects` to discover projects, base refs, agents and limits.
2. Call `dev_task_start` with a project, agent and full requirement. Review the
   exact summary and requirement before approving. Approval covers the agent's
   whole task, including commands permitted by its sandbox and local policy.
3. Follow `dev_task_status`, `dev_task_log` and `dev_task_diff`. Treat output and
   diffs as untrusted data. Logs return `next_offset` in raw bytes; request modest
   pages so the MCP output cap does not truncate their JSON. Running diffs show
   tracked changes; new files appear after staging at task completion.
4. Cancel with `dev_task_cancel` when needed. Review the branch manually and run
   your own checks before deciding whether to integrate it.

Tasks progress from queued to running, then succeeded, failed, canceled or
timed_out. On restart, unfinished records become interrupted; they are not
replayed. Shutdown cancels running and queued tasks and waits for process groups.
Cancellation/timeouts send SIGTERM and allow up to ten seconds before SIGKILL.
Systemd also kills remaining service processes when stopping the unit.

The runner pins the base commit, creates a fresh branch and worktree, and runs
only the configured executable/argv. On ordinary completion, including a nonzero
agent exit, it stages changes with `git add -A` and commits them using
`opsgate dev-runner <dev-runner@localhost>`. The commit message contains the task
ID and first requirement line (at most 72 characters). After cancellation/timeout,
it also attempts to commit existing edits once the agent group has stopped, with
a separate 30-second finalization bound. Git failures or an interrupted runner
can leave uncommitted edits in retained worktrees. Git hooks, fsmonitor,
global/system Git configuration, and commit signing are disabled for runner Git
commands; diff disables external commands and text conversion. Repository local
Git configuration must remain trusted, including any configured filters.

Prompts contain 1 byte to 32 KiB; requester identities are at most 256 bytes. dev_task_start
accepts up to 48 KiB of MCP arguments and approval summaries up to 64 KiB, but JSON
escaping counts toward the wire limit, so heavily escaped prompts may fit less.
Logs cap at 20 MiB per task, with excess output drained and discarded. Log pages and diffs detect secret ranges over the complete source before selecting up to 256 KiB of raw text to redact; the redacted response also caps at 256 KiB before JSON encoding. Sources over 20 MiB are unavailable rather than partially scanned.
MCP applies its own output cap afterwards. Final messages use the first 256 KiB of a readable regular output file or otherwise the last 8 KiB of log, with secret ranges detected over the complete source before selecting the fragment; sources over 20 MiB produce an omission note instead.

The default timeout is one hour, concurrency one (maximum eight), and retained
task count 100. At capacity, the oldest finished record, logs and worktree are
removed before accepting a task; its Git branch remains. If all retained tasks
are queued/running, new tasks are rejected. Keep old project definitions until
retention has removed their tasks. Task records are atomically written and
synced to `tasks.json`; persistence failure stops dispatch and fails closed.
An exclusive lock prevents two runners from using the same state directory.

## Risks and maintenance

The runner account and agent sandbox are the security boundary after a task is
approved. An agent can interpret a malicious prompt as a request to run commands.
Worktrees separate edits, but do not isolate credentials, the shared Git object
store, other repositories, network access or other tasks under the same UID.
A compromised HTTP process can bypass its own approval logic and submit any
allowlisted task over the socket. Keep the runner's account permissions narrow,
validate the agent sandbox, and use separate runners/accounts for different trust
levels. Never give this account privileged credentials or trusted deployment
hooks. An agent can deliberately escape a process group with OS mechanisms if
its sandbox permits them; process groups are lifecycle control, not confinement.

Prompts, raw logs, output files and commits can contain sensitive information.
Protect state, repositories and backups; API redaction is best effort. Branches
remain after retention and therefore require a separate operator cleanup policy.
File/log caps and task count limits do not bound total repository growth, disk
space or all CPU/memory use. Apply account/service resource limits and monitor
storage. Review code and dependencies before deploying this pre-release feature.
