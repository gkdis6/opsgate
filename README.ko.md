<p align="center"><img src="docs/images/logo.png" alt="opsgate 마스코트: 승인 체크 패를 든 해태" width="240"></p>

# opsgate

[English](README.md)

**셸을 열어 주지 않고도 AI 어시스턴트의 도움을 받아 서버를 운영할 수 있습니다.**

opsgate는 Go로 작성한 오픈 소스 MCP 서버입니다. Claude, ChatGPT, Claude Code,
Cursor 같은 클라이언트에 Linux 호스트와 Kubernetes를 운영하는 정해진 도구를
제공합니다. 정책을 명시하고 감사 로그를 남기며, 모든 변경에는 사람의 승인을 받습니다.

> **상태:** 첫 공개 릴리스에 필요한 기능을 구현했습니다. 변경 작업에는 사용자 권한 검사,
> 허용 대상 제한, 사람의 승인이 적용됩니다. 조회 범위 제한과 출력 마스킹으로 민감 정보
> 노출을 줄입니다. 외부 보안 검토는 아직 받지 않았으며, 보호 범위와 남아 있는 한계는
> [보안 정책](docs/ko/security.md)에 설명합니다.

## 왜 opsgate인가

- **임의 명령을 실행하지 않습니다.** 목적이 정해진 도구만 있습니다. 셸 도구도,
  Kubernetes Secret 조회 도구도 없습니다.
- **모든 기능이 꺼진 상태로 시작합니다.** provider, 쓰기 작업, 허용 목록은 설정에서
  직접 켜기 전까지 동작하지 않습니다.
- **변경에는 사람의 승인이 필요합니다.** 쓰기 도구는 먼저 바꿀 내용을 정확히 계산하고,
  클라이언트 안이나 브라우저에서 사람의 승인을 기다립니다. 승인은 그 인자와 계획에만
  유효합니다.
- **원격 접속은 인증을 거칩니다.** 내장 OAuth 2.1 서버가 claude.ai와 ChatGPT
  커넥터를 지원합니다. 패스키, GitHub, OIDC(Google 포함)로 로그인하거나 외부 JWT를
  검증하도록 설정할 수 있습니다.
- **조회 결과는 분석 자료로 취급합니다.** AI 클라이언트에는 로그나 조회 결과 안의 지시문을
  따르지 않도록 안내합니다. 반환 결과에는 민감 정보 마스킹과 길이 제한을 적용합니다.
  누가 어떤 도구를 호출했고 어떻게 처리됐는지는 한 줄에 JSON 하나씩 감사 로그에 기록합니다.
- **지난 조치를 기록하고 다시 참고합니다.** 선택 기능인 note provider가 런북과 지난 장애 기록을
  일반 Markdown으로 보관합니다. AI는 같은 증상을 전에 어떻게 처리했는지 먼저 찾아보고,
  승인을 받아 이번 조치를 기록합니다. [노트](docs/ko/notes.md)를 참고하세요.

## 할 수 있는 일

| Provider | 조회 도구 | 쓰기 도구(승인 필요) |
| --- | --- | --- |
| 호스트(systemd, journald, `/proc`) | 개요, 서비스 목록, 유닛 상태, 로그, 상위 프로세스 | 허용된 유닛 재시작 |
| Kubernetes | 개요, 목록, 상세, 이벤트, 파드 로그 | rollout restart, scale, 파드 삭제 |
| 노트: 런북과 메모리, Markdown 폴더 또는 Obsidian 볼트 *(실험적)* | 검색, 읽기, 목록, 최근 노트, 링크, 태그 | 생성, 덧붙이기, 치환, `.trash`로 이동 |
| Dev *(실험적)* | 프로젝트, 작업 상태, 로그, diff, 목록 | Git worktree에서 코딩 에이전트(Codex, Claude) 실행 |

쓰기 도구를 쓰려면 `ops:write` 스코프가 있어야 하고 도구별 허용 목록에도 항목이 있어야 합니다.
전체 인자와 제한은 [도구 문서](docs/ko/tools.md)를 참고하세요.

## 동작 방식

<img src="docs/images/ko/architecture.svg" alt="클라이언트 연결, 인증과 도구 처리, provider 및 OS 계정·RBAC 신뢰 경계를 보여 주는 opsgate 구조" width="830">

클라이언트는 로컬 stdio 또는 TLS 리버스 프록시를 거치는 HTTPS로 연결합니다.
opsgate는 신원과 정책을 확인하고, 쓰기 작업을 계획한 뒤 승인을 받아 실행합니다.
이후 출력을 처리하고 감사 로그를 남깁니다.
실제 접근 범위는 OS 계정과 Kubernetes RBAC가 제한합니다.

자세한 내용: [구조](docs/ko/architecture.md) · [승인](docs/ko/approval.md) ·
[인증](docs/ko/authentication.md)

### 승인 흐름

쓰기 도구는 실제 상태를 바꾸지 않는 계획(Plan)을 먼저 계산합니다. 유효한 승인 시간 창이
없으면 클라이언트 내 확인이나 웹 승인을 받아야 합니다. 일회성 승인은 승인한 인자와
계획에만 유효합니다.

<img src="docs/images/ko/approval-flow.svg" alt="쓰기 호출의 계획, 승인 시간 창·클라이언트·웹 승인, 재호출 검증과 실행 및 거부 경로" width="830">

### 노트: 런북과 메모리

note provider를 활성화하면 AI는 조치하기 전에 지난 장애 기록을 찾아보고, 승인을 받아
이번 조치를 기록합니다. 다음에 조사할 때는 이 기록을 바탕으로 시작할 수 있습니다.
[노트](docs/ko/notes.md)를 참고하세요.

<img src="docs/images/ko/note-memory.svg" alt="OOMKilled 검색으로 지난 조치를 참고하고, 사람의 승인 후 실행과 노트 기록을 거쳐 다음 조사에 활용하는 흐름" width="830">

## 빠른 시작: 로컬(stdio)

Go 1.26 이상으로 빌드하세요.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest   # 또는: make build
```

설정 파일을 저장하세요. 아래 예제에서는 `/path/opsgate.yaml`을 사용합니다.

```yaml
stdio:
  scopes: [ops:read]
providers:
  host:
    enabled: true
    units: [ssh.service, "nginx*"]
```

Claude Code에 등록하세요. 경로는 절대 경로로 쓰세요.

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

stdio는 실행한 계정의 권한으로 동작하므로 관리자 권한이 없는 일반 계정으로 실행하세요. 다른
클라이언트는 [클라이언트 설정](docs/ko/clients.md)을 참고하세요.

## 빠른 시작: 원격(HTTP + 패스키)

패스키는 별도의 ID 제공자가 필요 없습니다. Face ID, Touch ID, Windows Hello,
휴대폰, 보안 키로 로그인할 수 있습니다.

```yaml
http:
  listen: 127.0.0.1:8787
  public_url: https://mcp.example.com
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: passkey
    users:
      - subject: passkey:alice
        scopes: [ops:read]
approval:
  web: true
providers:
  host:
    enabled: true
    units: [myapp.service]
```

```sh
opsgate -transport http -config /etc/opsgate/opsgate.yaml
```

1. 처음 시작하면 패스키가 없는 사용자마다 일회용 등록 링크(24시간 유효)가 로그에
   남습니다. 링크를 열고 **Create passkey**를 누르세요. 링크를 새로 만들려면
   `sudo -u opsgate opsgate passkey invite -config /etc/opsgate/opsgate.yaml -subject passkey:alice`를 실행하세요.
2. 앞단에 `Host` 헤더를 그대로 전달하는 TLS 리버스 프록시를 두세요. 공개 호스트는
   `public_url`과 같아야 하며, 다른 호스트로 온 요청은 421로 거부됩니다.
3. 클라이언트를 `https://mcp.example.com/mcp`에 연결하세요. 예:
   `claude mcp add --transport http opsgate https://mcp.example.com/mcp`.
   claude.ai나 ChatGPT에서는 커넥터로 추가하세요.

외부에 공개하기 전에 클라이언트 리다이렉트 호스트를 제한하세요
(`allowed_redirect_hosts`). GitHub, Google 등 다른 로그인 방식은
[인증 문서](docs/ko/authentication.md)에, 프록시 경로·systemd 유닛·릴리스 검증은
[배포 문서](docs/ko/deploy.md)에 있습니다.

## 보안 모델 요약

- 인스턴스 하나가 신뢰 그룹 하나입니다. `ops:read`는 켜진 provider 전체에 적용됩니다.
  운영 서버, 개인 노트, 코딩 에이전트처럼 신뢰 수준이 다른 대상은 인스턴스와 OS
  계정을 나누세요.
- 스코프, 서버 도구 정책, 사용자별 도구 목록, provider 허용 목록을 차례로 검사하고,
  마지막으로 OS 권한과 Kubernetes RBAC가 접근을 제한합니다.
- 공격자가 장악한 클라이언트는 클라이언트 안에 뜬 승인 요청을 스스로 수락할 수 있습니다.
  브라우저 승인만 허용하려면 `approval.require_web: true`로 설정하세요.

자세한 내용: [보안 정책](docs/ko/security.md)

## 문서

| 문서 | 내용 |
| --- | --- |
| [구조](docs/ko/architecture.md) | 요청 흐름, 패키지, 설계 원칙 |
| [인증](docs/ko/authentication.md) | 패스키, GitHub, OIDC, 외부 JWT, 고정 토큰, stdio |
| [승인](docs/ko/approval.md) | 계획과 실행, 승인 경로, 승인 시간 창 |
| [클라이언트 설정](docs/ko/clients.md) | Claude Code, Claude Desktop, claude.ai, ChatGPT, Cursor |
| [설정](docs/ko/configuration.md) | 모든 설정 키와 기본값, 감사 로그 형식 |
| [도구](docs/ko/tools.md) | 도구 인자, 제한, 마스킹 |
| [노트](docs/ko/notes.md) | Markdown 폴더에 런북과 장애 기록 남기기 |
| [배포](docs/ko/deploy.md) | 릴리스 검증, systemd, Kubernetes, 리버스 프록시 |
| [Dev provider](docs/ko/dev.md) | 별도 runner로 코딩 에이전트 실행 |
| [로드맵](docs/ko/roadmap.md) | 완료한 기능과 앞으로의 계획 |

## 기여와 라이선스

[CONTRIBUTING.md](CONTRIBUTING.md)를 참고하세요. 라이선스는 [Apache-2.0](LICENSE)이며,
저작권은 2026 The opsgate Authors에 있습니다.
