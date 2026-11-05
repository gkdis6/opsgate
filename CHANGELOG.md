# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
GitHub releases carry generated release notes.

## [Unreleased]

### Changed

- Require web approval by default wherever available on HTTP; explicit `approval.require_web: false` opts in to elicitation, which lets a compromised client approve its own writes. Stdio keeps elicitation unless `require_web: true` is explicit.

### Added

- Independent watch outputs for direct Discord embeds and custom JSON webhooks, with body templates, headers, credential files and retries for HTTP 429. Existing webhook and SMTP settings remain supported.
- Independent SMTP email delivery for `opsgate watch`, alongside or instead of webhooks, with mandatory verified TLS, separate batching and retries, and optional email-only warning filtering. Alerts are not stored and are lost after retries; names and redacted summaries pass through a mail provider outside the cluster. Port 465 uses implicit TLS; every other port requires STARTTLS. Use a separately revocable dedicated credential (such as an app password) and check account limits, especially for shared accounts: one message per batch interval can mean 288/day at 5m or 96/day at 15m.
- MCP over stdio and authenticated Streamable HTTP, including the sessionless `2026-07-28` protocol, with bounded output, redaction and JSONL auditing.
- Kubernetes inspection across one or more clusters, with allowlists, namespace denies, scopes and per-user/token tool restrictions.
- Builtin OAuth 2.1 with PKCE, consent, GitHub/OIDC login, passkeys, registration invites, refresh rotation and optional persistence.
- External JWT and digest-configured static bearer authentication, plus client registration policy and admin review.
- Approved Kubernetes rollout restarts, scaling and pod deletion.
- Argument-bound, expiring, single-use approvals through MCP elicitation or browser fallback, with optional approval windows.
- Kubernetes deployment defaults, `opsgate init` and effective configuration display.
- Experimental Markdown note tools and a separate development task runner with approvals and ownership checks.
- Experimental Kubernetes watch alerts with batching, deduplication and optional webhook signatures.
- Release archives, container images, signatures, SBOMs, provenance, Kubernetes deployment files and the dev-runner systemd unit.

### Removed

- Removed the host provider, `mode: host`, the `opsgate init` flags `-mode`, `-units` and `-kubeconfig`, and the server and watch systemd units. opsgate now targets Kubernetes.
