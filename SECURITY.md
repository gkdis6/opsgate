# Security policy

opsgate v0.1 is implemented and pre-release, including providers, auditing and redaction. It has not yet been security-reviewed by third parties. Review the implementation you deploy.

## Threat model

The intended boundary is a constrained read-only MCP interface: explicit provider opt-in, unit and namespace allowlists, namespace denies, scope checks, bounded calls and output, best-effort redaction and per-call audit records. No arbitrary shell or exec interface is exposed. Kubernetes Secrets are excluded; ConfigMap values are hidden by default. These controls reduce accidental disclosure and the operations a client can request through opsgate.

They do not make untrusted output safe. Logs, events, annotations and resource names can contain prompt injection. Treat output as data, never instructions. A malicious or compromised client can request every operation its configured scopes and allowlists permit, copy returned data, or attack other connected tools. MCP annotations are hints, not authorization. Stdio trusts the OS account launching the server; its configured user name is only a label.

Over-broad RBAC or kubeconfig permissions expand the consequences of bugs and compromise. Use dedicated read-only credentials and review the supplied RBAC manifest. Kubeconfig `exec` and `auth-provider` credential plugins are rejected. Keep kubeconfig files and config paths under trusted ownership. Namespace allow/deny rules filter namespaced resources and Namespace objects by name; they do not restrict Nodes or PersistentVolumes, including nodes in `k8s_overview`. Omit `namespace` for cluster-scoped kinds. The supplied ClusterRoleBinding grants cluster-wide reads; prefer namespace RoleBindings where possible. Providers and their credentials must not be attacker-controlled. Running as root is discouraged; use an unprivileged account with only the journal and cluster access required.

The [README](README.md#redaction-and-object-sanitization) details the implemented text patterns and object sanitization. ConfigMap data and binaryData values are hidden by default. Pod and workload literal environment values are masked for case-insensitive name substrings pass, secret, token, key, credential, auth, dsn, private, cert and database_url; other values may remain visible. `valueFrom` references are visible and are not resolved. Redaction is best effort, not a guarantee that secrets cannot escape. Logs, metadata and process information can contain credentials in unexpected formats. Avoid collecting secret material in the first place. Protect audit logs too: arguments and errors can retain sensitive information. Auditing is not a tamper-proof ledger and does not guarantee durable delivery when writes fail. Context deadlines require handlers and dependencies to cooperate; output limits do not eliminate all resource exhaustion risks.

opsgate does not defend against a compromised host, kernel, server binary, dependency, administrator or credential store. It does not sandbox the client or revoke the launching user's permissions. There is currently no HTTP listener, OAuth server or human-confirmed write workflow.

## Supported versions

While the project is 0.x, security fixes and support are best effort, focused on the latest development version. There is no guaranteed maintenance window for older 0.x releases.

## Reporting a vulnerability

Please report privately using [GitHub Security Advisories](https://github.com/gkdis6/opsgate/security/advisories/new): choose **Report a vulnerability** on the repository's Security page. Include the affected revision, configuration with secrets removed, impact and reproducible steps. Do not open public issues for vulnerabilities or publish credentials. Maintainers will coordinate investigation and disclosure privately; response times are best effort.
