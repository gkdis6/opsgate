# Roadmap

[한국어](ko/roadmap.md)

There are no promised dates for future work.

## Direction

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

In order. Each step still goes through security review.

1. **Remove the Dev provider** (`dev_*` tools, `opsgate dev-runner`, the
   `dev:run` scope). Coding agents are outside the focus above. Done,
   unreleased; see the [changelog](../CHANGELOG.md).
2. **Common action gate.** An action catalog shared by alerts and MCP tools
   (action catalog and links: done, unreleased);
   execution on approval: done, unreleased; an
   approval inbox page with Web Push, with messenger alerts linking to it.
3. **More choices per failure.** Rule-based suggestions for more failure states
   (for example `OOMKilled` and `Pending`), and analyzer proposals that become
   buttons when they match the catalog.
4. **Alert groups by namespace,** each with its own outputs, level
   (warnings only or all) and batching.
5. **Optional host provider** for systemd units and Docker or Compose
   containers: detection and restart, off by default, with a narrow privilege
   model instead of direct root or Docker socket access.
6. **Undo buttons** for reversible actions, such as returning to the previous
   replica count or image.

## Done

The features below are implemented through v0.3.0. They have not yet been
security-reviewed by third parties.


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
- Experimental `opsgate watch`: a separate process that watches pods, workloads, nodes and ConfigMap keys and posts batched, deduplicated, optionally HMAC-signed alerts to a webhook.
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
