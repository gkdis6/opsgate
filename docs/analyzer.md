# Automatic alert investigation

`opsgate-analyzer` is an optional, separate process. It receives signed watch
batches, uses a function-calling model to query a read-only opsgate MCP server,
and posts a report to Discord. It is experimental. The normal watch notification
and interactive MCP clients continue to work without a model or analyzer.

```text
watch ──→ Discord (immediate notification)
      └─→ analyzer /alerts → model ↔ read-only opsgate MCP → Discord report
```

## Model backends and credentials

Build both executables with `make build`. Copy [analyzer.example.yaml](../analyzer.example.yaml)
to your own configuration. `model.backend` selects `openai_compatible` (default)
or `claude_cli` or `codex_cli`. There is no automatic fallback or simultaneous model execution.
Explicitly select a model. For `openai_compatible`, the model must support the Chat
Completions API's function tools, `max_completion_tokens`, `parallel_tool_calls`
and `usage.total_tokens`. The model endpoint is a full URL, defaulting to
`https://api.openai.com/v1/chat/completions`; no model name is chosen implicitly.
Compatible providers must support these fields; compatibility is not guaranteed
for every model. The function-calling loop follows the
[OpenAI documentation](https://developers.openai.com/api/docs/guides/function-calling).
No model SDK dependency is required. Other direct API protocols, including
Responses and Anthropic Messages, are not implemented in this version.

Provide protected files for a shared HMAC key (at least 16 bytes), an MCP
bearer token and a Discord incoming webhook URL. The API backend also requires
a model API key file; CLI backends use the execution account's CLI
authentication instead and do not require `model.api_key_file`. Files are read
and trimmed at startup, never printed or passed as shell arguments by the
analyzer. Restart after rotation. The Discord URL must use
`https://discord.com/api/webhooks/ID/TOKEN` without query parameters or fragments.

### API backend

```yaml
model:
  backend: openai_compatible
  endpoint: https://api.openai.com/v1/chat/completions
  name: YOUR_FUNCTION_CALLING_MODEL
  api_key_file: /etc/opsgate-analyzer/model-api-key
```

### Claude Code CLI backend

```yaml
model:
  backend: claude_cli
  name: sonnet
  claude:
    binary: /absolute/path/to/claude
    config_dir: /absolute/path/to/.claude
    max_turns: 3
    max_budget_usd: 0.5
```

See [the full CLI example](../analyzer.claude.example.yaml). `binary` and
`config_dir` are optional: the executable is resolved from PATH once at startup,
and the CLI uses its normal auth directory if no override is set. Explicit paths
must be absolute. Run `claude auth status` as the same OS account that runs the
analyzer. Existing CLI login or supported auth environment variables can be used;
login expiry, refresh and account usage limits still apply. The auth directory
may need writes for token refresh. The default opsgate image does not install
Claude Code or contain a login; provide them in the execution environment. The
local Kubernetes helper supports API mode and statically linked native CLI
binaries with an explicitly mounted auth directory.

The CLI is used as a model adapter: it returns a structured choice of function
or final answer, and the shared engine makes all MCP calls after checking policy.
It never gets the MCP bearer token or direct MCP servers. Every invocation uses
`claude -p` with JSON-schema output, an empty native tool list, strict empty MCP
configuration, restricted mode, disabled hooks/skills/session history and a
fresh temporary working directory. User/project settings are excluded; managed
organization settings can still apply. `--bare` is intentionally not used because
it skips subscription login. These flags are documented in the
[Claude Code automation guide](https://code.claude.com/docs/en/headless).
This is a constrained subprocess, not an OS sandbox for an untrusted executable;
use an operator-controlled CLI binary and authentication directory.

Prompts arrive on stdin, output is bounded to 1 MiB, and raw stdout/stderr are
not copied into failure notifications. A timeout kills the process group on
Linux/macOS. `max_turns` bounds internal CLI turns per model invocation (1–8);
`max_budget_usd` bounds the CLI's estimated spend per invocation, not per incident
or the account bill. The incident may make up to `max_rounds` invocations.
`max_completion_tokens` is passed through `CLAUDE_CODE_MAX_OUTPUT_TOKENS` for
individual CLI requests. Usage includes input/output and cache tokens; missing
structured output or usage is an error. Explicit HTTP and structured-output
retry settings are minimized; shared investigation deadlines still apply.
Restart the analyzer to change backend; the existing config remains API-compatible.

### Codex CLI backend

```yaml
model:
  backend: codex_cli
  name: YOUR_CODEX_MODEL
  codex:
    binary: /absolute/path/to/codex
    config_dir: /absolute/path/to/.codex
```

See [the full Codex example](../analyzer.codex.example.yaml). Paths are optional:
`binary` defaults to startup PATH lookup and `config_dir` to the CLI's auth
location; an explicit directory is supplied as `CODEX_HOME` in the child process.
Run `codex login status` as the runtime account. Existing ChatGPT login or
supported CLI API-key environment variables may be used. Auth files can need
writes during refresh. The default image does not bundle Codex or a login.
The helper's CLI hostPath deployment requires a native static ELF binary and
runs under the auth directory owner's non-root UID. Mount the auth directory
only in the analyzer container and protect it as credential material.

The adapter uses `codex exec --json --output-schema` with ephemeral sessions,
read-only sandboxing, no user config/rules, disabled hooks/plugins/apps/skills,
no direct MCP connections and disabled collaboration, shell and image tools.
A per-invocation exact-model catalog removes model-owned shell/patch tools and
supplies the model-adapter instructions. The model metadata format and config
keys are checked against Codex CLI 0.166.0. Use a CLI that supports these flags;
unknown/incompatible config fails the investigation. Managed requirements still
apply. The catalog changes local tool metadata, not account access to the model.
The implementation follows the [non-interactive CLI contract](https://learn.chatgpt.com/docs/non-interactive-mode)
and [configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference).

Codex returns requested function names and arguments as structured text; only
the shared engine can execute them as MCP calls. The JSONL reader permits agent
messages and reasoning events, and cancels the process on native-tool, failed or
unsupported events. Output is capped at 1 MiB and the process group is killed
on timeout on Linux/macOS. Input/output token usage comes from `turn.completed`;
cached input is already included and is not counted twice. Missing usage,
malformed JSON, incomplete streams or missing schema fields fail closed.

Codex has no per-invocation dollar/turn flag configured here. Shared time,
context-byte, round, MCP-call and reported-token limits apply. The output-token
allowance is included in the prompt, not enforced by a Codex output-token flag;
byte/context estimates do not include all CLI-internal overhead. These limits
are not an exact billing cap. No native tools or model API requests are needed
for `-check`: it verifies paths and executable availability, not login freshness.

```sh
opsgate-analyzer -config analyzer.yaml -check
opsgate-analyzer -config analyzer.yaml
```

`-check` validates configuration and reads credentials without contacting the
model, MCP or Discord. Normal startup opens the receiver and a single worker.
`GET /healthz` checks process health; `GET /readyz` also connects to MCP and checks
that allowed read tools are available. Neither endpoint calls the model.

## Read-only MCP identity

Use a dedicated MCP server for unattended investigations. Keep existing public
OAuth/passkey settings unchanged. A static token with only `ops:read`, a server
tool allowlist and Kubernetes read-only RBAC restrict this server. In a pod,
bind MCP to `127.0.0.1:8788` and run the analyzer as a sidecar, so unencrypted
bearer authentication stays on loopback. A remote MCP endpoint must use HTTPS.
Only CLI backends launch their configured executable, with fixed flags
and no shell. MCP tools cannot request subprocess execution.

A minimal reader configuration (replace the token hash before use) is:

```yaml
mode: kubernetes
server:
  tools:
    allow: [k8s_*]
http:
  listen: 127.0.0.1:8788
  public_url: http://127.0.0.1:8788
auth:
  mode: static
  static:
    - name: analyzer
      sha256: REPLACE_WITH_SHA256_OF_MCP_TOKEN
      scopes: [ops:read]
      tools: [k8s_*]
approval:
  web: false
  require_web: false
providers:
  kubernetes:
    namespaces: [apps]
    allow_logs: true
```

If Notes are enabled, mount the notes directory read-only, disable
`providers.note.allow_write` and add `note_*` to the reader's tool policies.
The analyzer searches relevant notes before letting the model select additional
queries when the call budget allows it. Without Notes, the investigation uses Kubernetes evidence alone.

Independently of MCP annotations, the analyzer allows only these exact tool
names: `k8s_clusters`, `k8s_overview`, `k8s_list`, `k8s_get`, `k8s_events`,
`k8s_logs`, `k8s_service_check`, `k8s_remediation_options`, `action_link`, `note_search`, `note_read`, `note_recent`,
`note_list`, `note_links`, `note_tags`. They must also advertise `readOnlyHint`.
A model-selected call is checked again before execution. Write tools are never
offered or called; no approval window can expand this list.

## Connect watch

Add a native JSON output and mount the same HMAC key in both processes:

```yaml
watch:
  outputs:
    - name: analyzer
      type: webhook
      url: http://opsgate-analyzer.opsgate.svc.cluster.local:8080/alerts
      allow_private_http: true
      problems_only: true
      secret_file: /etc/opsgate/analyzer-hmac/key
```

Do not customize this output's body: `/alerts` accepts the native `alert.Batch`
JSON with `source: opsgate`, `sent_at` and structured `alerts`. The receiver
checks HMAC-SHA256 over the exact body and accepts batches sent no more than
10 minutes ago or one minute in the future. Bodies are limited to 1 MiB and
500 alerts. Network access to this endpoint should be limited to watch; the
reference deployment has no public ingress. Use TLS outside a trusted private
network. Authentication does not make log, event or note text trusted instructions.

[The Kubernetes reference](../deploy/kubernetes/opsgate-analyzer.yaml) starts at
zero replicas until configs and credentials are supplied. Set `listen: 0.0.0.0:8080` in
`analyzer.yaml`, because its Service and readiness probe connect through the pod address. It uses a distinct
ServiceAccount bound to `opsgate-readonly`. Create the `opsgate-analyzer`
ConfigMap containing `analyzer.yaml` and `reader.yaml`, an `opsgate-analyzer`
Secret with `hmac`, `mcp-token` and `model-api-key`, and an
`opsgate-watch-discord` Secret with `url`. Paths in `analyzer.yaml` must match
`/etc/analyzer-credentials/*` and `/etc/analyzer-discord/url` in the manifest.
Pin an image built from a release that includes `/opsgate-analyzer`; the Docker
image retains `/opsgate` as its default entrypoint. Keep one replica because
queue, replay protection and cooldown are local to the process.

## Queue, limits and reports

| Setting | Default | Accepted values |
| --- | --- | --- |
| `report.language` | `ko` | `ko` or `en`, report explanations and labels |
| `report.suggest_actions` | `true` | Request concrete proposals; never execute them |
| `listen` | `127.0.0.1:8080` | TCP listen address |
| `secret_file` | required | Shared HMAC key file |
| `mcp.endpoint` | required | HTTPS or loopback HTTP; no credentials/query/fragment |
| `mcp.token_file` | required | Read-only MCP bearer token file |
| `model.backend` | `openai_compatible` | `openai_compatible`, `claude_cli` or `codex_cli` |
| `model.codex.binary` | PATH lookup | Optional absolute path to installed Codex CLI |
| `model.codex.config_dir` | CLI default | Optional absolute auth directory, passed as child CODEX_HOME |
| `model.claude.binary` | PATH lookup | Optional absolute path to installed Claude CLI |
| `model.claude.config_dir` | CLI default | Optional absolute auth/config directory |
| `model.claude.max_turns` | `3` | 1–8 internal turns per CLI invocation |
| `model.claude.max_budget_usd` | `0.5` | Greater than 0 and at most 10, estimated per CLI invocation |
| `model.endpoint` | OpenAI Chat Completions endpoint | API backend only; same URL policy as MCP |
| `model.name` | required | Explicit function-calling model |
| `model.api_key_file` | required for API backend | Model API key file; unused for CLI |
| `discord_url_file` | required | Discord webhook URL file |
| `warnings_only` | `true` | Ignore informational changes when true |
| `batch_window` | `10s` | 1s–1m |
| `cooldown` | `15m` | 1m–24h |
| `timeout` | `2m` | 1s–10m per investigation |
| `queue_size` | `64` | 1–256 pending resources |
| `max_tool_calls` | `12` | 1–50, including the initial note search |
| `max_rounds` | `8` | 2–20 model calls; final turn reserves a summary |
| `max_completion_tokens` | `1500` | 128–8192 per model response |
| `max_total_tokens` | `100000` | 4096–1000000 reported tokens per investigation |
| `max_context_bytes` | `131072` | 4096–1048576 bytes per model request |
| `tool_output_bytes` | `16384` | 1024–65536 bytes per tool result |

Repeated alerts are grouped by cluster/kind/namespace/name during the batch
window, retaining up to 50 distinct reason/summary pairs. Excess alerts are
counted as dropped. Exact batch replays are suppressed in memory for 10 minutes;
resource cooldown starts when investigation starts, including failed investigations.
The receiver retains only source alert evidence, dropping localized `display` and unused batch `text`, `actions` and `language`. Fixed field caps match watch: cluster/namespace 63 bytes, kind 64 bytes, name 253 bytes, reason/summary/hint 301 Unicode characters and 1203 bytes, and restart URL 4096 bytes (restart identity uses the same caps). Oversized fields return 413. Retention is charged as serialized batch/key bytes plus conservative struct/map allowances, with fixed 64 KiB per job and 8 MiB globally, including replacements; dequeuing releases the charge. These budgets fit the reference pod's 256 MiB limit but do not bound every transient allocation or concurrent request. A full count or byte budget returns 503 atomically, so watch can apply its bounded retry policy. One worker
processes incidents sequentially; no automatic model retries are performed.

Requests are preflighted with a conservative byte estimate plus the next response
allowance, and reported `usage.total_tokens` is accumulated. Missing usage,
exceeded limits, an incomplete response or timeout stops the investigation.
These controls bound work, but are not a provider billing or exact dollar-spend
limit. `max_total_tokens` must exceed `max_completion_tokens`. The final summary requires at least one successful Kubernetes read.

Reports ask the model to separate confirmed facts, hypotheses and outstanding
checks, and append actual tool names, redacted arguments and evidence IDs. The footer shows reads, tokens, total investigation wall time (including tool calls), and cumulative time inside model calls across API and CLI backends; for example `3 reads · 12345 tokens · 1m12s (model 48s)`. Durations round to whole seconds with a minimum of 1s; below a minute only seconds are shown. Failure/timeout notices also show elapsed total time once an investigation starts.
The model can still misinterpret evidence; a citation is not proof of correctness.
Failures produce an explicit failure/evidence-insufficient report rather than a
claimed cause. No system changes are performed. Discord output groups complete paragraphs within 1900 UTF-16 units, with automatic mentions disabled.
Markdown links and code blocks that fit a message stay together. Oversized code blocks
continue at line boundaries with balanced fences; long prose falls back to line or word boundaries. Network/429/5xx
notification failures receive bounded retries, which can duplicate messages.

Alert string values are masked before their final JSON encoding for every model backend, including quoted assignments and escaped text; tool output is masked before model use. Reports are masked once before both the optional forward receiver and Discord. This is not a guarantee that all sensitive
content is detected: allow access only to data you intend the selected model
provider and Discord to receive. Logs contain status, counts, timings and
notification success, not provider bodies or credentials. The reference analyzer pod disables automatic ServiceAccount token mounting and projects the token, CA and namespace only into the reader; the analyzer uses its MCP token and does not need direct Kubernetes credentials. The queue and replay
cache are not persistent, pending work is lost on restart, and failed notifications
are logged rather than stored. Durable jobs, multi-worker coordination, automatic
remediation and other model protocols are outside this first version.

## Display language and proposed actions

Immediate Discord notifications use per-output `watch.outputs[].language`
(default `en`); analyzer reports use `report.language` (default `ko`). Both accept
`ko` and `en`. Set both if you want the whole Discord flow in one language:

```yaml
# Watch configuration
watch:
  outputs:
    - name: discord
      type: discord
      language: ko
      url_file: /etc/opsgate/discord/url
```

```yaml
# Analyzer configuration
report:
  language: ko
  suggest_actions: true
```

Korean Discord display explains known waiting/termination/node/resource states,
including ImagePullBackOff and OOMKilled, in Korean and gives fixed investigation
hints. It is not an arbitrary log translator: error codes, resource names and
original messages are preserved separately. Unknown states receive a generic
investigation explanation rather than an invented cause. Native webhook JSON
`reason`/`summary` are unchanged. Existing n8n Telegram templates are independently
managed. The model is explicitly instructed to explain report causes in the
selected language while preserving quoted evidence and identifiers.

`report.suggest_actions` defaults to true. Reports request concrete operator
proposals with target identity, evidence, an approval link obtained through
`action_link`, impact/risk, verification and rollback. Unknown parameters require
further checks before proposing a fix. A restart is not proposed as a
fix for a missing image or configuration. These are model-generated proposals
requiring review, not executed actions; neither language nor action suggestions
expand MCP access. Set this option to false to request checks without an action
proposal section. Restart the process after configuration changes.

Successful Discord sends log only message/channel IDs for delivery verification,
never message bodies or webhook tokens. `wait=true` is used to require a created
message receipt before reporting notification success.


## Immediate alerts and separate analysis

Keep watch notifications connected to their existing destinations and add the analyzer as
an independent output. Watch does not wait for the model. Analysis completion or failure
produces a separate report; it cannot delay the initial incident alert.

Optional `forward.url` and `forward.secret_file` deliver the report as a signed webhook
(`source: opsgate-analyzer`, `sent_at`, `language`, `text`, optional `actions`). Use a different
receiver path from the immediate watch webhook. Verify HMAC-SHA256 over the raw body and
freshness before rendering. `forward.allow_private_http` enables private HTTP receivers.
Global `watch.language: ko|en` adds localized `display` fields to webhook alerts; retain raw
`reason` and `summary` for diagnostics and deduplication.

`report.approval_url` enables verified image restoration links when the Kubernetes server
also opts in with `restore_image` resource patterns and `action_approval_url`. The read tool
checks Pod ownership and a non-deleting Deployment-owned ReplicaSet with an actual non-deleting ready Pod owned by that exact ReplicaSet UID. Its target container must be ready/running, its spec image must equal the RS template, and its status image reference must match with only Docker Hub/default-tag spelling normalization. A SHA-256/SHA-512 runtime imageID is required; digest-pinned templates must match that runtime digest too. Checks are repeated at apply time; scans fail closed above 500 ReplicaSets or 500 Pods in the namespace. Readiness is not application health, and tags remain mutable. Only its verified links become
buttons. The browser uses existing authentication, passkey verification, fresh plan review,
and one-use approval. The write rechecks state and changes only the chosen container image.
No approval window is opened. Arbitrary model commands and model-generated URLs never become
executable actions. Without a verified candidate, the report identifies the outstanding checks.


Catalog buttons also support restart, scale and controller-owned pod deletion.
`action_link` is in the default read-only tool allowlist. The model may propose a
fix only by calling it with verified arguments, must prefer the least invasive
action, and must explain that a person approves every action. The tool returns a
link only after the action's side-effect-free Plan accepts the current target;
it never writes and needs no write scope. Enable the action allowlist and
`action_approval_url` on the MCP server, and set `report.approval_url` to the same
origin. Restrictive MCP token tool policies must permit `action_link` and the
catalog tools it may plan; the token can remain `ops:read` only. For the dedicated
reader deployment above, add `action_link` to both tool allowlists and enable
the matching action resource allowlists with `action_approval_url` pointing to
the public approval server. Keep the reader's Kubernetes RBAC read-only. The
public server independently needs those actions enabled and write permissions;
it checks the approving user's own access and current plan.

Buttons are collected from `k8s_remediation_options` and `action_link` results,
limited to five distinct URLs on the configured HTTPS origin. The analyzer
accepts `/approve/action/{catalog-tool}` and the old image-restore alias, checks
the catalog arguments, and uses the catalog label in the report language.
`source=ai` adds “AI suggestion: ” / “AI 제안: ”. Generated prose URLs never
become buttons. Sign-in, the approver's write scope/tool policy, fresh review,
approval and re-plan checks remain mandatory. See [approval](approval.md).

Reports collect the same current-state rule choices as the browser choices
page, including restore-before-restart for failing releases, temporary OOM
restarts and stuck/unreachable controller-owned pod deletion. Each choice must
pass its enabled catalog entry, target allowlist and current Plan. Rule buttons
use the catalog label; `action_link` proposals use **AI suggestion** / **AI 제안**
labels, and both kinds can appear together. Existing five-button limits remain.
Every pod report also includes **All choices** / **모든 선택지 보기** pointing to
`/approve/options/{cluster}/{namespace}/{pod}` on `report.approval_url`, with a
workload hint when available. This navigation URL is built from bounded alert
identity fields, never copied from model prose or an alert-supplied URL. It
remains available when analysis fails; sign-in and fresh user policy/state
checks still apply. A recovered incident may have no applicable choices.
The receiver also caps `options_url` at 4096 bytes and workload hint fields at
the corresponding restart identity limits.
