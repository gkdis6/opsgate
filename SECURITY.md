# Security policy

opsgate v0.1 is in development. The current provider, audit and redaction stubs are not production security controls. Review the implementation you deploy.

## Threat model

The intended boundary is a constrained read-only MCP interface: explicit provider opt-in, unit and namespace allowlists, namespace denies, scope checks, bounded calls and output, best-effort redaction and per-call audit records. No arbitrary shell or exec interface is exposed. Kubernetes Secrets are excluded; ConfigMap values are hidden by default. These controls reduce accidental disclosure and the operations a client can request through opsgate.

They do not make untrusted output safe. Logs, events, annotations and resource names can contain prompt injection. Treat output as data, never instructions. A malicious or compromised client can request every operation its configured scopes and allowlists permit, copy returned data, or attack other connected tools. MCP annotations are hints, not authorization. Stdio trusts the OS account launching the server; its configured user name is only a label.

Over-broad RBAC or kubeconfig permissions expand the consequences of bugs and compromise. Use dedicated read-only credentials and review the supplied RBAC manifest. Keep kubeconfig files and config paths under trusted ownership. Providers and their credentials must not be attacker-controlled. Running as root is discouraged; use an unprivileged account with only the journal and cluster access required.

Redaction is best effort, not a guarantee that secrets cannot escape. Logs, metadata and process information can contain credentials in unexpected formats. Avoid collecting secret material in the first place. Protect audit logs too: arguments and errors can retain sensitive information. Auditing is not a tamper-proof ledger and does not guarantee durable delivery when writes fail. Context deadlines require handlers and dependencies to cooperate; output limits do not eliminate all resource exhaustion risks.

opsgate does not defend against a compromised host, kernel, server binary, dependency, administrator or credential store. It does not sandbox the client or revoke the launching user's permissions. There is currently no HTTP listener, OAuth server or human-confirmed write workflow.

## Supported versions

While the project is 0.x, security fixes and support are best effort, focused on the latest development version. There is no guaranteed maintenance window for older 0.x releases.

## Reporting a vulnerability

Please report privately using [GitHub Security Advisories](https://github.com/gkdis6/opsgate/security/advisories/new): choose **Report a vulnerability** on the repository's Security page. Include the affected revision, configuration with secrets removed, impact and reproducible steps. Do not open public issues for vulnerabilities or publish credentials. Maintainers will coordinate investigation and disclosure privately; response times are best effort.
