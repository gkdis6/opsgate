# Security policy

opsgate stages 1–4 are implemented and pre-release, including remote HTTP, authentication and human-approved writes. It has not yet been security-reviewed by third parties. Review the implementation you deploy.

## Threat model

The intended boundary is a constrained MCP interface with read tools and explicitly approved write actions: explicit provider opt-in, unit and namespace allowlists, namespace denies, scope checks, bounded calls and output, best-effort redaction and per-call audit records. No arbitrary shell or exec interface is exposed. Kubernetes Secrets are excluded; ConfigMap values are hidden by default. These controls reduce accidental disclosure and the operations a client can request through opsgate.

They do not make untrusted output safe. Logs, events, annotations and resource names can contain prompt injection. Treat output as data, never instructions. A malicious or compromised client can request every operation its configured scopes and allowlists permit, copy returned data, or attack other connected tools. MCP annotations are hints, not authorization. Stdio trusts the OS account launching the server; its configured user name is only a label.

Over-broad RBAC or kubeconfig permissions expand the consequences of bugs and compromise. Use dedicated credentials with only required read and optional write permissions, and review the supplied RBAC manifests. Kubeconfig `exec` and `auth-provider` credential plugins are rejected. Keep kubeconfig files and config paths under trusted ownership. Namespace allow/deny rules filter namespaced resources and Namespace objects by name; they do not restrict Nodes or PersistentVolumes, including nodes in `k8s_overview`. Omit `namespace` for cluster-scoped kinds. The supplied ClusterRoleBinding grants cluster-wide reads; prefer namespace RoleBindings where possible. Providers and their credentials must not be attacker-controlled. Running as root is discouraged; use an unprivileged account with only the journal and cluster access required.

The [README](README.md#redaction-and-object-sanitization) details the implemented text patterns and object sanitization. ConfigMap data and binaryData values are hidden by default. Pod and workload literal environment values are masked for case-insensitive name substrings pass, secret, token, key, credential, auth, dsn, private, cert and database_url; other values may remain visible. `valueFrom` references are visible and are not resolved. Redaction is best effort, not a guarantee that secrets cannot escape. Logs, metadata and process information can contain credentials in unexpected formats. Avoid collecting secret material in the first place. Protect audit logs too: arguments and errors can retain sensitive information. Auditing is not a tamper-proof ledger and does not guarantee durable delivery when writes fail. Context deadlines require handlers and dependencies to cooperate; output limits do not eliminate all resource exhaustion risks.

opsgate does not defend against a compromised host, kernel, server binary, dependency, administrator or credential store. It does not sandbox the client or revoke the launching user's permissions. It does not protect against stolen bearer tokens used within their lifetime, denial of service at the network edge, or disclosure by an authorized client.

## Remote exposure and authorization

HTTP exposes `/mcp` with Protected Resource Metadata. Authentication is mandatory; HTTPS is required for the public origin except loopback development. Terminate TLS at a trusted proxy, keep the backend private and forward the discovery, OAuth and approval routes described in [deployment](docs/deploy.md). Do not log bearer tokens, cookies, authorization codes, client secrets or approval URLs. CORS and MCP annotations are not authorization boundaries.

Builtin OAuth addresses these threats:

- Authorization-code interception: PKCE S256 is mandatory; no implicit or password grant.
- Redirect theft: exact registered redirect URI matching on authorization and code redemption.
- DCR abuse: bounded bodies, metadata and client capacity, registration rate limiting, restricted grants and authentication methods, HTTPS redirects or loopback HTTP and supported native URI schemes. At capacity, unused registrations older than 10 minutes are evicted oldest first, excluding clients with tokens or outstanding authorizations. Registration does not grant tool access.
- Phishing through DCR: the consent screen shows the requesting client, redirect destination and scopes. Client names are untrusted; consent is not proof of client ownership. Humans must inspect the destination and requested permissions. Before consent, authorization errors are rendered locally; Deny after the consent screen may redirect.
- Code and refresh replay: single-use codes, refresh rotation, reuse detection and revocation of descendant token families.
- Login CSRF: upstream login state is tied to the browser; consent and browser approval use CSRF protection. OIDC also validates identity tokens.
- Credential disclosure from state storage: opaque access/refresh tokens and client secrets are stored as hashes, not raw credentials. Secret comparisons use constant-time checks. Protect state files and backups regardless; state persistence errors fail closed.

OAuth responses are sent after releasing the shared state lock. OAuth GET routes reject bodies and POST body reads have a 10-second deadline. Pending authorizations evict oldest entries at 20 per client or 1000 overall; browser approval logins use a separate pool of 100. HTTP access logs mask `/approve/<id>` as `/approve/*` and omit query strings.

Builtin login accepts only configured GitHub logins, verified OIDC email identities or exact, case-sensitive `oidc:<sub>` identities from the configured issuer. Google email identities additionally require a Gmail/Googlemail domain or an `hd` claim matching the email domain; other Google addresses must use an immutable subject. GitHub login renames can change who matches a login allowlist: prefer verified email via OIDC or keep the GitHub allowlist current. External mode verifies JWT signatures via discovered JWKS and checks issuer, audience, expiry and allowed signing algorithms. Issuer, discovery and JWKS URLs require HTTPS, except loopback HTTP. Optional external user entries restrict identities and scopes. Static mode stores only SHA-256 token digests; it has no interactive OAuth flow.

Writes need a separate action allowlist, `ops:write`, OS/RBAC permission and human confirmation. HTTP also requires `ops:read`. Planning validates the target and produces the summary before execution. Elicitation obtains an in-client decision through MCP input requests. With builtin auth and `approval.web: true`, clients without elicitation receive a browser approval link; the approver signs in upstream as the same user. The approval expires, is single-use and binds the exact arguments and plan. A retry with changed arguments cannot reuse it. Without confirmation, writes are denied.

A compromised client UI can auto-approve elicitation; the server cannot establish that a real person clicked a client-controlled prompt. Prompt injection can still cause the model to *request* writes. Humans must read the summary and target before approving. Approval does not make an operation safe or guarantee the target's state remains unchanged; OS privileges, RBAC and narrow allowlists remain essential. Audit records include plans, approval methods and pending outcomes, but audit delivery is not a prerequisite for execution.

## Supported versions

While the project is 0.x, security fixes and support are best effort, focused on the latest development version. There is no guaranteed maintenance window for older 0.x releases.

## Reporting a vulnerability

Please report privately using [GitHub Security Advisories](https://github.com/gkdis6/opsgate/security/advisories/new): choose **Report a vulnerability** on the repository's Security page. Include the affected revision, configuration with secrets removed, impact and reproducible steps. Do not open public issues for vulnerabilities or publish credentials. Maintainers will coordinate investigation and disclosure privately; response times are best effort.
