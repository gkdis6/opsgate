# Roadmap

v0.1 is in progress: stdio-only, read-only Linux and Kubernetes inspection. The shared wrapper checks scopes, supplies context deadlines and calls redaction, truncation and audit APIs. Provider registration, audit persistence and redaction are still TODO stubs in this checkout; the READMEs describe the intended v0.1 tool contract. Complete and test these before relying on the security model.

Everything below is **planned, not implemented**. There are no promised release dates.

- Streamable HTTP transport for remote MCP clients, including claude.ai and ChatGPT.
- A built-in OAuth 2.1 authorization server: authorization code with PKCE S256 only, refresh-token rotation, Dynamic Client Registration, and per-client consent screens. Delegate upstream login to GitHub, Google or any OIDC provider. Store opaque tokens hashed rather than in plaintext.
- An external authorization mode validating JWTs from an existing authorization server via JWKS, including Keycloak, Authentik, Zitadel, Auth0 and WorkOS. Validate issuer, audience, expiry and allowed algorithms as well as signatures.
- Write actions: systemd restart and Kubernetes rollout restart, scale and pod deletion. Require explicit action/resource allowlists, the `ops:write` scope and human confirmation through MCP elicitation that the model cannot bypass. No write should be enabled by a scope alone.
- Docker and PostgreSQL providers with explicitly reviewed permissions and bounded inputs.
- Signed releases using GoReleaser, cosign and SLSA provenance.

Security review and tests must precede enabling new transports or write actions. Current configuration has no HTTP or OAuth settings; do not add speculative keys to deployed YAML.
