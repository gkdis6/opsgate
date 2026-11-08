# 알림 자동 분석

`opsgate-analyzer`는 선택적으로 실행하는 별도 분석 프로세스입니다. 서명된 watch
알림을 받고, 모델이 조회 전용 opsgate MCP 도구를 호출하도록 중계한 뒤 Discord에
분석 결과를 보냅니다. 실험 기능이며, 모델 없이도 기존 watch 알림과 대화형 MCP
사용은 그대로 가능합니다.

```text
watch ──→ Discord: 즉시 알림
      └─→ analyzer /alerts → 모델 ↔ 조회 전용 opsgate MCP → Discord: 분석 결과
```

## 시작하기

`make build`로 `bin/opsgate`와 `bin/opsgate-analyzer`를 빌드합니다.
[설정 예시](../../analyzer.example.yaml)를 복사한 뒤 모델명과 파일 경로를 지정하세요.

```sh
opsgate-analyzer -config analyzer.yaml -check
opsgate-analyzer -config analyzer.yaml
```

`-check`는 설정과 자격 증명 파일만 검사하며 외부 서비스를 호출하지 않습니다.
실행 시 `/healthz`는 프로세스 상태를, `/readyz`는 MCP 인증과 조회 도구 목록까지
확인합니다. 준비 상태 검사에서는 모델을 호출하지 않습니다.

`model.backend`에서 **`openai_compatible`(기본값)** 또는 **`claude_cli`**, **`codex_cli`**를
선택합니다. 자동 전환이나 두 모델의 동시 실행은 하지 않습니다. API 방식은
OpenAI 호환 Chat Completions를 사용하며 모델이 함수
호출, `max_completion_tokens`, `parallel_tool_calls`, `usage.total_tokens`를
지원해야 합니다. 기본 endpoint는 `https://api.openai.com/v1/chat/completions`이며
모델명은 직접 지정해야 합니다. 모든 호환 공급자·모델의 동작을 보장하지 않으며,
Responses나 Anthropic Messages 프로토콜은 아직 구현하지 않았습니다.
함수 호출 흐름은 [OpenAI 공식 문서](https://developers.openai.com/api/docs/guides/function-calling)를 참고했습니다.

공유 HMAC 키(공백 제거 후 16바이트 이상), 조회 전용 MCP 토큰, Discord 웹훅
URL을 각각 보호된 파일로 전달하세요. API 방식만 모델 API 키 파일이 추가로
필요하고, CLI 방식은 실행 계정의 해당 CLI 인증을 사용합니다. 시작할 때 한 번 읽으므로
변경 후 재시작해야 합니다. Discord URL은 쿼리·프래그먼트 없는
`https://discord.com/api/webhooks/ID/TOKEN` 형식입니다.

## API와 Claude CLI 선택

API 방식은 기존 설정에 backend를 명시하면 됩니다. 생략해도 기존처럼 동작합니다.

```yaml
model:
  backend: openai_compatible
  endpoint: https://api.openai.com/v1/chat/completions
  name: YOUR_FUNCTION_CALLING_MODEL
  api_key_file: /etc/opsgate-analyzer/model-api-key
```

CLI 방식은 다음처럼 설정합니다. 이때 `api_key_file`은 필요하지 않습니다.

```yaml
model:
  backend: claude_cli
  name: sonnet
  claude:
    binary: /absolute/path/to/claude
    config_dir: /absolute/path/to/.claude
    max_turns: 3
    max_budget_usd: 0.5
```

[CLI 전체 설정 예시](../../analyzer.claude.example.yaml)도 제공합니다. `binary`를
생략하면 시작할 때 PATH에서 `claude`를 찾고, `config_dir`를 생략하면 CLI의 기본
인증 경로를 사용합니다. 직접 지정하는 경로는 절대 경로여야 합니다. 분석기와
같은 OS 계정으로 `claude auth status`를 확인하세요. 기존 CLI 로그인이나 지원되는
인증 환경변수를 사용할 수 있으며 로그인 만료·갱신과 계정 사용량 제한이 적용됩니다.
인증 디렉터리는 토큰 갱신을 위해 쓰기 권한이 필요할 수 있습니다. 기본 opsgate
이미지에는 Claude CLI와 로그인이 포함되지 않으므로 실행 환경에 별도로 준비해야
합니다. 서버용 Kubernetes 배포 도우미는 API와 정적으로 링크된 네이티브 CLI 바이너리를
지원합니다. CLI 인증 디렉터리도 별도로 마운트합니다.

CLI는 모델의 다음 조회 선택과 요약만 담당합니다. CLI에 MCP 토큰이나 직접
접속할 MCP 서버를 전달하지 않고, 실제 조회는 공통 엔진이 도구 이름과 권한을
검사한 뒤 실행합니다. 따라서 API·CLI 모두 같은 조회 전용 정책과 조사 한도를
사용합니다.

`claude -p`에 JSON 스키마 출력, 빈 기본 도구 목록, 엄격한 빈 MCP 설정,
restricted 모드, 훅·스킬·세션 기록 비활성화 옵션을 지정하고 매번 별도 임시
작업 폴더를 사용합니다. 사용자·프로젝트 설정은 제외하지만 조직 관리 설정은
적용될 수 있습니다. 구독 로그인을 읽지 않는 `--bare`는 사용하지 않습니다.
옵션 근거는 [Claude Code 공식 문서](https://code.claude.com/docs/en/headless)를
참고하세요. 이 제한이 실행 파일 자체를 격리하는 OS 샌드박스는 아니므로 운영자가
관리하는 CLI와 인증 디렉터리를 사용해야 합니다.

프롬프트는 표준 입력으로 전달하고 출력은 최대 1 MiB로 제한하며, 실패 시 원본
stdout/stderr를 알림에 노출하지 않습니다. Linux/macOS에서는 시간 초과 시
프로세스 그룹을 종료합니다. `max_turns`는 CLI 호출 한 번의 내부 턴 수(1–8),
`max_budget_usd`는 CLI 호출 한 번의 추정 비용 제한(0 초과, 최대 10)입니다.
조사 전체나 청구 금액의 정확한 상한은 아니며 조사당 최대 `max_rounds`번 실행할
수 있습니다. `max_completion_tokens`는 CLI의 개별 모델 요청에 출력 토큰 환경변수로
전달합니다. 입력·출력·캐시 토큰 사용량을 합산하고 구조화 결과나 사용량이 없으면
실패로 처리합니다. backend 변경 후에는 분석기를 재시작하세요.

## Codex CLI 선택

```yaml
model:
  backend: codex_cli
  name: YOUR_CODEX_MODEL
  codex:
    binary: /absolute/path/to/codex
    config_dir: /absolute/path/to/.codex
```

[Codex 전체 설정 예시](../../analyzer.codex.example.yaml)를 제공합니다.
`binary` 생략 시 PATH에서 찾고, `config_dir` 생략 시 기존 인증 경로를 사용합니다.
지정한 인증 디렉터리는 하위 프로세스의 `CODEX_HOME`으로 전달하며, 명시하는 경로는
절대 경로여야 합니다. 같은 실행 계정으로 `codex login status`를 확인하세요.
기존 ChatGPT 로그인 또는 지원되는 CLI 인증 환경변수를 사용할 수 있습니다.
토큰 갱신을 위해 인증 디렉터리에 쓰기 권한이 필요할 수 있습니다. 기본 이미지에
Codex와 로그인은 포함되지 않습니다. 배포 도우미의 hostPath 방식은 정적 ELF
바이너리를 사용하고 인증 디렉터리 소유자의 non-root UID로 실행합니다.
인증 디렉터리는 분석기 컨테이너에만 마운트하고 자격 증명으로 보호하세요.

`codex exec --json --output-schema`로 임시 세션을 실행하며 읽기 전용 샌드박스,
사용자 설정·규칙 제외, 훅·플러그인·앱·스킬·협업·셸·이미지 도구 비활성화와
빈 MCP 설정을 적용합니다. 호출마다 만든 정확한 모델명 기준의 로컬 카탈로그로
모델 자체의 셸·패치 도구도 제거합니다. 메타데이터와 설정 키는 Codex CLI
0.166.0을 기준으로 검증했습니다. 호환되지 않는 CLI/설정이면 조사를 실패로
처리하며 조직의 관리 요구 사항은 계속 적용됩니다. 로컬 카탈로그가 모델의 계정
접근 권한을 늘려 주지는 않습니다. 옵션과 출력 형식은
[공식 비대화형 실행 문서](https://learn.chatgpt.com/docs/non-interactive-mode)와
[설정 참조](https://learn.chatgpt.com/docs/config-file/config-reference)를 따릅니다.

Codex는 다음 조회 선택이나 요약을 구조화된 텍스트로 반환하고, MCP 호출은
공통 엔진만 실행합니다. JSONL에서 에이전트 메시지·추론 이외의 직접 도구 실행,
실패·미지원 이벤트가 나오면 중단합니다. 출력은 최대 1 MiB이며 Linux/macOS에서
시간 초과 시 프로세스 그룹을 종료합니다. `turn.completed` 사용량의 입력·출력
토큰을 합산하고, 입력에 포함된 캐시 토큰을 다시 더하지 않습니다. 사용량 누락,
잘못된 JSON, 불완전한 스트림이나 스키마 필드 누락도 실패로 처리합니다.

Codex 자체의 호출당 금액·턴 제한 옵션은 이번 연동에 없습니다. 공통 조사 시간,
요청 바이트, 모델 라운드, MCP 호출과 누적 보고 토큰 한도를 적용합니다. 응답 토큰
요청량은 프롬프트로 전달하며 Codex 전용 출력 토큰 옵션으로 강제하지 않습니다.
바이트 추정에 모든 CLI 내부 문맥이 포함되는 것은 아니므로 정확한 요금 상한은
아닙니다. `-check`는 모델을 호출하지 않고 경로·실행 파일을 검사하며 로그인 만료는
확인하지 않습니다.

## MCP 권한과 watch 연결

기존 사용자용 OAuth/패스키 서버의 설정을 바꾸지 않고, 분석 전용 MCP 서버를
별도로 사용합니다. 전용 토큰은 `ops:read`만 부여하고 서버 도구 목록과 Kubernetes
RBAC도 읽기 전용으로 제한하세요. 같은 파드 안에서는 MCP를 `127.0.0.1:8788`에
바인딩하고 분석기를 함께 실행합니다. 원격 MCP 연결은 HTTPS만 허용합니다.
CLI backend만 고정 인자와 설정된 실행 파일로 하위 프로세스를 실행합니다.
셸은 사용하지 않고, MCP 도구가 프로세스 실행을 요청할 수 없습니다.

도구 이름의 고정 허용 목록과 `readOnlyHint`를 모두 확인하고, 실행 직전에도
재검사합니다. Kubernetes 상태·객체·이벤트·로그·서비스 설정 조회와 노트 검색·읽기
등만 허용하며 재시작, 노트 수정 도구는 호출할 수 없습니다.
Notes가 켜져 있고 호출 예산이 허용하면 먼저 관련 노트를 검색합니다. 노트 볼륨도 읽기 전용으로
마운트하고 `providers.note.allow_write`를 끄세요. 노트가 없어도 Kubernetes
조회로 조사할 수 있습니다.

watch에는 원본 JSON 출력 대상을 추가합니다. 본문 템플릿은 지정하지 마세요.

```yaml
watch:
  outputs:
    - name: analyzer
      type: webhook
      url: http://opsgate-analyzer.opsgate.svc.cluster.local:8080/alerts
      allow_private_http: true
      problems_only: true
      secret_file: /etc/opsgate/analyzer-hmac/key
```

양쪽에 같은 HMAC 키를 마운트해야 합니다. 수신기는 본문 서명과 `sent_at`을
검증하고, 10분보다 오래됐거나 1분 넘게 미래인 배치를 거부합니다. 요청 본문은
최대 1 MiB, 알림은 최대 500건입니다. 외부에 공개하지 말고 watch만 접근하도록
네트워크를 제한하세요. 신뢰하는 사설망 밖에서는 TLS를 사용해야 합니다.
서명이 유효해도 알림·로그·노트 내용은 지시가 아닌 신뢰할 수 없는 데이터입니다.

[Kubernetes 배포 예시](../../deploy/kubernetes/opsgate-analyzer.yaml)는 자격 증명
설정 전 기동되지 않도록 replicas 0에서 시작합니다. Service와 readiness probe가 파드 주소로
접속하므로 `analyzer.yaml`에 `listen: 0.0.0.0:8080`을 설정하세요. `opsgate-analyzer` ConfigMap에
`analyzer.yaml`과 `reader.yaml`, 같은 이름의 Secret에 `hmac`, `mcp-token`,
`model-api-key`, `opsgate-watch-discord` Secret에 `url`을 준비하세요. 분석기 설정의
경로는 매니페스트에 맞게 `/etc/analyzer-credentials/*`, `/etc/analyzer-discord/url`을
사용합니다. 분석기 바이너리가 포함된 릴리스 이미지를 고정해 사용하세요.
전체 MCP 설정 예시와 고정 도구 목록은 [영문 문서](../analyzer.md)에 있습니다.

## 제한과 결과

기본값은 경고만 분석하며, 같은 클러스터·종류·namespace·리소스 알림을 10초 동안
묶습니다. 조사 시작 후 같은 리소스는 성공·실패 여부와 관계없이 15분간 중복
분석을 막습니다. 배치 재전송은 10분간 메모리에서 식별합니다. 대기 리소스는
최대 64개, 조사당 서로 다른 이유·요약은 최대 50건이며 초과는 누락으로 셉니다.
수신기는 원본 알림 근거만 보관하고 번역된 `display`와 사용하지 않는 배치 `text`·`actions`·`language`는 버립니다. 고정 필드 상한은 watch에 맞춰 cluster/namespace 63바이트, kind 64바이트, name 253바이트, reason/summary/hint 유니코드 301자 및 1203바이트, restart URL 4096바이트입니다(restart 식별자에도 같은 상한 적용). 필드가 크면 413을 반환합니다. 직렬화한 배치·키 크기와 구조체·맵을 위한 보수적 여유분을 합산해 작업당 64 KiB, 전체 8 MiB로 제한하며 교체도 계산하고 큐에서 꺼내면 차감합니다. 이 예산은 기준 파드의 256 MiB 제한에 맞췄지만 모든 임시 할당이나 동시 요청 메모리를 제한하지는 않습니다. 개수나 바이트 예산이 가득 차면 일부만 저장하지 않고 503을 반환해 watch의 제한된 재시도를 유도합니다.

단일 worker가 순서대로 조사하며, 조사당 기본 제한은 2분, 도구 호출 12회,
모델 호출 8회, 응답당 1500토큰, 누적 보고 토큰 100000, 요청 본문 128 KiB,
도구 결과 16 KiB입니다. 모든 설정의 범위는 [전체 설정 표](../analyzer.md#queue-limits-and-reports)에 있습니다.
요청 전 바이트 수를 이용한 보수적 추정과 공급자가 보고한 토큰 사용량을 함께
확인합니다. 정확한 요금 상한을 보장하는 기능은 아닙니다. `max_total_tokens`는
`max_completion_tokens`보다 커야 합니다. 사용량이 없거나 한도에
도달하면 중단하고, 마지막 모델 호출은 추가 조회 없이 요약에 사용합니다.
API backend의 모델 요청은 자동 재시도하지 않습니다. CLI 내부 재시도 설정도
최소화하며 공통 조사 시간 제한을 적용합니다.

보고서에는 확인된 사실·추정 원인·추가 확인 사항을 구분하도록 요청하고, 실제
호출한 도구·마스킹한 인자·근거 ID를 붙입니다. 꼬리말에는 조회 수·토큰 수·도구 호출을 포함한 전체 조사 경과 시간과 API/CLI의 모델 호출 내부 누적 시간을 표시합니다(예: `조회 3회 · 사용 토큰 12345 · 소요 1분 12초 (모델 48초)`). 시간은 초 단위로 반올림하며 최소 1초로 표시하고, 1분 미만이면 초만 표시합니다. 시작한 조사가 실패하거나 시간 초과되어도 전체 경과 시간을 표시합니다. 최소 한 번의 Kubernetes 조회가
성공해야 보고서를 수락합니다. 모델이 근거를 잘못 해석할 수 있으므로 인용이
정확성을 보장하지는 않습니다. 실패나 증거 부족이면 명시적으로 알리고 시스템
변경은 수행하지 않습니다. Discord 결과는 1900 UTF-16 단위 안에서 문단 경계로 나눠 보내고
자동 멘션은 끕니다. 전송 재시도로 중복 메시지가 생길 수 있습니다.

따옴표 안의 할당 값과 이스케이프된 텍스트를 포함해 알림 문자열은 모든 모델 backend에서 최종 JSON 인코딩 전에 마스킹하고, 조회 결과도 모델 전송 전에 마스킹합니다. 보고서는 선택적 전달 수신자와 Discord 양쪽에 보내기 전에 한 번 마스킹하지만 모든 민감 정보를 탐지한다는 보장은 없습니다. 선택한 모델 공급자와
Discord로 전달해도 되는 데이터만 허용하세요. 로그에는 상태·호출 수·토큰 수·시간과
알림 전송 성공 여부를 남기며 자격 증명과 공급자 응답 본문은 남기지 않습니다.
기준 analyzer 파드는 ServiceAccount 토큰 자동 마운트를 끄고 토큰·CA·namespace를 reader에만 투영합니다. analyzer는 MCP 토큰을 사용하므로 직접 Kubernetes 자격 증명이 필요 없습니다. 큐·중복 방지 정보는 메모리에만 있어 재시작 시 대기 작업이 유실됩니다. 실패한
결과 전송은 저장하지 않고 로그로 남깁니다. 단일 replica로 운영하세요. 영구 큐,
다중 worker 조정과 자동 복구는 이번 버전 범위에 포함되지 않습니다.

## 알림 언어와 권장 조치

즉시 도착하는 Discord 알림과 AI 분석 보고서는 언어 설정이 각각 있습니다.
Discord는 영어가 기본이고, 분석기는 한글이 기본입니다. 둘 다 `ko`와 `en`을
지원합니다.

```yaml
# opsgate watch 설정
watch:
  outputs:
    - name: discord
      type: discord
      language: ko
      url_file: /etc/opsgate/discord/url
```

```yaml
# 분석기 설정
report:
  language: ko
  suggest_actions: true
```

영어를 원하면 각각 `language: en`으로 바꾸고 재시작하세요. 한글 즉시 알림은
`ImagePullBackOff`, `ErrImagePull`, `OOMKilled`, 배치 불가와 노드 자원 압박 등
알려진 상태의 의미와 확인할 사항을 한글로 설명합니다. 알 수 없는 상태는
구체적인 원인을 추정하지 않고 MCP 조회를 안내합니다. 임의의 애플리케이션 로그를
번역하는 기능은 아니므로 **오류 코드·리소스명·원문 로그는 그대로 보존**합니다.
분석 보고서에서는 원인 설명 자체를 선택한 언어로 작성하도록 모델에 요청합니다.
원본 webhook JSON의 `reason`·`summary`는 바꾸지 않습니다. watch의 전역 `language: ko`는 webhook에도 번역된 `display` 필드를 추가합니다.
텔레그램 수신 템플릿은 `display.reason`, `display.summary`, `display.hint`를 우선 표시해야 합니다.

`suggest_actions`는 기본 true이며 대상 리소스, 근거, 실행 제안 명령 또는 기존
승인 링크, 영향·주의점, 조치 후 확인과 되돌리기 절차를 제안하도록 요청합니다.
올바른 이미지 태그처럼 검증되지 않은 값은 임의로 만들지 않고 확인이 필요한
자리표시자로 표시합니다. 없는 이미지나 잘못된 설정의 해결책으로 단순 재시작을
권하지 않습니다. 제안은 모델이 작성하므로 검토가 필요하며 **자동 실행하지 않습니다**.
승인 링크도 알림에 실제로 포함된 것만 사용하도록 요청합니다. 조치 제안이
필요 없으면 false로 바꿀 수 있습니다.


## 즉시 알림과 분석 결과 분리

watch는 장애를 발견하면 기존 알림 대상으로 먼저 전송하고, 독립된 출력 큐를 통해
분석기에 전달합니다. 모델 응답을 기다리지 않으므로 분석 지연이나 실패가 최초 알림을
막지 않습니다. 분석 완료 후 별도 메시지에 원인·근거·권장 조치를 보냅니다.
분석 실패 시에는 실패 사실과 추가 확인 필요를 표시합니다.

분석 결과를 텔레그램 등으로 전달하려면 분석기 `forward`를 설정합니다.
원본 watch webhook과 다른 경로를 사용하세요. 수신기는 원문 body의 HMAC-SHA256
서명(`X-Opsgate-Signature`)과 `sent_at`, `source: opsgate-analyzer`를 검증하고
`text`와 선택적인 `actions`를 표시합니다. `report.language`가 보고서 언어를 정합니다.

```yaml
report:
  language: ko
  suggest_actions: true
  approval_url: https://mcp.example.com
forward:
  url: https://automation.example.com/webhook/opsgate-analysis
  secret_file: /etc/opsgate-analyzer/forward-hmac
```

이미지 다운로드 장애의 조치 버튼은 서버 Kubernetes 설정의 `restore_image` 허용 목록과
`action_approval_url`을 함께 설정한 대상에만 제공됩니다. 조회 도구가 소유 관계와
삭제 중이 아닌 Deployment 소유 ReplicaSet과 정확히 그 UID가 소유한 삭제 중이 아닌 준비된 파드를 확인한 이미지에 한해 승인 링크를 만듭니다. 대상 컨테이너는 준비되어 실행 중이어야 하며, 파드 명세 이미지는 ReplicaSet 템플릿과 같고 상태 이미지 참조도 Docker Hub·기본 태그 표기 차이만 정규화한 뒤 일치해야 합니다. SHA-256/SHA-512 런타임 imageID가 필요하고 digest 고정 이미지는 그 값도 일치해야 합니다. 실행 때 다시 검사하며 namespace에 ReplicaSet이나 파드가 500개를 넘으면 안전하게 거부합니다. 준비 상태는 애플리케이션 정상 동작을 보장하지 않고 태그는 바뀔 수 있습니다.
모델이 만든 명령이나 URL을 실행하지 않습니다. 버튼을 누르면 기존 로그인·패스키
인증을 거쳐 이미지 변경 내용을 검토하고 승인합니다. 승인 때 상태를 다시 확인하고
해당 컨테이너 이미지 하나만 변경합니다. 승인은 한 번만 유효하며 승인 창을 열지 않습니다.
검증된 이전 이미지가 없으면 버튼 없이 수동 확인 방법을 제시합니다.

텔레그램 내장 브라우저에서 외부 브라우저로 전환해도 이미지 복구 대상과 파라미터를
보존하여 새 로그인을 시작합니다. 이전 브라우저의 인증 상태를 재사용하지 않습니다.
대상이 삭제되거나 허용 정책이 바뀐 이전 알림의 승인 링크는 사용할 수 없습니다.

Discord 본문의 Markdown 링크와 한 메시지에 들어가는 코드 블록은 통째로 유지합니다.
긴 문단은 줄·단어 경계에서 나누며, 제한보다 긴 코드 블록은 줄 경계에서 나누고
각 메시지의 코드 블록 표시를 닫고 다시 열어 읽을 수 있게 합니다. 파일 첨부는 사용하지 않습니다.
