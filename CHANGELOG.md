# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
GitHub releases carry generated release notes.

## [Unreleased]

### Added

- MCP over stdio and authenticated Streamable HTTP, including the sessionless `2026-07-28` protocol, with bounded output, redaction and JSONL auditing.
- Host inspection and Kubernetes inspection across one or more clusters, with allowlists, namespace denies, scopes and per-user/token tool restrictions.
- Builtin OAuth 2.1 with PKCE, consent, GitHub/OIDC login, passkeys, registration invites, refresh rotation and optional persistence.
- External JWT and digest-configured static bearer authentication, plus client registration policy and admin review.
- Approved host service restarts and Kubernetes rollout restarts, scaling and pod deletion.
- Argument-bound, expiring, single-use approvals through MCP elicitation or browser fallback, with optional approval windows.
- Host/Kubernetes deployment defaults, `opsgate init` and effective configuration display.
- Experimental Markdown note tools and a separate development task runner with approvals and ownership checks.
- Experimental Kubernetes watch alerts with batching, deduplication and optional webhook signatures.
- Release archives, container images, signatures, SBOMs, provenance, systemd and Kubernetes deployment files.
