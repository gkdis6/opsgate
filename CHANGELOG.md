# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
GitHub releases carry generated release notes.

## [Unreleased]

### Added

- Recovery check after web-approved Kubernetes actions: opsgate watches the workload for up to 3 minutes and reports Recovered, Not recovered yet (with counts and a reason such as `CrashLoopBackOff`) or unknown on the approval page, in the inbox, in `approval_status` and in the audit log, and pushes a notification that names only the result. Payload-less push notifications now fetch their text from the signed-in `/approvals/latest`. / 웹에서 승인한 Kubernetes 조치 뒤에 복구 확인을 추가합니다. opsgate가 최대 3분간 워크로드를 지켜보고 복구됨, 아직 복구되지 않음(개수와 `CrashLoopBackOff` 같은 사유 포함), 확인 불가를 승인 페이지, 승인함, `approval_status`, 감사 로그에 표시하며, 결과만 담은 푸시 알림을 보냅니다. 내용 없는 푸시 알림은 로그인된 `/approvals/latest`에서 문구를 가져옵니다.

### Changed

- Builtin auth now defaults to web approval (`approval.web: true`, hence web-only `require_web: true`) without a `mode`, so a compromised client cannot approve its own writes by default. Set `approval.web: false` or `approval.require_web: false` to keep in-client elicitation. / `mode` 없이 builtin 인증을 써도 웹 승인(`approval.web: true`, 따라서 웹 전용 `require_web: true`)이 기본이 되어, 침해된 클라이언트가 기본 설정에서 자기 쓰기를 스스로 승인할 수 없습니다. 클라이언트 내 확인을 유지하려면 `approval.web: false` 또는 `approval.require_web: false`를 설정하세요.

## [0.4.0] - 2026-11-08

### Added

- Tapping an approval notification opens `/approvals/next`, which redirects to the newest waiting approval or alert. Repeating a call whose approval is still waiting joins it instead of creating a second approval, and the page shows Applied / Not applied after approval. / 승인 알림을 누르면 `/approvals/next`가 가장 최근의 대기 중인 승인이나 알림으로 바로 이동합니다. 승인이 아직 대기 중일 때 같은 호출을 반복하면 새 승인을 만들지 않고 기존 승인에 합류하며, 승인한 뒤 페이지에 적용됨/적용 안 됨을 표시합니다.

- Optional host provider for servers without Kubernetes: `host_units`, `host_unit_logs`, `host_containers`, `host_container_logs` and the catalog actions `host_restart_unit` (systemd through polkit, no sudo) and `host_restart_container` (Docker through a socket proxy, bound to the container ID). It has a `/approve/host-options/...` choices page and `opsgate watch` alerts for failing or restarting units and containers. Off by default and refused with `mode: kubernetes`. / Kubernetes 없는 서버를 위한 선택 기능 호스트 provider를 추가합니다. `host_units`, `host_unit_logs`, `host_containers`, `host_container_logs`와 카탈로그 조치 `host_restart_unit`(polkit을 거치는 systemd, sudo 없음), `host_restart_container`(소켓 프록시를 거치는 Docker, 컨테이너 ID에 고정)를 제공합니다. `/approve/host-options/...` 선택지 페이지와, 실패하거나 재시작하는 유닛·컨테이너에 대한 `opsgate watch` 알림이 있습니다. 기본값은 꺼짐이며 `mode: kubernetes`에서는 거부됩니다.

- Undo links for reversible actions: after `k8s_scale` or `k8s_restore_image` is applied, the approval page, inbox, MCP result and `approval_status` offer a catalog link back to the previous replica count or image. `k8s_restore_image` gains `revert: true`, limited to images in the Deployment's ReplicaSet history. / 되돌릴 수 있는 조치에 되돌리기 링크를 추가합니다. `k8s_scale`이나 `k8s_restore_image`를 실행하면 승인 페이지, 승인함, MCP 결과, `approval_status`에 이전 파드 수나 이전 이미지로 돌아가는 카탈로그 링크가 붙습니다. `k8s_restore_image`에 Deployment의 ReplicaSet 이력 이미지로 제한한 `revert: true`를 추가합니다.

- Namespace alert groups with ordered routing, output selection, warning filters and independent batching/cooldown per group and output; unmatched alerts are counted locally. / namespace 알림 그룹에 순서별 경로, 출력 선택, 경고 필터와 그룹·출력별 묶음 및 cooldown을 추가하고 미일치 알림은 로컬 누락 개수로 집계합니다.

- Optional signed watch-to-inbox notices with bounded per-user alert storage, CSRF-protected dismissal and coalesced payload-less phone push; choices stay behind live permission checks and human approval. / 서명된 watch 승인함 알림 선택 기능과 사용자별 제한된 경고 저장, CSRF로 보호한 해제, 묶음 내용 없는 휴대폰 푸시를 추가하며 현재 권한 검사와 사람 승인을 유지합니다.

- Current-state rule choices for image failures, failing rollouts, repeated crashes/OOM and stuck/unreachable controller-owned pods; signed-in choices pages linked from watch alerts and analyzer reports, with fixed bilingual reasons and preserved webhook fields. / 이미지 실패·배포 정체·반복 종료/OOM·멈추거나 연결할 수 없는 컨트롤러 소유 파드에 현재 상태 규칙 선택지를 제공하고, watch·분석기에서 로그인 후 선택지 페이지로 연결하며 고정 이중 언어 이유와 기존 웹훅 필드를 유지합니다.

- Installable, per-user `/approvals` inbox and optional payload-less Web Push with durable VAPID keys, protected device subscriptions and bounded delivery. / 사용자별 설치 가능한 `/approvals` 승인함과 VAPID 키 영구 저장, 보호된 기기 구독, 제한된 큐를 사용하는 내용 없는 Web Push 선택 기능을 추가합니다.

- Shared action catalog and generic approval links for restart, image restore, scale and controller-owned pod deletion; read-only `action_link` validates live plans, and analyzer buttons use catalog labels and rule/AI provenance. Legacy restart/image links remain aliases. / 재시작·이미지 복구·규모 조정·컨트롤러 소유 파드 삭제의 공통 카탈로그와 승인 링크, 현재 계획을 검증하는 조회 전용 `action_link`, 출처를 표시하는 분석기 버튼을 추가하고 기존 링크 별칭을 유지합니다.

### Changed

- Web approval now rechecks and executes MCP writes immediately, once, using the original caller. Calls wait up to `approval.wait` (default 25s with web approval; 0–55s). Read retained results with the new read-only `approval_status` tool or an argument-bound `approval_id` retry; retries never apply again.

### Removed

- Remove the experimental Dev provider, `dev_projects`/`dev_task_*` tools, `opsgate dev-runner` and `dev:run` scope in v0.4.0. Delete `providers.dev` and remove `dev:run` from configured scope lists; stop and remove the runner service. Existing OAuth state and requests drop the removed scope while retaining valid scopes.

## [0.3.0] - 2026-11-06

### Fixed

- Render new note previews as unified diffs from `/dev/null` and color approval summary lines only after paired diff headers, so plain text lists are not shown as deletions.

### Removed

- Remove `watch.webhook_url`, `watch.secret_file` and `watch.allow_private_http`; configurations using them fail with migration guidance. Use `watch.outputs` for webhook destinations; the default JSON body and HMAC signature stay the same. `watch.email` remains available.

  Before:
  ```yaml
  watch:
    webhook_url: https://hooks.example.com/watch
    secret_file: /etc/opsgate/watch/key
    allow_private_http: false
  ```

  After:
  ```yaml
  watch:
    outputs:
      - name: webhook
        type: webhook
        url: https://hooks.example.com/watch
        secret_file: /etc/opsgate/watch/key
        allow_private_http: false
  ```

## [0.2.1] - 2026-11-06

### Security

- Mask and bound all cluster-controlled watch alert text before delivery and deduplication, including direct JSON and custom templates.
- Mask decoded alert strings before model JSON encoding so quoted and escaped secret assignments stay masked across API and CLI backends.
- Mask analysis reports before both forwarding and Discord delivery.
- Reject oversized alert fields and retain investigations within atomic per-job/global byte budgets, including replacements.
- Require a ready owned Pod with a ready running container reporting the proposed image for image restore, and recheck source evidence before applying.
- Mount Kubernetes ServiceAccount credentials only into the analyzer pod's reader container.

### Changed

- Analyzer retention is limited to 64 KiB per job and 8 MiB overall with fixed producer-compatible field caps; unused display/batch fields are discarded.
- The reference analyzer pod uses an explicit reader-only projected token/CA/namespace volume instead of automatic ServiceAccount mounts.
- Analysis reports show total investigation and cumulative model-call time; failure notices show elapsed total time.

## [0.2.0] - 2026-11-05

### Changed

- Require web approval by default wherever available on HTTP; explicit `approval.require_web: false` opts in to elicitation, which lets a compromised client approve its own writes. Stdio keeps elicitation unless `require_web: true` is explicit.

### Added

- Korean/English Discord state explanations and configurable report language/action proposals, preserving source error codes and messages. Discord analysis notifications log verified delivery IDs without logging report bodies.
- Codex CLI analyzer backend with schema-based JSONL replies, native-tool suppression, shared read-only MCP enforcement and bounded execution.
- Selectable analyzer model backend: OpenAI-compatible API or installed Claude Code CLI with shared read-only tool enforcement and bounded subprocess execution.
- Experimental `opsgate-analyzer` process for signed watch alerts, bounded read-only MCP investigations through a configurable Chat Completions model, and Discord reports.
- Independent watch outputs for direct Discord embeds and custom JSON webhooks, with body templates, headers, credential files and retries for HTTP 429. Existing webhook and SMTP settings remain supported.
- Independent SMTP email delivery for `opsgate watch`, alongside or instead of webhooks, with mandatory verified TLS, separate batching and retries, and optional email-only warning filtering. Alerts are not stored and are lost after retries; names and redacted summaries pass through a mail provider outside the cluster. Port 465 uses implicit TLS; every other port requires STARTTLS. Use a separately revocable dedicated credential (such as an app password) and check account limits, especially for shared accounts: one message per batch interval can mean 288/day at 5m or 96/day at 15m.
- MCP over stdio and authenticated Streamable HTTP, including the sessionless `2026-07-28` protocol, with bounded output, redaction and JSONL auditing.
- Kubernetes inspection across one or more clusters, with allowlists, namespace denies, scopes and per-user/token tool restrictions.
- Builtin OAuth 2.1 with PKCE, consent, GitHub/OIDC login, passkeys, registration invites, refresh rotation and optional persistence.
- External JWT and digest-configured static bearer authentication, plus client registration policy and admin review.
- Approved Kubernetes rollout restarts, scaling and pod deletion, plus optional `k8s_restore_image` restoring a Deployment container image from a currently ready owned ReplicaSet, with `k8s_remediation_options` approval links.
- Argument-bound, expiring, single-use approvals through MCP elicitation or browser fallback, with optional approval windows.
- Kubernetes deployment defaults, `opsgate init` and effective configuration display.
- Experimental Markdown note tools and a separate development task runner with approvals and ownership checks.
- Experimental Kubernetes watch alerts with batching, deduplication and optional webhook signatures.
- Release archives, container images, signatures, SBOMs, provenance, Kubernetes deployment files and the dev-runner systemd unit.

### Removed

- Removed the host provider, `mode: host`, the `opsgate init` flags `-mode`, `-units` and `-kubeconfig`, and the server and watch systemd units. opsgate now targets Kubernetes.
