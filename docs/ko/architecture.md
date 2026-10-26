# 아키텍처

[English](../architecture.md)

opsgate는 Go MCP 서버입니다. provider를 통해 설정된 운영 작업을 노출합니다. 각 도구에 공통 인가, 출력 처리, 감사 기능을 적용합니다.

## 요청 흐름

```text
AI 클라이언트 (claude.ai, ChatGPT, Claude Code 등 MCP 클라이언트)
  |
  +-- HTTPS -> 리버스 프록시 -> HTTP 리스너
  |     공통 보안 헤더, panic 복구, 접근 로그
  |     -> Host 검사 (불일치 시 421) -> 엔드포인트별 CORS -> 라우팅
  |          /mcp -> Bearer 검증 -> ops:read -> 프로토콜 핸들러
  |          /.well-known/* -> 검색 메타데이터
  |          /oauth/*, /passkey/*, /admin/clients -> 내장 OAuth
  |          /approve/* -> 웹 승인
  |          /healthz -> 상태 응답
  |
  +-- stdio -> 설정된 로컬 ID와 스코프
                  |
                  v
             Go MCP SDK 서버: tools/list, tools/call
                  |
             공통 도구 래퍼
                  |
       +----------+----------+------------+
       |          |          |            |
      host       kube     obsidian       dev
                                          |
                                      Unix 소켓
                                          |
                                      dev-runner
                                          |
                                 Git worktree + 에이전트
```

HTTP 리스너는 일반 HTTP를 제공합니다. 공개 HTTPS에는 리버스 프록시를 사용합니다. `http.public_url`은 HTTPS origin이어야 합니다. 루프백 HTTP만 예외입니다. Host 검사는 설정된 공개 authority를 허용합니다. 리스너와 로컬 연결 주소가 모두 루프백이면 루프백 호스트도 허용합니다. CORS는 검색 및 일부 MCP/OAuth 엔드포인트에만 적용합니다. 브라우저에서 결정을 내리는 페이지에는 적용하지 않습니다.

`/mcp`는 `Mcp-Protocol-Version` 값을 문자열로 비교합니다. `2026-07-28` 이상이면 stateless 핸들러를 선택합니다. 이전 값이거나 헤더가 없으면 세션 핸들러를 선택합니다. 세션 제한 시간은 30분입니다. 이 분기는 ChatGPT를 포함한 새 세션리스 클라이언트를 지원하면서 이전 클라이언트와 클라이언트 내 확인(elicitation)을 위한 세션을 유지합니다. 라우팅 분기 자체는 접근 권한을 부여하지 않습니다.

명령은 프로세스당 전송 방식 하나를 선택합니다. 기본값은 stdio입니다. HTTP에는 명시적인 인증 모드가 필요합니다. `dev:run` 도구를 쓰는 호출자도 `ops:read`가 있어야 합니다.

## 패키지

| 패키지 | 역할 |
| --- | --- |
| `cmd/opsgate` | 설정을 읽고 프로세스를 구성합니다. `-gen-token`, `passkey invite`, `dev-runner`를 지원합니다. |
| `internal/config` | 기본값 위에 YAML 문서 하나를 읽습니다. 모르는 키를 거부하고 설정을 검증하며 스코프를 정의합니다. |
| `internal/httpserver` | HTTP 라우팅, Host/CORS 검사, 보안 헤더, 접근 로그, 프로토콜 선택을 담당합니다. |
| `internal/authn` | static Bearer와 검색된 JWKS를 사용하는 외부 JWT를 검증합니다. |
| `internal/oauth` | 내장 OAuth 인가 서버입니다. 클라이언트 등록(DCR), S256 PKCE, 동의 화면, 토큰 회전/폐기, 패스키, 관리자 클라이언트 검토를 제공합니다. |
| `internal/server` | MCP 서버를 만들고 활성화된 provider를 등록합니다. |
| `internal/tool` | 등록 정책, 호출자/도구 스코프, 인자 제한, 시간 제한, 쓰기 계획/승인, 출력 마스킹/자르기, 감사 이벤트를 처리합니다. |
| `internal/approval` | 클라이언트 내 확인, 웹 승인 링크, 선택적인 승인 시간 창을 제공합니다. |
| `internal/redact` | 정규식과 직접 스캔으로 알려진 비밀 패턴을 마스킹합니다. 완전한 비밀 탐지가 아닌 보조 장치입니다. |
| `internal/audit` | 파일 또는 stderr에 JSONL 감사 이벤트를 추가합니다. |
| `internal/ui` | 동의, 승인, 패스키, 관리 페이지의 공통 레이아웃을 제공합니다. |
| `internal/provider/host` | systemd 상태/로그, 프로세스, 디스크 정보, 허용된 유닛 재시작을 제공합니다. |
| `internal/provider/kube` | Kubernetes 오브젝트, 이벤트, 로그를 제공합니다. 네임스페이스 정책 안에서 허용된 rollout restart, scale, 파드 삭제를 수행합니다. |
| `internal/provider/obsidian` | Markdown 볼트를 검색하고 읽습니다. 선택적인 create/append/replace/trash 도구는 내용/diff 미리보기와 스냅샷 검사를 사용합니다. |
| `internal/provider/dev`, `internal/devrunner` | 별도 runner에 요청을 보냅니다. runner는 설정된 프로젝트/에이전트를 선택하고 Git worktree에서 작업을 실행합니다. |

## 도구 실행

서버 도구 정책은 등록 시 적용합니다. 거부된 도구는 `tools/list`에 나타나지 않습니다. `server.tools.allow`가 비어 있으면 제공되는 모든 도구를 허용합니다. 거부 패턴이 우선합니다. 제공되는 도구와 하나도 일치하지 않는 서버 정책 패턴은 시작 실패를 일으킵니다. 호출자의 선택적인 `tools` 글로브는 호출을 추가로 제한합니다. 공통 도구 목록은 필터링하지 않습니다.

```text
읽기/일반 도구:
  인자 크기 -> 호출자 -> 사용자 도구 정책 -> 필수 스코프
  -> 시간 제한 핸들러 -> 출력 처리 -> 감사 -> MCP 결과

승인이 필요한 쓰기 도구 (AddWrite):
  인자 파싱/크기/해시 -> 호출자 -> 사용자 도구 정책 -> 필수 스코프
  -> 시간 제한 Plan -> 미리보기 검증 -> 승인 -> 시간 제한 Apply
  -> 출력 처리 -> 감사 -> MCP 결과
```

대부분의 도구에는 `ops:read`가 필요합니다. 승인이 필요한 쓰기에는 보통 `ops:write`가 필요합니다. Dev 도구에는 `dev:run`이 필요합니다. `dev_task_start`는 이 스코프로 승인 래퍼를 사용합니다. `dev_task_cancel`은 상태를 바꾸는 일반 도구입니다. 작업을 중지하기 위한 사람의 승인은 의도적으로 요구하지 않습니다.

기본 인자 제한은 16 KiB입니다. 내용을 전달하는 쓰기는 더 큰 도구별 제한을 사용할 수 있습니다. 기본 시간 제한은 30초이며 context 취소를 사용합니다. 쓰기에는 Plan과 Apply의 제한 시간이 따로 있습니다. 승인은 두 제한 시간 사이에서 진행합니다. 개별 제한은 [도구](tools.md)를 참고합니다.

도구 출력과 오류 텍스트가 4 MiB를 넘으면 생략 메시지로 대체합니다. 그 외에는 알려진 비밀을 마스킹한 뒤 `server.max_output_bytes`에 맞춰 자릅니다. 기본값은 64 KiB입니다. 감사 이벤트는 ID, 전송 방식, 도구, 마스킹된 인자, 결과, 소요 시간, 출력 크기를 기록합니다. 승인된 쓰기는 계획과 승인 방식도 기록합니다. 성공한 전체 출력은 감사 이벤트에 저장하지 않습니다. `server.audit_log`가 비어 있으면 프로세스 로그와 함께 stderr로 JSONL 이벤트를 보냅니다.

## 권한 경계

접근은 여러 독립 계층으로 제한합니다. 토큰/로컬 스코프, 서버 도구 정책, 호출자 도구 제한, provider 허용 목록, 마지막으로 OS 권한이나 Kubernetes RBAC가 적용됩니다. provider 검사는 핸들러나 계획 안에서 수행합니다. 승인은 이 검사를 대체하지 않습니다.

승인이 필요한 쓰기는 Plan → 승인 → Apply를 따릅니다. 일회성 승인은 사용자, 전송 방식, 도구, 마스킹 전 인자 해시, 계획 요약에 묶입니다. 선택적인 승인 시간 창은 사용자/도구 쌍에 의도적으로 더 넓은 접근을 부여합니다. [사람의 승인](approval.md)을 참고합니다.

로그, 노트, 이벤트, 에이전트 출력은 신뢰하지 않는 데이터로 취급합니다. 마스킹은 우발적 노출을 줄입니다. 프롬프트 인젝션을 막거나 해당 텍스트를 안전한 지시로 만들지는 않습니다.

`dev-runner`는 자체 설정, 프로젝트, 에이전트 명령을 가진 별도 계정으로 실행합니다. 서버는 Unix 소켓으로 통신합니다. Linux에서 runner는 `SO_PEERCRED`로 peer 자격을 검사합니다. 자신의 UID 또는 설정된 소켓 그룹에 속한 호출자를 허용합니다. 에이전트 실행 범위는 runner 계정과 설정된 에이전트 샌드박스가 제한합니다. 요청한 사람의 OS 권한이 기준이 되지는 않습니다. [개발 도구](dev.md)와 [보안 정책](security.md)을 참고합니다.

## 최소 로컬 설정

기본값에서는 모든 provider가 꺼져 있습니다. provider 활성화, 쓰기 활성화, 쓰기 스코프 부여는 별도 설정입니다. 서버 도구 허용 정책처럼 비어 있을 때 넓게 허용하는 정책도 있습니다. “모두 꺼짐”은 모든 설정이 아니라 provider 기본값을 설명합니다.

```yaml
stdio:
  user: alice
  scopes: [ops:read]
providers:
  host:
    enabled: true
    units: [example.service]
```

`opsgate.yaml`로 저장하고 실행합니다.

```sh
opsgate -config opsgate.yaml -transport stdio
```

운영 설정은 [설정](configuration.md), [인증](authentication.md), [배포](deploy.md)를 참고합니다.
