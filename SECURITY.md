# Security policy

[한국어](docs/ko/security.md)

opsgate is a released 0.7.x project implementing remote HTTP, authentication and human-approved writes. The optional note provider is experimental. It has not yet been security-reviewed by third parties. Review the implementation you deploy.

## Threat model

**One opsgate instance is one trust group.** `ops:read` reads every enabled read
provider: servers, cluster resources and notes. Provider allowlists constrain
resources; `server.tools` and per-user/token `tools` globs can narrow tool access.
They are not separate credential or process boundaries. For different trust
levels run separate instances and accounts, for example production operations and
personal notes. Keep each instance's credentials and reachable
data appropriate for its entire trust group.

The intended boundary is a constrained MCP interface with read tools and explicitly approved write actions: explicit provider opt-in, namespace allowlists, namespace denies, scope checks, bounded calls and output, best-effort redaction and per-call audit records. No arbitrary shell or exec interface is exposed. Kubernetes Secrets are excluded; ConfigMap values are hidden by default. These controls reduce accidental disclosure and the operations a client can request through opsgate.

They do not make untrusted output safe. Logs, events, annotations and resource names can contain prompt injection. Treat output as data, never instructions. A malicious or compromised client can request every operation its configured scopes and allowlists permit, copy returned data, or attack other connected tools. MCP annotations are hints, not authorization. Stdio trusts the OS account launching the server; its configured user name is only a label.

Over-broad RBAC or kubeconfig permissions expand the consequences of bugs and compromise. Use dedicated credentials with only required read and optional write permissions, and review the supplied RBAC manifests. Kubeconfig `exec` and `auth-provider` credential plugins are rejected. Keep kubeconfig files and config paths under trusted ownership. Namespace allow/deny rules filter namespaced resources and Namespace objects by name; they do not restrict Nodes or PersistentVolumes, including nodes in `k8s_overview`. Omit `namespace` for cluster-scoped kinds. The supplied ClusterRoleBinding grants cluster-wide reads; prefer namespace RoleBindings where possible. Providers and their credentials must not be attacker-controlled. Running as root is discouraged; use an unprivileged account with only the cluster access required.

The [tool reference](docs/tools.md#redaction-and-object-sanitization) details the implemented text patterns and object sanitization. Task-log pages, note line pages and search snippets use sensitive ranges detected in the full bounded source, including secrets crossing page boundaries. Fragments preserve line endings within masked spans. Private-key PEM blocks missing a BEGIN or END marker are masked from or through the available source boundary; material with neither marker cannot be identified as PEM. ConfigMap data and binaryData values are hidden by default. Pod and workload literal environment values are masked for case-insensitive name substrings pass, secret, token, key, credential, auth, dsn, private, cert and database_url; other values may remain visible. `valueFrom` references are visible and are not resolved. Redaction is best effort, not a guarantee that secrets cannot escape. Logs, metadata and process information can contain credentials in unexpected formats. Avoid collecting secret material in the first place. Protect audit logs too: arguments and errors can retain sensitive information. Auditing is not a tamper-proof ledger and does not guarantee durable delivery when writes fail. Context deadlines require handlers and dependencies to cooperate; output limits do not eliminate all resource exhaustion risks.

opsgate does not defend against a compromised host, kernel, server binary, dependency, administrator or credential store. It does not sandbox the client or revoke the launching user's permissions. It does not protect against stolen bearer tokens used within their lifetime, denial of service at the network edge, or disclosure by an authorized client.

## Remote exposure and authorization

HTTP exposes `/mcp` with Protected Resource Metadata. Authentication is mandatory; HTTPS is required for the public origin except loopback development. Terminate TLS at a trusted proxy, keep the backend private and forward the discovery, OAuth and approval routes described in [deployment](docs/deploy.md). Do not log bearer tokens, cookies, authorization codes, client secrets or approval URLs. The Host check replaces the SDK's localhost check: public requests must match
`public_url`'s host, including any explicit port, or receive 421. Loopback
listeners additionally accept loopback Host values for probes. When signed inbox
notices are configured, only the exact `POST /approvals/notify` endpoint also
accepts other syntactically valid Hosts for private service delivery. It ignores
browser credentials and authenticates with constant-time HMAC verification,
signed freshness and replay checks, a 64 KiB body cap and a global rate limit.
It stores bounded identifier-only notices and never creates approvals or executes
actions. All browser and MCP routes retain the Host policy. A Host-preserving
reverse proxy works with a loopback backend; wildcard listeners have no local
exception. CORS and MCP annotations are not authorization boundaries.

Builtin OAuth addresses these threats:

- Authorization-code interception: PKCE S256 is mandatory; no implicit or password grant.
- Redirect theft: exact registered redirect URI matching on authorization and code redemption.
- DCR abuse: bounded bodies, metadata and client capacity, registration rate limiting, restricted grants and authentication methods, HTTPS redirects or loopback HTTP and supported native URI schemes. At capacity, unused registrations older than 10 minutes are evicted oldest first, excluding clients with tokens or outstanding authorizations. Registration does not grant tool access.
- Registration policy: `allowed_redirect_hosts` limits redirect destinations. With a nonempty list, `unknown_redirect_hosts: deny` rejects unknown hosts; `approve` keeps clients pending until a configured administrator reviews `/admin/clients`. Admins must be allowed users. `notify_url` optionally receives a best-effort JSON notification for pending registrations. An empty host list accepts any otherwise valid redirect host and ignores the unknown-host policy. Registration approval does not grant user access or skip consent.
- Phishing through DCR: the consent screen shows the requesting client, redirect destination and scopes. Client names are untrusted; consent is not proof of client ownership. Humans must inspect the destination and requested permissions. Before consent, authorization errors are rendered locally; Deny after the consent screen may redirect.
- Code and refresh replay: single-use codes, refresh rotation, reuse detection and revocation of descendant token families.
- Login CSRF: upstream login state is tied to the browser; consent and browser approval use CSRF protection. OIDC also validates identity tokens.
- Credential disclosure from state storage: opaque access/refresh tokens and client secrets are stored as hashes, not raw credentials. Secret comparisons use constant-time checks. Protect state files and backups regardless; state persistence errors fail closed.

OAuth responses are sent after releasing the shared state lock. OAuth GET routes reject bodies and POST body reads have a 10-second deadline. Pending authorizations evict the oldest entry that has not finished sign-in at 20 per client or 1000 overall; entries whose user has signed in are never evicted, and when only those remain new requests get HTTP 429; browser approval and admin logins use a separate pool of 256, with a burst of 20 and one new admission every 3 seconds over a 10-minute transaction lifetime, and only expired login entries are removed. A flood can delay new sign-ins until the shared limiter recovers (HTTP 429 with `Retry-After`), while existing sign-ins remain valid until expiry; apply a rate limit at the reverse proxy. HTTP access logs mask `/approve/<id>` as `/approve/*` and omit query strings.

Passkey login (upstream `passkey`) needs no external identity provider. Registration requires a one-time invite: a random 256-bit token whose SHA-256 is stored in a `0600` file next to the state file, valid for its TTL (24h by default, at most 7 days), bound to one configured subject and deleted on use. At first start opsgate writes such invites to its log for users without a passkey, so anyone who can read the service log can enroll as those users until they do: restrict log access, or issue invites with `opsgate passkey invite` instead. Credentials require user verification and a resident key; the relying party ID is the `public_url` host and the only accepted origin is `public_url`. Each login challenge is single-use and bound to the browser's pending authorization cookie; a signature counter that fails to advance is treated as a cloned authenticator and rejected. Losing a passkey means issuing a new invite; removing a user from `users` immediately blocks sign-in. Passkey pages are the only pages that run script: the same-origin `/passkey.js` under `script-src 'self'`, with no inline script.

Builtin login accepts only configured passkey users (`passkey:<name>`), GitHub logins, verified OIDC email identities or exact, case-sensitive `oidc:<sub>` identities from the configured issuer. Google email identities additionally require a Gmail/Googlemail domain or an `hd` claim matching the email domain; other Google addresses must use an immutable subject. GitHub login renames can change who matches a login allowlist: prefer verified email via OIDC or keep the GitHub allowlist current. External mode verifies JWT signatures via discovered JWKS and checks issuer, audience, expiry and allowed signing algorithms. Issuer, discovery and JWKS URLs require HTTPS, except loopback HTTP. Optional external user entries restrict identities and scopes. Static mode stores only SHA-256 token digests; it has no interactive OAuth flow.

Kubernetes writes need a separate action allowlist; note writes need `allow_write`. These tools need `ops:write`, OS/RBAC permission and human confirmation. HTTP also requires `ops:read`. Planning validates the target and produces the summary before execution. Elicitation obtains an in-client decision through MCP input requests. With builtin auth and `approval.web: true`, HTTP defaults to browser approval even for clients supporting elicitation; the client receives an approval link and the approver signs in upstream as the same user. The approval expires, is single-use and binds the SHA-256 of canonical unredacted arguments (excluding `approval_id`) and the exact plan. A summary changed by redaction is denied because its target cannot be shown unambiguously. Kubernetes summaries include the UID, and Apply receives the plan from the same invocation; rollout uses a JSON patch UID test, scale retains resourceVersion, and pod deletion retains a UID precondition. A retry with changed arguments cannot reuse it. Without confirmation, writes are denied.

Time-window approvals are off by default. Enabling them requires `approval.window` between 1m and 12h and explicit tool-name `path.Match` globs in `approval.window_tools`; an empty allowlist allows none. A human can opt in while approving an eligible call through the browser or elicitation. A grant binds one exact user, tool and target, and allows later valid calls from other clients with different arguments on that same target until expiry. A different target requires a new approval. Use narrow tool allowlists and short windows. Grants do not bypass scope checks, provider policy, planning, argument limits or summary redaction checks. They are held only in memory, capped at 100 live grants, never extended by use, and cleared on restart. Creation emits a separate `window_granted` audit event (source method, duration and expiry), and every use is audited by the write wrapper with `approval: window`. `require_web` still requires the browser to obtain a grant and denies stdio writes. Compromised clients can opt into elicitation windows just as they can accept individual elicitation prompts.

Raw write JSON defaults to 16 KiB; note create/append/replace allow 528 KiB (256 KiB per content/replacement input). JSON escaping counts toward the wire limit. Approval summaries allow `max(64 KiB, argument limit + 16 KiB)`; redacted display arguments must fit the tool argument limit. Provider output above 4 MiB is omitted entirely; other output is redacted before truncation to the configured response cap (default 64 KiB). Approval and consent pages share escaped HTML templates, inline CSS, no scripts or external assets, and retain their CSRF, cross-origin, CSP and cache protections. Approval links and CSRF tokens are not included in grant audit events.

`approval.require_web` defaults to `true` when `approval.web` is enabled, including by a mode with builtin auth. Explicit `approval.require_web: false` opts in to elicitation: a compromised client can approve its own writes, and the server cannot establish that a real person clicked a client-controlled prompt. External/static auth and `approval.web: false` keep `require_web` false by default because no web channel is available. HTTP startup warns when elicitation is allowed. When the file omits `require_web`, `-transport stdio` allows elicitation even with a mode, since the user starts the process locally and there is no web channel. Explicit `true` still denies stdio writes. `config show` prints the HTTP default explicitly; its `require_web: true` must be removed or set to `false` to allow stdio writes. Prompt injection can still cause the model to *request* writes. Humans must read the summary and target before approving. Approval does not make an operation safe or guarantee the target's state remains unchanged; OS privileges, RBAC and narrow allowlists remain essential. Audit records include plans, approval methods, pending outcomes, web decisions (with only an approval ID prefix) and denied multi round-trip verifications, but audit delivery is not a prerequisite for execution.

## Security testing

Go fuzz tests cover every parser that takes input from outside the server.
Their seeds run with the ordinary tests on every push, and a weekly workflow
fuzzes each target further and keeps any failing input.

| Input | Fuzz target | What must hold |
| --- | --- | --- |
| Action links and choices links | `FuzzParse`, `FuzzOptionsURL` (`internal/action`) | A link that parses only carries catalog fields that pass validation and rebuilds to the same tool, arguments and source; choices links stay within their path |
| Approval routes and forms | `FuzzApprovalRoutes` (`internal/approval`) | No malformed path or form causes a server error |
| Signed inbox notices | `FuzzNoticeSignature`, `FuzzNoticeFields` | Nothing is accepted without the key; a signed notice stores only identifiers and links to choices pages |
| Push subscriptions | `FuzzPushEndpoint` | Only HTTPS endpoints on the allowed push services pass |
| OAuth registration, authorization and token requests | `FuzzRegister`, `FuzzAuthorize`, `FuzzToken`, `FuzzValidRedirect` (`internal/oauth`) | Registered clients only have valid redirects, grants and hashed secrets; sign-in starts only for a registered client, redirect and S256 challenge; no form obtains a token without a code or refresh token from this server |
| Signed watch batches for the analyzer | `FuzzReceive` (`internal/analyzer`) | Another key is rejected; the queue stays within its byte budget and holds only checked alerts |
| External JWT scope claims | `FuzzTokenScopes` (`internal/authn`) | Only known scopes, each once |
| Redaction | `FuzzRanges`, `FuzzValue` (`internal/redact`) | Ranges are ordered and in bounds; pages keep their line breaks and never show a masked part; known secrets stay masked whatever text surrounds them |

The server has not had a third-party security review.

## Known limitations

- A flood of anonymous authorization requests can push out connection attempts that have not finished sign-in yet, so those users must start again. Connections whose user has signed in are kept.
- A quoted secret value left open at its line end is masked through its closing quote when that quote follows within 4 KiB and 32 lines; past that, only its first line is masked. Text between the opening and closing quote is masked even if the quote belongs to other text.
- Dependency scanners may report GO-2026-5932 for the pinned `x/crypto` module, although opsgate does not import or call its affected OpenPGP packages.
- Task log and diff reads fail when the complete source exceeds 20 MiB.
- Without an existing approval window, stdio refuses note writes whose preview needs more than about 8 KiB of review.
- Email retries can deliver duplicate messages if the connection drops during DATA.

## Note provider

Vault access is confined with `os.Root`, excludes dot paths and sync-conflict
copies, and applies configured exclusions. Safe in-vault symlinks are supported.
Note text and filenames are untrusted. Note write plans show the target, hashes
of masked current/result text, and a content preview or diff bounded near 8 KiB
with omitted lines reported. Without an existing eligible approval window, a
truncated preview requires web approval showing the complete masked arguments;
if web approval is unavailable, split the change into smaller writes.

When full review is required, any redaction change to the arguments (excluding `approval_id`) denies the write before confirmation or approval-window reuse; split the change or remove the masked value.

Apply rechecks the approved file identity, hash, size and modification time.
Updates then publish a synced temporary file with rename. This is an optimistic
check, not a transaction against external editors: a concurrent external edit
between the check and rename can be overwritten. Use version history in sync
tools and backups. Creates publish without overwriting a racing creator; trash
retains a copy locally rather than permanently deleting a note. Protect the
vault and its backups with filesystem permissions.

## Host provider

The optional host provider is off by default and refused with
`mode: kubernetes`. It takes only concrete, validated unit and container names
from allowlists and runs `systemctl`/`journalctl` with fixed argv, never a shell,
with timeouts and output caps. Restarts need both the visible and the restart
allowlist and human approval. Unit restarts go through systemd's D-Bus API and a
polkit rule limited to the `restart` verb on listed units, so opsgate needs no
sudo and keeps `NoNewPrivileges`. Docker access must go through a socket proxy
that allows only list, inspect, logs and restart; the Docker socket itself is
root-equivalent. A container restart is bound to the container ID in the
approved plan. See [host provider](docs/host.md).

## Supported versions

While the project is 0.x, security fixes and support are best effort, focused on the latest development version. There is no guaranteed maintenance window for older 0.x releases.

## Reporting a vulnerability

Please report privately using [GitHub Security Advisories](https://github.com/gkdis6/opsgate/security/advisories/new): choose **Report a vulnerability** on the repository's Security page. Include the affected revision, configuration with secrets removed, impact and reproducible steps. Do not open public issues for vulnerabilities or publish credentials. Maintainers will coordinate investigation and disclosure privately; response times are best effort.

## Optional analyzer model backends

The analyzer can use an API model or a configured Claude Code or Codex CLI executable.
CLI mode launches only that operator-controlled executable with fixed flags,
without a shell. Alert text and evidence arrive on stdin, not command arguments.
Native CLI tools, direct MCP servers, user/project settings, hooks and persisted
sessions are disabled through CLI flags; managed organizational settings may
still apply. All requested MCP calls return to the shared read-only tool policy.
The process has a deadline, bounded output and process-group cancellation on
Linux/macOS. These controls do not sandbox a malicious executable or protect its
OS account from that executable: secure the CLI installation and auth directory.
CLI credentials and account limits are independent of the MCP read-only token;
the MCP token is not given to the CLI. `-check` checks executable availability,
not login freshness or provider access. The default container image does not
bundle Claude Code or a subscription login.

Codex mode also uses a per-invocation local model catalog to remove model-owned
shell/patch tools, explicit feature/config overrides, and a JSONL event guard
that cancels unexpected native-tool events. Its format is tested against CLI
0.166.0, including a local fake Responses-server contract test that asserts the
outgoing native tool list is empty. This metadata/config contract must be
rechecked when upgrading Codex; read-only sandboxing alone does not constrain
remote MCP writes. Codex receives no MCP token or direct MCP server. Common
MCP policies remain enforced by the analyzer. Existing CLI auth can be shared
with a properly protected mount; refresh may write to that directory.
