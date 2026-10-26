# opsgate

[English](README.md)

**셸을 내주지 않고도 AI 어시스턴트가 서버 운영을 돕게 합니다.**

opsgate는 Go로 작성한 오픈 소스 MCP 서버입니다. Claude, ChatGPT, Claude Code,
Cursor 같은 클라이언트에 Linux 호스트와 Kubernetes용 운영 도구를 정해진 범위 안에서만
제공합니다. 명시적인 정책과 감사 로그를 갖추고, 모든 변경에는 사람의 승인을 받습니다.

> **상태:** 첫 공개 릴리스에 필요한 기능은 모두 구현했지만, 아직 제3자 보안 검토를
> 받지 않았습니다. 비밀값 마스킹은 최선의 노력 수준이며, 도구 출력에는 프롬프트
> 인젝션이 섞여 있을 수 있습니다. 배포 전에 [보안 정책](docs/ko/security.md)을 읽으세요.

## 왜 opsgate인가

- **임의 명령을 실행하지 않습니다.** 목적이 정해진 도구만 있습니다. 셸 도구도,
  Kubernetes Secret 조회 도구도 없습니다.
- **모든 기능이 꺼진 상태로 시작합니다.** provider, 쓰기 작업, 허용 목록은 설정에서
  직접 켜기 전까지 동작하지 않습니다.
- **변경에는 사람이 필요합니다.** 쓰기 도구는 먼저 바꿀 내용을 정확히 계산하고,
  클라이언트 안이나 브라우저에서 사람의 승인을 기다립니다. 승인은 그 인자와 계획에만
  유효합니다.
- **원격 접속은 인증을 거칩니다.** 내장 OAuth 2.1 서버가 claude.ai와 ChatGPT
  커넥터를 지원합니다. 로그인은 패스키, GitHub, OIDC(Google 포함) 중에서 고르거나,
  외부 JWT를 검증하도록 설정할 수 있습니다.
- **출력은 신뢰하지 않습니다.** 결과는 마스킹하고 크기를 제한한 뒤 JSONL 감사 로그에
  남깁니다.

## 할 수 있는 일

| Provider | 조회 도구 | 쓰기 도구(승인 필요) |
| --- | --- | --- |
| 호스트(systemd, journald, `/proc`) | 개요, 서비스 목록, 유닛 상태, 로그, 상위 프로세스 | 허용된 유닛 재시작 |
| Kubernetes | 개요, 목록, 상세, 이벤트, 파드 로그 | rollout restart, scale, 파드 삭제 |
| Obsidian 볼트 *(실험적)* | 검색, 읽기, 목록, 최근 노트, 링크, 태그 | 생성, 덧붙이기, 치환, `.trash`로 이동 |
| Dev *(실험적)* | 프로젝트, 작업 상태, 로그, diff, 목록 | Git worktree에서 코딩 에이전트(Codex, Claude) 실행 |

쓰기 도구는 `ops:write` 스코프와 도구별 허용 목록 항목도 필요합니다. 인자와 제한은
[도구 문서](docs/ko/tools.md)에 모두 정리되어 있습니다.

## 동작 방식

```text
 MCP 클라이언트 ──stdio 또는 HTTPS──▶ opsgate
                                      ├─ 인증 (OAuth / JWT / 고정 토큰)
                                      ├─ 도구 정책과 스코프 확인
                                      ├─ 쓰기 도구: 계획 ─▶ 사람의 승인 ─▶ 실행
                                      ├─ 출력 마스킹, 크기 제한
                                      └─ 감사 로그 (JSONL)
                                            │
                  호스트 · Kubernetes · Obsidian · dev-runner(별도 계정)
```

자세한 내용: [구조](docs/ko/architecture.md) · [승인](docs/ko/approval.md) ·
[인증](docs/ko/authentication.md)

## 빠른 시작: 로컬(stdio)

Go 1.26 이상으로 빌드합니다.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest   # 또는: make build
```

설정 파일을 저장합니다. 예: `/path/opsgate.yaml`

```yaml
stdio:
  scopes: [ops:read]
providers:
  host:
    enabled: true
    units: [ssh.service, "nginx*"]
```

Claude Code에 등록합니다. 경로는 절대 경로로 쓰세요.

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

stdio는 실행한 계정의 권한으로 동작하므로 권한이 낮은 계정으로 실행하세요. 다른
클라이언트는 [클라이언트 설정](docs/ko/clients.md)을 참고하세요.

## 빠른 시작: 원격(HTTP + 패스키)

패스키는 별도의 ID 제공자가 필요 없습니다. Face ID, Touch ID, Windows Hello,
휴대폰, 보안 키로 로그인합니다.

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
   `sudo -u opsgate opsgate passkey invite -config /etc/opsgate/opsgate.yaml -subject passkey:alice`를 실행합니다.
2. 앞단에 `Host` 헤더를 그대로 전달하는 TLS 리버스 프록시를 둡니다. 공개 호스트는
   `public_url`과 같아야 하며, 다른 호스트로 온 요청은 421로 거부됩니다.
3. 클라이언트를 `https://mcp.example.com/mcp`에 연결합니다. 예:
   `claude mcp add --transport http opsgate https://mcp.example.com/mcp`.
   claude.ai나 ChatGPT에서는 커넥터로 추가합니다.

외부에 공개하기 전에 클라이언트 리다이렉트 호스트를 제한하세요
(`allowed_redirect_hosts`). GitHub, Google 등 다른 로그인 방식은
[인증 문서](docs/ko/authentication.md)에, 프록시 경로·systemd 유닛·릴리스 검증은
[배포 문서](docs/ko/deploy.md)에 있습니다.

## 보안 모델 요약

- 인스턴스 하나가 신뢰 그룹 하나입니다. `ops:read`는 켜진 provider 전체에 적용됩니다.
  운영 서버, 개인 노트, 코딩 에이전트처럼 신뢰 수준이 다른 대상은 인스턴스와 OS
  계정을 나누세요.
- 권한 검사는 여러 겹입니다. 스코프, 서버 도구 정책, 사용자별 도구 목록, provider
  허용 목록을 거친 뒤 마지막으로 OS 권한과 Kubernetes RBAC가 적용됩니다.
- 손상된 클라이언트는 클라이언트 안에 뜬 승인 요청을 스스로 수락할 수 있습니다.
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
| [배포](docs/ko/deploy.md) | 릴리스 검증, systemd, Kubernetes, 리버스 프록시 |
| [Dev provider](docs/ko/dev.md) | 별도 runner로 코딩 에이전트 실행 |
| [로드맵](docs/ko/roadmap.md) | 완료한 기능과 앞으로의 계획 |

## 기여와 라이선스

[CONTRIBUTING.md](CONTRIBUTING.md)를 참고하세요. 라이선스는 [Apache-2.0](LICENSE)이며,
저작권은 2026 The opsgate Authors에 있습니다.
