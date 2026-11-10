# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
GitHub releases carry generated release notes.

## [Unreleased]

### Added

- [Stability and compatibility](docs/stability.md): which configuration, tools, routes and formats are kept compatible, which parts are experimental (analyzer, notes, host), and how removals and changed defaults are announced. `opsgate watch`, the approval inbox and push are part of the covered core loop and no longer labelled experimental. / [안정성과 호환성](docs/ko/stability.md) 문서를 추가합니다. 어떤 설정, 도구, 경로, 형식을 유지하는지, 무엇이 실험 기능인지(analyzer, 노트, 호스트), 삭제와 기본값 변경을 어떻게 알리는지 적었습니다. `opsgate watch`, 승인함, 푸시는 유지하는 핵심 흐름에 속하며 실험 기능 표시를 뗍니다.

- `approval.state_file` (default `/var/lib/opsgate/approvals.json` with a mode): undecided approvals opened from links, inbox notices and the link-signing key survive a server restart. A restored approval gets a new CSRF token and is planned again when approved; a decided one is never restored. / `approval.state_file`(모드 기본값 `/var/lib/opsgate/approvals.json`)을 추가합니다. 링크로 연 미결 승인, 승인함 알림, 링크 서명 키가 서버 재시작 뒤에도 남습니다. 복원된 승인은 CSRF 토큰을 새로 받고 승인할 때 계획을 다시 세우며, 이미 결정된 승인은 복원하지 않습니다.

- Go fuzz tests for every parser that takes outside input: action and choices links, approval routes, signed inbox notices, push endpoints, OAuth registration, authorization and token requests, signed analyzer batches, JWT scope claims and redaction. A weekly workflow fuzzes each target further; SECURITY.md lists what each must hold. / 외부 입력을 해석하는 모든 코드(조치 링크, 승인 경로, 서명된 알림, 푸시 주소, OAuth 등록·인가·토큰, 분석기 묶음, JWT scope, 비밀값 가리기)에 Go fuzz 테스트를 추가하고, 매주 더 오래 도는 워크플로를 둡니다. 대상마다 지켜야 하는 성질은 SECURITY.md에 적었습니다.

### Changed

- Analyzer reports are short and lead with the cause: **Cause** (confirmed, likely or insufficient evidence), **Do now** (at most three steps, each an approval link or one command alone in a code block for copying), up to three **Evidence** lines, and **Still to check** only when needed. The header is one line and the footer lists reads by name without their arguments. / 분석 보고서를 짧게 바꾸고 원인을 맨 앞에 둡니다. **원인**(확정·추정·증거 부족), **지금 할 일**(최대 3단계, 승인 링크이거나 복사하기 쉽게 코드 블록에 하나씩 넣은 명령), **근거**(최대 3줄), 필요할 때만 **더 확인할 것** 순서입니다. 머리말은 한 줄이고 꼬리말은 조회한 도구 이름만 적습니다.

- `action_link` only issues `source=ai` links; `source: rule` is refused. Rule links come from `k8s_remediation_options` and the choices page. / `action_link`는 `source=ai` 링크만 만들고 `source: rule`은 거부합니다. rule 링크는 `k8s_remediation_options`와 조치 선택 페이지에서만 나옵니다.

### Security

- From an AI-assisted audit (OpenAI Codex CLI 0.166, read-only, findings verified and fixed by hand; not a third-party human review): / AI 보조 감사(OpenAI Codex CLI 0.166, 읽기 전용, 지적 사항은 직접 검증해 수정. 사람이 한 외부 검토는 아님)에서 나온 수정:
  - `k8s_get` and `k8s_env` hide literal values of variables named like `MYSQL_PWD`, `COOKIE` and `SESSION_ID`, and a secret flag at the end of `command` masks the first element of `args`. / `k8s_get`과 `k8s_env`가 `MYSQL_PWD`, `COOKIE`, `SESSION_ID` 같은 이름의 값도 가리고, `command` 끝의 비밀 플래그 다음에 오는 `args` 첫 값도 가립니다.
  - `k8s_env` bounds `$(VAR)` expansion to 16 KiB per value and 1 MiB per container, so a small spec that doubles a value at every variable cannot exhaust memory. / `k8s_env`의 `$(VAR)` 펼치기를 값마다 16 KiB, 컨테이너마다 1 MiB로 제한해, 변수마다 값을 두 배로 늘리는 작은 스펙으로 메모리를 고갈시킬 수 없습니다.
  - Rollout restart also tests the reviewed spec generation, so a spec changed between the final plan and the patch is not restarted unseen. / 재시작 패치가 검토한 스펙의 generation도 확인해, 마지막 계획과 패치 사이에 스펙이 바뀌면 실행하지 않습니다.
  - An apply that times out or loses its connection is reported as **Outcome unknown** (`approval_status`: `unknown`) instead of Not applied, since the change may have reached the target. / 적용 중 시간 초과나 연결 끊김은 "적용 안 됨"이 아니라 **결과 확인 필요**(`approval_status`의 `unknown`)로 알립니다. 변경이 대상에 도달했을 수 있기 때문입니다.
  - Opening the same action link again returns the approval already waiting, and one user can hold at most 20 undecided approvals opened from links, so another site cannot fill the approval table through a signed-in browser. / 같은 조치 링크를 다시 열면 이미 대기 중인 승인으로 이동하고, 링크로 연 미결 승인은 사용자당 20개까지입니다. 다른 사이트가 로그인된 브라우저로 승인 대기열을 채울 수 없습니다.
  - Request bodies must arrive within 30 seconds, new MCP sessions are admitted at a bounded rate per user (burst 20, then one every 3 seconds), and `/healthz` answers only GET. / 요청 본문은 30초 안에 도착해야 하고, 새 MCP 세션은 사용자별로 제한된 속도(한 번에 20개, 이후 3초에 1개)로만 받으며, `/healthz`는 GET에만 답합니다.

- Action links are signed over their tool, arguments and source. A link the server did not sign (hand-made, edited, or older than the last restart) still needs the same approval, but its card reads "a link this server did not sign — review carefully" instead of repeating the source it claims, and the audit source is `link`. / 조치 링크에 도구, 인자, 출처를 묶은 서명을 붙입니다. 서버가 서명하지 않은 링크(손으로 만든 링크, 고친 링크, 마지막 재시작 전 링크)도 같은 승인이 필요하지만, 카드에는 링크가 주장하는 출처 대신 "서버가 서명하지 않은 링크"라고 표시하고 감사 로그 출처는 `link`로 남깁니다.

- Anonymous authorization requests can no longer push out a connection whose user has already signed in; when only signed-in entries remain, new requests get HTTP 429. / 익명 인가 요청이 로그인을 마친 연결을 밀어내지 못합니다. 그런 항목만 남으면 새 요청은 HTTP 429를 받습니다.
- A quoted secret that continues past its line is masked through its closing quote (within 4 KiB and 32 lines) instead of only its first line, and later lines are still scanned so a truncated value cannot hide the next secret. / 여러 줄에 걸친 따옴표 비밀값을 첫 줄만이 아니라 닫는 따옴표까지(4 KiB, 32줄 이내) 가립니다. 이어지는 줄도 계속 검사해 잘린 값 뒤의 비밀이 드러나지 않습니다.

## [0.7.0] - 2026-11-09

### Added

- **Send a test notification / 테스트 알림 보내기** in the approval inbox (`POST /approvals/push/test`, CSRF-protected, once per 10 seconds), a count of devices with notifications on, and a log line with how many devices the push service accepted. / 승인함에 **테스트 알림 보내기**(`POST /approvals/push/test`, CSRF 보호, 10초에 한 번)와 알림을 켠 기기 수를 추가하고, 푸시 서비스가 받아들인 기기 수를 로그에 남깁니다.
- On iPhone and iPad, the inbox opened in a Safari tab explains that notifications need **Add to Home Screen**. / iPhone과 iPad의 Safari 탭에서 승인함을 열면 알림을 받으려면 **홈 화면에 추가**가 필요하다고 안내합니다.

### Changed

- Every opsgate page (approval, choices, sign-in, admin) links the install manifest and icon, so adding any page to the Home Screen installs the same app; their CSP allows `manifest-src 'self'`. / 승인, 조치 선택, 로그인, 관리 페이지 모두 설치 정보와 아이콘을 연결해 어느 페이지에서 홈 화면에 추가해도 같은 앱이 설치되며, CSP에 `manifest-src 'self'`를 허용합니다.

## [0.6.0] - 2026-11-09

### Added

- QR codes to open pages on a phone: approval, inbox and choices pages show **Open on your phone / 폰에서 열기** with a QR code of the page on the public origin, `opsgate passkey invite` draws the invite as a QR code on a terminal (or with `-qr`), and `opsgate init -recover` ends with a QR code of the inbox. The QR links hold no secret except the passkey invite. / 폰에서 페이지를 여는 QR 코드를 추가합니다. 승인 페이지, 승인함, 조치 선택 페이지에 공개 주소의 해당 페이지를 여는 **폰에서 열기** QR이 나오고, `opsgate passkey invite`는 터미널에서(또는 `-qr`로) 초대 링크를 QR로 그리며, `opsgate init -recover`는 승인함 QR로 끝납니다. 패스키 초대를 빼면 QR 링크에는 비밀이 없습니다.

## [0.5.0] - 2026-11-09

### Added

- Listing in the official MCP Registry as `io.github.gkdis6/opsgate` (`server.json`, published by the release workflow; the image carries the `io.modelcontextprotocol.server.name` label), a documentation site at https://gkdis6.github.io/opsgate/ with a sitemap, and `llms.txt`. The README gains a comparison with other Kubernetes MCP servers and an FAQ. / 공식 MCP Registry에 `io.github.gkdis6/opsgate`로 등록합니다(`server.json`, 릴리스 워크플로가 게시, 이미지에 `io.modelcontextprotocol.server.name` 라벨). 사이트맵이 있는 문서 사이트 https://gkdis6.github.io/opsgate/ 와 `llms.txt`를 추가하고, README에 다른 Kubernetes MCP 서버와의 비교와 FAQ를 넣습니다.

- Recovery check after web-approved Kubernetes actions: opsgate watches the workload for up to 3 minutes and reports Recovered, Not recovered yet (with counts and a reason such as `CrashLoopBackOff`) or unknown on the approval page, in the inbox, in `approval_status` and in the audit log, and pushes a notification that names only the result. Payload-less push notifications now fetch their text from the signed-in `/approvals/latest`. / 웹에서 승인한 Kubernetes 조치 뒤에 복구 확인을 추가합니다. opsgate가 최대 3분간 워크로드를 지켜보고 복구됨, 아직 복구되지 않음(개수와 `CrashLoopBackOff` 같은 사유 포함), 확인 불가를 승인 페이지, 승인함, `approval_status`, 감사 로그에 표시하며, 결과만 담은 푸시 알림을 보냅니다. 내용 없는 푸시 알림은 로그인된 `/approvals/latest`에서 문구를 가져옵니다.

- `opsgate init -recover "<namespace/name globs>"` sets up phone recovery in one step: it also writes `opsgate-watch.yaml`, grants `ops:write`, lists the workloads for restart and image restore, enables Web Push and signed inbox notices, and prints the remaining commands in order. The reference manifests now mount the `opsgate-inbox-hmac` and `opsgate-watch-hmac` Secrets as optional, so nothing needs uncommenting. / `opsgate init -recover "<namespace/name 패턴>"`이 폰 복구 설정을 한 번에 끝냅니다. `opsgate-watch.yaml`도 만들고, `ops:write`를 주고, 워크로드를 재시작·이미지 복구 대상에 넣고, Web Push와 서명된 승인함 알림을 켠 뒤 남은 명령을 순서대로 출력합니다. 참조 매니페스트는 `opsgate-inbox-hmac`, `opsgate-watch-hmac` Secret을 선택 사항으로 마운트하므로 주석을 풀 필요가 없습니다.

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
