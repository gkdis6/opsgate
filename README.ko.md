# opsgate

opsgate는 명시적 정책, 변경에 대한 사람의 승인, 감사 기록을 통해 MCP 클라이언트에 Linux와 Kubernetes에 대한 제한된 접근을 제공합니다.

[English](README.md) · [클라이언트 설정](docs/clients.md) · [설정](docs/configuration.md) · [도구](docs/tools.md) · [보안](SECURITY.md)

Claude, ChatGPT, Cursor 같은 클라이언트를 위한 오픈 소스 Go MCP 서버입니다. stdio와 인증된 Streamable HTTP, GitHub/OIDC 로그인을 사용하는 내장 OAuth, 외부 JWT 검증, 정적 bearer 토큰을 지원합니다. Linux 호스트와 Kubernetes 조회가 핵심 provider입니다. Obsidian vault 접근과 별도 dev runner를 통한 코딩 작업은 선택적으로 사용하는 실험적 provider입니다.

모든 provider는 기본적으로 비활성화됩니다. 호스트/Kubernetes 작업 허용 목록은 비어 있고 vault 쓰기는 꺼져 있으며, 코딩 작업 시작에는 명시적 dev 활성화와 `dev:run`이 필요합니다. 사람의 승인을 사용하는 도구는 9개입니다: `host_service_restart`, `k8s_rollout_restart`, `k8s_scale`, `k8s_delete_pod`, `obsidian_create`, `obsidian_append`, `obsidian_replace`, `obsidian_trash`, `dev_task_start`. 각 도구는 provider 및 작업 설정이 허용할 때만 등록되며 서버 도구 정책도 적용됩니다. `dev_task_cancel`은 위험이 낮은 명시적 예외로, 추가 승인 없이 본인 작업을 중지합니다(runner 관리자는 모든 작업을 중지할 수 있습니다).

**릴리스 상태:** 이 기능들은 구현되었으며 첫 공개 릴리스를 준비하고 있습니다. 제3자 보안 검토는 아직 받지 않았습니다. 비밀정보 마스킹은 최선의 노력이며 도구 출력에는 프롬프트 인젝션이 포함될 수 있습니다. 배포 전에 [신뢰 모델](SECURITY.md)을 읽으세요.

## stdio 빠른 시작

Go 1.26 이상으로 빌드합니다:

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest
# 또는 체크아웃한 소스에서:
make build
```

다음을 `/path/opsgate.yaml`로 저장합니다:

```yaml
stdio:
  scopes: [ops:read]
providers:
  host:
    enabled: true
    units: [ssh.service, "nginx*"]
```

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

경로는 절대 경로로 바꾸세요. 소스 빌드는 `bin/opsgate`를 생성합니다. 호스트 조회는 Linux의 systemd/journald와 `/proc`를 사용하며, stdio는 실행한 OS 계정을 신뢰합니다. 비특권 계정으로 실행하고 설정 파일의 소유권을 보호하세요. 설정이 없으면 모든 provider가 비활성화됩니다. Desktop, Cursor, SSH는 [클라이언트 설정](docs/clients.md)을, 전체 설정은 [읽기 전용 예제](opsgate.example.yaml)를 참고하세요.

## 패스키를 사용하는 HTTP 빠른 시작 (IdP 불필요)

가장 간단한 원격 구성은 Google, GitHub 같은 외부 IdP가 필요 없습니다. 사용자는 패스키(Face ID, Touch ID, Windows Hello, 휴대폰, 보안 키)로 로그인합니다.

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
```

처음 시작하면 패스키가 없는 사용자마다 24시간 동안 유효한 일회용 설정 링크를 로그에 출력합니다. 휴대폰이나 컴퓨터에서 열고 **Create passkey**를 누르세요. 이후 링크는 `sudo -u opsgate opsgate passkey invite -config /etc/opsgate/opsgate.yaml -subject passkey:alice`로 발급합니다. 클라이언트를 연결하면 외부 로그인 대신 **Sign in with passkey** 화면이 나옵니다.

## Google을 사용하는 HTTP 빠른 시작

정확한 리디렉션 URI가 `https://mcp.example.com/oauth/callback`인 Google OIDC 웹 애플리케이션을 만드세요. 아래 origin, 클라이언트 ID, 검증된 Workspace 이메일을 바꾸세요(Gmail/Googlemail 또는 불변 `oidc:<sub>` 식별자도 사용 가능합니다). 먼저 비공개 쓰기 가능 상태 디렉터리와 보호된 클라이언트 비밀 파일을 준비하세요.

```yaml
http:
  listen: 127.0.0.1:8787
  public_url: https://mcp.example.com
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: oidc
      issuer: https://accounts.google.com
      client_id: YOUR_GOOGLE_CLIENT_ID
      client_secret_file: /etc/opsgate/google-client-secret
    users:
      - subject: email:operator@example.com
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
claude mcp add --transport http opsgate https://mcp.example.com/mcp
```

신뢰하는 역방향 프록시에서 TLS를 종료하고 Host를 보존하며 `/mcp`, `/.well-known/*`, `/oauth/*`, `/approve/*`, `/admin/clients`를 전달하세요. 공개 요청의 Host는 `public_url`과 일치해야 합니다(명시적 포트 포함). 다른 Host는 421을 반환하지만 루프백 리스너의 로컬 루프백 요청에는 예외가 있습니다. SDK의 localhost 검사는 이 검사로 대체되므로 Host를 보존하는 프록시 뒤의 루프백 백엔드를 사용할 수 있습니다. probe와 템플릿은 [배포 문서](docs/deploy.md)를 참고하세요. Claude/ChatGPT OAuth 커넥터는 내장 동적 클라이언트 등록을 사용할 수 있습니다. 공개 전에 리디렉션 호스트 정책을 설정하세요. 이 최소 예제는 호스트 목록을 비워 두므로 다른 검증을 통과한 모든 리디렉션 호스트를 허용합니다.

## 정책과 승인

하나의 인스턴스는 하나의 신뢰 그룹입니다. `ops:read`는 서버, 클러스터 리소스, 노트를 포함한 모든 활성화된 provider의 읽기 접근을 허용합니다. `server.tools`는 존재하는 도구를 제어하고 사용자/토큰의 `tools` glob은 호출을 추가로 제한합니다. 프로덕션 운영, 개인 노트, 코딩 에이전트처럼 신뢰 수준이 다르면 별도 인스턴스와 OS 계정을 사용하세요. Kubernetes 네임스페이스 규칙은 RBAC를 보완하며 Node와 PersistentVolume은 제한하지 않습니다. Secrets 도구나 임의 셸/명령 도구는 노출하지 않습니다. 설정된 dev 에이전트는 자체 권한과 샌드박스 안에서 명령을 실행할 수 있습니다.

호스트/Kubernetes 및 vault 쓰기에는 `ops:write`, provider 작업 허용 설정, OS/RBAC 권한이 필요합니다. 모든 dev 도구에는 `dev:run`이 필요하며 HTTP에는 추가로 `ops:read`가 필요합니다. 서버는 작업을 계획한 뒤 사람의 승인을 요청합니다. Elicitation은 클라이언트 UI를 사용합니다. 내장 브라우저 승인은 같은 사용자로 로그인하며 일회성 결정을 정확한 인자와 계획에 연결합니다. `approval.web: true`와 `approval.require_web: true`를 함께 설정하면 브라우저 승인을 강제하고 stdio 쓰기를 거부합니다. 침해된 클라이언트는 자신의 elicitation 요청을 스스로 수락할 수 있습니다.

승인 시간 창은 기본적으로 꺼져 있습니다. 사용자가 선택한 시간 창은 만료까지 클라이언트와 대상을 가리지 않고 사용자/도구 쌍에 적용되며 메모리에만 저장됩니다. `dev_task_start`에는 시간 창을 활성화하지 마세요. Worktree는 샌드박스가 아니며 작업 소유권은 신뢰하는 소켓 상대가 주장한 사용자에 의존합니다. 큰 노트 미리보기는 생략된 줄 수를 표시하며 잘리고, vault 업데이트의 낙관적 확인과 rename 사이에 발생한 외부 편집은 덮어쓸 수 있습니다.

인자는 기본 16 KiB입니다. 노트 내용 도구는 JSON 528 KiB와 내용/치환 입력당 256 KiB를, `dev_task_start`는 인자 48 KiB와 프롬프트 32 KiB를 허용합니다. 요약 한도는 `max(64 KiB, 인자 한도 + 16 KiB)`입니다. 4 MiB를 넘는 provider 출력은 생략하며 나머지 출력은 마스킹한 뒤 설정된 한도(기본 64 KiB)로 자릅니다. 호출과 쓰기 단계에는 협조적 시간 제한이 있고 감사 기록에는 거부와 승인이 포함됩니다. [도구](docs/tools.md), [설정 및 감사 참조](docs/configuration.md), [dev 설정](docs/dev.md), [보안 한계](SECURITY.md)를 참고하세요.

## 개발과 라이선스

[CONTRIBUTING.md](CONTRIBUTING.md), [배포 및 릴리스 검증](docs/deploy.md), [로드맵](docs/roadmap.md)을 참고하세요. [Apache-2.0](LICENSE) 라이선스이며 저작권은 2026 The opsgate Authors에 있습니다.
