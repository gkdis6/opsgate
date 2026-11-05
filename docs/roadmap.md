# Roadmap

[한국어](ko/roadmap.md)

The features below are implemented for the first public release. They have not yet been security-reviewed by third parties. There are no promised dates for future work.

## Done

- Passkey sign-in for builtin auth with upstream provider `passkey` and one-time registration invites via `opsgate passkey invite`.
- Stateless serving of the sessionless MCP protocol `2026-07-28`, required by ChatGPT.
- Stdio Kubernetes inspection, allowlists, namespace denies, scope checks, bounded output, redaction and JSONL audit persistence.
- Streamable HTTP at `/mcp`, mandatory authentication and Protected Resource Metadata.
- Builtin OAuth 2.1: authorization code with PKCE S256, DCR, consent, upstream GitHub/OIDC login (including Google), hashed opaque tokens, refresh rotation/reuse detection and optional state persistence.
- External JWT authentication using issuer discovery/JWKS and static bearer tokens configured by digest; `-gen-token` helper.
- Kubernetes rollout restart, scale and pod deletion, gated by separate action allowlists, `ops:write` and human confirmation.
- MCP elicitation across protocol versions and browser approval fallback with same-user upstream login, expiry and single-use argument-bound approvals.
- GoReleaser releases, cosign keyless signatures, SBOMs, provenance attestations, container image and Kubernetes manifests.
- Tool registration policy and per-user/token tool restrictions; redirect-host registration policy, pending-client admin review and optional notifications.
- Optional approval windows, in memory and bound to user, tool and target; off by default.
- Experimental note provider: bounded Markdown folder (Obsidian-compatible) reads/search/links/tags and approved create/append/replace/trash operations.
- Experimental dev provider and separate runner: configured projects/agent commands, approved task starts, requester ownership/admin access, logs/diffs, cancellation and retention.
- A deployment mode (`mode: kubernetes`) that fills in defaults, `opsgate init` to write a short configuration and `opsgate config show` to print the effective one.
- Experimental `opsgate watch`: a separate process that watches pods, workloads, nodes and ConfigMap keys and posts batched, deduplicated, optionally HMAC-signed alerts to a webhook.

## Remaining ideas

These are ideas, not implemented commitments; scope remains subject to security review.

- Docker and PostgreSQL providers with narrow permissions and bounded inputs.
- Configuration reload.
- Client ID Metadata Documents (CIMD).
- Per-tool rate limits.
