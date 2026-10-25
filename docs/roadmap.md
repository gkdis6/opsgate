# Roadmap

Stages 1–4 are implemented and pre-release. They have not yet been security-reviewed by third parties. There are no promised dates for future work.

## Done

- Stdio host and Kubernetes inspection, allowlists, namespace denies, scope checks, bounded output, redaction and JSONL audit persistence.
- Streamable HTTP at `/mcp`, mandatory authentication and Protected Resource Metadata.
- Builtin OAuth 2.1: authorization code with PKCE S256, DCR, consent, upstream GitHub/OIDC login (including Google), hashed opaque tokens, refresh rotation/reuse detection and optional state persistence.
- External JWT authentication using issuer discovery/JWKS and static bearer tokens configured by digest; `-gen-token` helper.
- Host service restart and Kubernetes rollout restart, scale and pod deletion, gated by separate action allowlists, `ops:write` and human confirmation.
- MCP elicitation across protocol versions and browser approval fallback with same-user upstream login, expiry and single-use argument-bound approvals.
- GoReleaser releases, cosign keyless signatures, SBOMs, provenance attestations, container image, hardened systemd unit and Kubernetes manifests.

## Remaining ideas

These are planned, not implemented; scope remains subject to security review.

- Docker and PostgreSQL providers with narrow permissions and bounded inputs.
- Configuration reload.
- Client ID Metadata Documents (CIMD).
- Per-tool rate limits.
