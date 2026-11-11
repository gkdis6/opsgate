# Stability and compatibility

[한국어](ko/stability.md)

opsgate is not 1.0 yet. This page says which parts are meant to stay as they
are, which may still change, and how a change is announced. From 1.0 the
rules below become a promise; until then they are how the project already
works, and every exception is listed in the [changelog](../CHANGELOG.md).

## What is covered

The core loop is an alert, an approval card, a passkey approval and a recovery
check. These surfaces belong to it and are kept compatible:

| Surface | Covered |
| --- | --- |
| Configuration | Keys under `mode`, `server`, `http`, `auth`, `approval`, `providers.kubernetes` and `watch` in `opsgate.yaml`, and their defaults |
| MCP tools | Names, argument names and types, and required scopes of the `k8s_*` tools, `approval_status` and `action_link`; the `approval_id` retry flow |
| Command line | `opsgate`, `opsgate watch`, `init`, `config`, `passkey` and their documented flags |
| HTTP | `/mcp`, the OAuth and passkey endpoints, `/approve/...`, `/approvals...` and `/healthz` |
| Links | `GET /approve/action/{tool}` and `/approve/options/...`; an old link keeps opening an approval, although its signature may no longer verify |
| Formats | Audit log fields, the signed watch batch, the signed inbox notice and the `approval_status` result |
| State files | Files written by one release are read by later ones |

The wording of tool results, approval pages, notifications and log lines is
for people and may change in any release. So may limits that are documented as
numbers, when a larger or smaller value is safer.

## What is not covered

These parts are experimental. They work and are tested, but their
configuration, tools and output may change or be removed in a minor release:

- the analyzer (`opsgate-analyzer`), including its report layout and model backends;
- the note provider (`providers.note`, `note_*` tools);
- the host provider (`providers.host`, `host_*` tools).

## How a covered surface changes

1. **Additions** (a new key, tool, argument or field) can arrive in any minor release. Clients must ignore fields they do not know.
2. **Removal or a changed meaning** is announced one minor release ahead: the old form keeps working, the server logs a warning at startup that names the replacement, and the changelog lists it under *Deprecated*. The next minor release may remove it, under *Removed*.
3. **A changed default** is listed under *Changed* with the setting that restores the old behavior.
4. **Security fixes** may break compatibility without the waiting period when keeping the old behavior would leave users exposed. The changelog says so under *Security*.
5. A configuration file with a removed or unknown key **fails at startup with a message that names the key**; it is never ignored silently. The changelog names the replacement.

Before 1.0, rule 2 has exceptions: 0.3 removed configuration keys, 0.4 removed
the Dev provider, 0.5 changed the web approval default and 0.8 made
`action_link` issue only `source=ai`. Each is in the changelog.

## Versions

Releases follow semantic versioning. Security fixes go to the latest release;
older 0.x releases get none. The container image tag and the binary's
`-version` output name the release.
