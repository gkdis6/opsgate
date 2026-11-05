# Codex analyzer ImagePullBackOff smoke test

- Date: 2026-11-05T05:56:19.880933+00:00
- Backend: `codex_cli`, installed Codex 0.166.0, model `gpt-6.1-sol`.
- Dedicated namespace: `opsgate-e2e-20261105-055445`; Pod: `image-pull-check`.
- Intentionally nonexistent image: `registry.k8s.io/pause:opsgate-nonexistent-7cf1afb2fb91c808`.
- Kubernetes reported `ErrImagePull` and `ImagePullBackOff`; the registry returned `not found`.
- Read-only MCP audit confirmed `note_search`, `k8s_get` and `k8s_events` for this resource.
- Analyzer receipt: `2026/11/05 05:55:28 analyzer status=completed failure_code=none alerts=1 calls=3 tokens=9898 duration=25.709s notification_ok=true`.
- Discord notification uses `wait=true`, so success confirms Discord accepted the created message.
- The report body was not copied into process logs; its exact wording was not independently inspected here.
- Test namespace and Pod were deleted after verification; existing application workloads were not modified.
- The analyzer and signed watch output remain enabled.

The local installed-CLI contract test also used a fake Responses endpoint to
assert that Codex advertised no native tools. Shared-engine tests reject write
requests and native-tool events, and cover timeout, output and usage limits.

## Separate Telegram analysis and approval buttons (2026-11-05)

A temporary namespace `opsgate-e2e-split-063718` contained a Deployment that was
first ready on `registry.k8s.io/pause:3.10`, then updated to a nonexistent tag.
The previous ready replica remained available. A temporary policy entry and
namespace-scoped RoleBinding allowed only this test Deployment to be considered
for image restoration. The analyzer retained read-only access.

Observed delivery:

- 06:37:36 UTC: immediate Korean Telegram incident message (message 261,
  workflow execution 4103), showing ErrImagePull and Korean explanation/checks.
- 06:37:49 UTC: ImagePullBackOff state alert (message 262).
- 06:38:34 UTC: separate Korean analysis (execution 4105, messages 263/264),
  with the verified image restoration approval button.
- Discord report messages 1567790212187295875 and 1567790213173088331 were fetched
  through the webhook API; the first contained the same verified link button.
- Codex investigation: 7 reads, 23,461 tokens, 47.619 seconds; notifications succeeded.
- The approval URL returned HTTP 303 to authentication without a session.

No browser approval was submitted and no remediation was executed against the
live test workload. Provider/approval tests cover applying only the chosen image,
fresh-state checks, rejected stale/unverified targets, CSRF, one-use approval,
and replay rejection. `make check` passed, including lint, vet and race tests.
The namespace was deleted and temporary policy entries restored. Buttons from
these test messages cannot remediate a deleted test workload.

## External-browser login regression (2026-11-05)

The first test's button was no longer usable after cleanup; the approval endpoint
correctly rejected the removed policy target. Image restoration URLs also exposed
a separate login bug: `approvalReturn` accepted only query-free paths, so opening
the sign-in page in another browser lost the restore parameters and failed.

The fix preserves only the three expected restore parameters on local restore
paths. Unknown/duplicate parameters, invalid local paths and control characters
are rejected. OAuth authorization transactions remain cookie-bound. Race tests
cover both existing restart links and image restoration links across two browsers.
A live HTTPS test with two independent cookie jars verified that the second browser
returns to the original restore link and initializes a fresh login (HTTP 200).

A new isolated target `opsgate-e2e-split-064221/image-pull-check` was left available
for the user's manual approval test. Telegram execution 4113 successfully sent
message 271 with its verified restoration button at 06:43:33 UTC. The Codex report
used 4 reads / 12,517 tokens in 42.863 seconds. No approval was submitted by the agent.
Cleanup state is `/tmp/opsgate-split-e2e.json`; run
`python3 /tmp/opsgate-split-e2e.py cleanup` after the manual test, which deletes the
namespace and restores the public/reader policies saved before the test.

### Manual approval uncovered cluster-wrapper mismatch

The user approved at 06:44:13 UTC. Audit recorded approval followed by a denied
write (`workload or healthy image source changed; approve again`); the Deployment
remained on the failing image. The provider compared its local summary with the
cluster-decorated summary passed by `clusterWrite`, so even unchanged resources
were refused. The fix stores the original provider summary in the server-side
snapshot and compares fresh local state with that snapshot. Cluster wrapping also
now preserves an action's `NoWindow` flag. A wrapper-level regression test verifies
successful application, image state, no approval window, and stale-plan refusal.
The old one-use approval remains consumed; a fresh review is required from the
original notification action URL. No automatic retry or manual patch bypasses it.

### Successful manual remediation and cleanup

The user approved again at 06:51:04 UTC. Audit recorded `approved` followed by
`k8s_restore_image` outcome `ok` (12 ms). The live Deployment advanced to generation
3 (observed 3), restored `registry.k8s.io/pause:3.10`, and reported desired/updated/
ready/available replicas all 1. Only the healthy Running/Ready Pod remained; the
failed image-pull Pod was removed. Test cleanup then deleted the namespace and
restored the original public/reader image-restore policies.
