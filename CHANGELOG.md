# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
GitHub releases carry generated release notes.

## [Unreleased]

### Added

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
