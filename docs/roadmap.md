# Roadmap

[한국어](ko/roadmap.md)

The features below are implemented through v0.2.0. They have not yet been security-reviewed by third parties. There are no promised dates for future work.

## Done

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
- Experimental dev provider and separate runner: configured projects/agent commands, approved task starts, requester ownership/admin access, logs/diffs, cancellation and retention.
- A deployment mode (`mode: kubernetes`) that fills in defaults, `opsgate init` to write a short configuration and `opsgate config show` to print the effective one.
- Experimental `opsgate watch`: a separate process that watches pods, workloads, nodes and ConfigMap keys and posts batched, deduplicated, optionally HMAC-signed alerts to a webhook.
- Experimental `opsgate-analyzer`: signed watch alerts, bounded read-only MCP investigations with a selected OpenAI-compatible API, Claude Code CLI or Codex CLI backend, Discord reports and optional signed webhook forwarding, Korean/English reports and action proposals that are never executed.
- Allowlisted, human-approved `k8s_restore_image` restores a Deployment container image from a currently ready owned ReplicaSet; read-only `k8s_remediation_options` provides approval links for verified restoration candidates.
- Independent watch outputs for Discord and custom JSON webhooks, with body templates, headers, credential files and HTTP 429 retries; SMTP email delivery with verified TLS, separate batching and retries.
- Korean/English watch display preserving original error codes/messages, and per-output `problems_only` warning filtering.
- Web approval required by default wherever available on HTTP; `approval.require_web: false` permits elicitation. Stdio retains elicitation unless `require_web: true` is explicit.

## Remaining ideas

These are ideas, not implemented commitments; scope remains subject to security review.

- Docker and PostgreSQL providers with narrow permissions and bounded inputs.
- Configuration reload.
- Client ID Metadata Documents (CIMD).
- Per-tool rate limits.
