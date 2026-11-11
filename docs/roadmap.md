# Roadmap

[한국어](ko/roadmap.md)

There are no promised dates for future work.

## Direction

**AI proposes. Your passkey runs it.** Every change to the server becomes one
approval card with the exact plan, its source and whether it can be undone;
only a passkey approval runs it, and the card ends by saying whether the
service recovered.

People who run servers alone find out about outages late, and even when they
know the fix they often cannot reach the server. opsgate should alert them
first, offer the actions that fit the failure, and let them approve one with a
passkey wherever they are. Root-cause analysis with an AI client comes after the
service is back.

The work below follows these principles:

- **Actions come from a catalog.** A rule or the AI may suggest an action, but a
  button exists only for an action type the server defines and the allowlist
  permits. The AI chooses and fills in arguments; it cannot invent an action.
  Buttons show whether a rule or the AI suggested them.
- **No deletions in the catalog.** Restart, scale, image restore and deleting a
  pod that a controller recreates are in; deleting data or other resources is not.
- **Approving runs the action.** After a passkey approval the server rechecks the
  plan and runs it. Nobody has to go back to a chat to report the approval.
- **Approve anywhere.** Approval does not depend on one messenger; it works in
  any phone or desktop browser where passkeys work.

## Next

The six steps that followed from the direction above shipped in v0.4.0.
Sharpening the direction into the approval card exposed three gaps, closed in
v0.5.0 (see the [changelog](../CHANGELOG.md)):

1. **"Applied" is not "recovered".** After a web-approved Kubernetes action the
   card now watches the workload and reports Recovered or Not recovered yet,
   with a push. See [recovery check](approval.md#recovery-check).
2. **In-client approval contradicts "only a passkey runs it".** Builtin auth now
   defaults to web approval even without a `mode`.
3. **A long first setup loses solo operators.** `opsgate init -recover` writes
   both configurations and prints every remaining command.

Next candidates, without commitment: the same recovery check for host provider
actions (systemd units and Docker containers).

## Done

The features below are implemented through v0.8.0. They have not yet been
security-reviewed by third parties.

- Approvals opened from links, inbox notices and the link-signing key survive a
  restart; signed action links; Prometheus metrics; a stability policy; fuzz
  tests and an end-to-end test on a real cluster.
- Install from any page, an iPhone Home Screen hint and a test notification in
  the inbox.
- QR codes that open approval, inbox and choices pages and passkey invites on a
  phone.
- Recovery check after web-approved Kubernetes actions, with a push that names
  only the result.
- Web approval by default with builtin auth, with or without a `mode`.
- `opsgate init -recover` for phone recovery in one step.
- Official MCP Registry listing, documentation site and `llms.txt`.
- Approving runs the action: the server rechecks the plan and runs it once, as
  the original caller. Chat calls wait up to `approval.wait`; `approval_status`
  reads the result later.
- Action catalog shared by alerts, the analyzer and MCP tools (restart, image
  restore, scale, deleting a controller-owned pod), with rule or AI provenance
  on each button and the read-only `action_link`.
- Installable `/approvals` inbox with optional Web Push; a notification opens
  the newest waiting approval. Signed watch notices put alerts in the inbox.
- Current-state choices per failure (image failures, failing rollouts, repeated
  crashes or OOM, stuck or unreachable pods) on signed-in choices pages.
- Namespace alert groups with their own outputs, level and batching.
- Optional host provider for systemd units and Docker containers, off by
  default, using polkit and a socket proxy instead of root or the Docker socket.
- Undo links for scale and image restore.
- The experimental Dev provider was removed.
- Passkey sign-in for builtin auth with upstream provider `passkey` and one-time registration invites via `opsgate passkey invite`.
- Stateless serving of the sessionless MCP protocol `2026-07-28`, required by ChatGPT.
- Stdio Kubernetes inspection, allowlists, namespace denies, scope checks, bounded output, redaction and JSONL audit persistence.
- Streamable HTTP at `/mcp`, mandatory authentication and Protected Resource Metadata.
- Builtin OAuth 2.1: authorization code with PKCE S256, DCR, consent, upstream GitHub/OIDC login (including Google), hashed opaque tokens, refresh rotation/reuse detection and optional state persistence.
- External JWT authentication using issuer discovery/JWKS and static bearer tokens configured by digest; `-gen-token` helper.
- Kubernetes rollout restart, scale and pod deletion, gated by separate action allowlists, `ops:write` and human confirmation.
- MCP elicitation across protocol versions and browser approval with same-user upstream login, expiry and single-use argument-bound approvals.
- GoReleaser releases, cosign keyless signatures, SBOMs, provenance attestations, container image and Kubernetes manifests.
- Tool registration policy and per-user/token tool restrictions; redirect-host registration policy, pending-client admin review and optional notifications.
- Optional approval windows, in memory and bound to user, tool and target; off by default.
- Experimental note provider: bounded Markdown folder (Obsidian-compatible) reads/search/links/tags and approved create/append/replace/trash operations.
- A deployment mode (`mode: kubernetes`) that fills in defaults, `opsgate init` to write a short configuration and `opsgate config show` to print the effective one.
- `opsgate watch`: a separate process that watches pods, workloads, nodes and ConfigMap keys and posts batched, deduplicated, optionally HMAC-signed alerts to a webhook.
- Experimental `opsgate-analyzer`: signed watch alerts, bounded read-only MCP investigations with a selected OpenAI-compatible API, Claude Code CLI or Codex CLI backend, Discord reports and optional signed webhook forwarding, Korean/English reports and action proposals that are never executed.
- Allowlisted, human-approved `k8s_restore_image` restores a Deployment container image from a currently ready owned ReplicaSet; read-only `k8s_remediation_options` provides approval links for verified restoration candidates.
- Independent watch outputs for Discord and custom JSON webhooks, with body templates, headers, credential files and HTTP 429 retries; SMTP email delivery with verified TLS, separate batching and retries.
- Korean/English watch display preserving original error codes/messages, and per-output `problems_only` warning filtering.
- Web approval required by default wherever available on HTTP; `approval.require_web: false` permits elicitation. Stdio retains elicitation unless `require_web: true` is explicit.

## Remaining ideas

These are ideas, not implemented commitments; scope remains subject to security review.

- A PostgreSQL provider with narrow permissions and bounded inputs.
- Configuration reload.
- Client ID Metadata Documents (CIMD).
- Per-tool rate limits.
