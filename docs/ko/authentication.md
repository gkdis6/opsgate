# 인증

[English](../authentication.md)

HTTP를 사용하려면 `auth.mode`를 `builtin`, `external`, `static` 중 하나로 설정하세요. 로컬에서 사용하려면 대신 `stdio` 전송 방식을 선택하세요. stdio는 로그인 없이 사용하는 전송 방식이며, 인증 모드가 아닙니다.

## 로그인 방식 선택

| 선택 | ID 출처 | 선택 기준 | 웹 승인 |
| --- | --- | --- | --- |
| 내장 + 패스키 | 로컬 WebAuthn 자격 증명, 보통 `passkey:alice` | 외부 인증 provider 없이 개인 용도로 사용 | 활성화 시 가능 |
| 내장 + GitHub | `github:<login>` | 여러 기기에서 기존 GitHub ID 사용, 계정 보호는 GitHub에서 설정 | 활성화 시 가능 |
| 내장 + OIDC | 명시적으로 허용한 `oidc:<sub>` 또는 검증된 `email:<address>` | 조직 로그인 또는 Google 계정 사용, MCP 클라이언트용 OAuth는 opsgate에서 제공 | 활성화 시 가능 |
| External | 발급자 JWT 액세스 토큰의 문자열 클레임 | 기존 인가 서버가 클라이언트 로그인과 토큰 관리 | 불가 |
| Static | `static:<name>` | Bearer 헤더를 설정할 수 있는 스크립트/클라이언트 | 불가 |
| stdio | `stdio.user` 레이블(보안 경계는 실행한 OS 계정) | 로컬 프로세스 연동 및 자동화 | 불가(클라이언트 내 확인만 가능) |

claude.ai, ChatGPT처럼 OAuth를 쓰는 원격 클라이언트에는 내장 인가 서버나 호환되는 외부 인가 서버를 선택하세요. static 모드는 OAuth 로그인을 제공하지 않으므로, Claude Code, Cursor, 스크립트처럼 헤더를 직접 설정할 수 있는 클라이언트에서 사용하세요. 연결 설정은 [클라이언트](clients.md)를 참고하세요.

```text
builtin: 클라이언트 -> opsgate OAuth -> 패스키 / GitHub / OIDC 로그인
                                   -> 동의 -> opsgate 액세스/리프레시 토큰
external: 클라이언트 -> 외부 인가 서버 -> JWT -> opsgate JWKS 검증
static: 클라이언트 -> 설정한 Bearer 토큰 -> opsgate SHA-256 비교
stdio: 클라이언트 -> 로컬 opsgate 프로세스 -> 설정된 stdio ID/스코프
```

모든 HTTP 모드는 `/mcp`에서 `ops:read`를 요구합니다. 추가 스코프로 도구별 사용 권한을 제어하며, 보통 쓰기에는 `ops:write`, 코딩 도구에는 `dev:run`이 필요합니다. 스코프가 있어도 provider가 활성화되거나 사람의 승인을 건너뛸 수 있는 것은 아닙니다. 사용자/토큰에 `tools` 글로브를 설정하면 서버 정책에 더해 호출 범위를 제한할 수 있습니다.

`approval.web: true`인 HTTP에서는 기본적으로 웹 승인만 허용합니다(`approval.require_web: true`). builtin 인증을 쓰는 모드는 웹 승인을 자동으로 켭니다. `approval.require_web: false`를 명시하면 클라이언트 내 확인을 허용하며, 침해된 클라이언트가 자기 쓰기를 스스로 승인할 수 있습니다. external/static 인증에는 웹 채널이 없어 `require_web`의 기본값이 계속 false입니다. HTTP 시작 시 클라이언트 내 확인이 허용되어 있으면 경고를 남깁니다.

## 내장 OAuth

opsgate는 클라이언트 등록(DCR), S256 PKCE, 동의 화면, 액세스 토큰, 리프레시 토큰 교체, 토큰 폐기를 처리합니다. 사용자는 `auth.builtin.users`에 등록하세요. 한 번에 활성화할 수 있는 `upstream.provider`는 하나이므로, 한 프로세스에서 패스키, GitHub, OIDC를 함께 제공할 수는 없습니다.

아래는 `config.Load`로 읽을 수 있는 최소 구성의 전체 설정입니다. provider는 모두 꺼져 있으므로 필요한 provider를 별도로 활성화하세요.

```yaml
http:
  listen: 127.0.0.1:8787
  public_url: https://example.com
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: passkey
    users:
      - subject: passkey:alice
        scopes: [ops:read, ops:write]
approval:
  web: true
```

```sh
opsgate -config opsgate.yaml -transport http
```

외부에 HTTPS로 제공하려면 리버스 프록시를 사용하세요. MCP URL은 `https://example.com/mcp`이고 내장 issuer는 `https://example.com`입니다. 로컬 개발에서는 루프백 HTTP도 허용하며, HTTP 리스너 자체는 TLS를 종료하지 않습니다.

### 패스키

패스키 로그인에는 외부 애플리케이션, 클라이언트 ID, 비밀값이 필요하지 않습니다. `passkey:alice` 같은 subject를 설정하세요. 서버가 시작할 때 패스키를 등록하지 않은 사용자의 등록 초대 링크를 프로세스 로그에 남기며, 유효 기간은 24시간입니다. 초대 링크는 자격 증명처럼 취급하세요. 이 링크는 해당 사용자의 패스키 등록 권한을 부여하므로 opsgate 자체 로그에 도구가 접근하지 못하게 하세요.

패스키를 추가하려면 서버를 실행하는 OS 계정으로 다음 명령을 실행하세요.

```sh
opsgate passkey invite -config opsgate.yaml -subject passkey:alice -ttl 24h
```

이 명령에는 `state_file`이 필요하며, 상태 파일과 같은 디렉터리의 `passkey-invites`에 해시 처리한 초대 파일을 씁니다. 사용자가 설정에 등록되어 있고 활성 provider가 `passkey`여야 합니다. 초대는 일회용이며, 유효 기간은 기본 24시간, 최대 7일입니다. 상태를 저장하도록 설정하면 등록한 패스키도 상태 파일에 저장해 재시작 후에도 유지합니다.

### GitHub

콜백이 `https://example.com/oauth/callback`인 GitHub OAuth App을 만드세요. 전체 설정 예시의 HTTP origin과 상태 파일은 유지하고, 내장 upstream과 사용자 설정을 다음과 같이 교체하세요.

```yaml
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: github
      client_id: example-client-id
      client_secret_env: OPSGATE_GITHUB_SECRET
    users:
      - subject: github:alice
        scopes: [ops:read, ops:write]
```

서버 프로세스에 위에서 지정한 환경 변수를 설정하세요. 서버는 GitHub 로그인 이름을 받아 소문자로 바꾼 `github:<login>`을 subject로 사용합니다. 계정 인증은 GitHub가 담당하며, opsgate가 GitHub 2FA 정책을 강제하지는 않습니다.

### Google을 포함한 OIDC

콜백이 `https://example.com/oauth/callback`인 OIDC 애플리케이션을 등록하고, upstream과 사용자 설정을 다음과 같이 교체하세요.

```yaml
auth:
  mode: builtin
  builtin:
    state_file: /var/lib/opsgate/oauth.json
    upstream:
      provider: oidc
      issuer: https://accounts.google.com
      client_id: example-client-id
      client_secret_file: /etc/opsgate/oidc-secret
    users:
      - subject: email:alice@example.com
        scopes: [ops:read]
```

허용 목록에 명시한 `oidc:<sub>`를 우선 사용하며, 이때는 이메일 제공 여부나 소유권과 관계없이 허용합니다. 그 외에는 ID 토큰에 검증된 이메일이 있어야 하며, 서버는 이를 소문자로 바꿔 `email:<address>`를 사용합니다. Google 이메일 매핑에서는 Gmail/Googlemail 주소나 `hd`가 이메일 도메인과 일치하는 Workspace 주소를 허용합니다. 타사 메일함은 이메일 검증만으로 이 매핑을 통과할 수 없습니다. 따라서 Google에서 예시의 주소를 사용하려면 그 주소에 맞는 Workspace 클레임이 필요합니다. 대신 사용자의 정확한 `oidc:<sub>`를 설정해도 됩니다.

upstream 비밀값은 YAML에 직접 넣지 말고 `client_secret_file` 또는 `client_secret_env`로 지정하세요. 둘 다 설정하면 파일을 우선 사용합니다. 패스키 이외의 upstream은 시작할 때 클라이언트 ID와 비밀값이 모두 비어 있지 않아야 합니다. OIDC 검색과 엔드포인트 검증도 초기화 중에 처리합니다.

### 클라이언트 등록과 관리자

필요하면 아래 내용을 기존 내장 설정에 추가하세요.

```yaml
auth:
  builtin:
    allowed_redirect_hosts: [example.com, localhost]
    unknown_redirect_hosts: approve
    admins: [passkey:alice]
    users:
      - subject: passkey:alice
        scopes: [ops:read, ops:write]
```

모든 관리자는 허용된 사용자 목록에도 있어야 합니다. 사용하는 upstream에 맞게 관리자 subject를 바꾸세요. 리다이렉트 호스트 목록이 비어 있지 않으면 목록에 없는 호스트는 기본적으로 거부합니다. `unknown_redirect_hosts: approve`를 설정하면 거부하는 대신 클라이언트를 대기 상태로 두고 `/admin/clients`에서 관리자 검토를 받도록 합니다. 호스트 목록이 비어 있으면 다른 검증을 통과한 모든 리다이렉트 호스트를 허용합니다. 정확히 일치하는 호스트와 지원되는 private scheme을 허용합니다. `localhost`는 루프백 IP와 모든 포트까지 포함합니다. 등록 정책과 선택 사항인 `notify_url` 알림은 [설정](configuration.md)을 참고하세요.

클라이언트 검토는 개별 작업에 대한 사람의 승인과 별개입니다. 내장 토큰의 기본 유효 기간은 액세스 토큰 1시간, 리프레시 토큰 30일입니다. `state_file`에는 클라이언트 기록, 토큰 해시, 폐기/코드 상태, 패스키를 `0600` 권한으로 저장해 재시작 후에도 유지합니다. 이 설정이 비어 있으면 재시작할 때 상태가 사라집니다. 브라우저 세션은 메모리에만 보관합니다. 기존 상태 파일의 issuer/resource가 호환되지 않으면 서버가 시작되지 않습니다. 상태 스냅샷은 읽기와 쓰기 모두 512 MiB로 제한합니다. 쓰기 중 이 제한을 넘으면 이전 스냅샷을 보존하고, 다른 저장 실패와 마찬가지로 실행 중인 인스턴스는 인증을 거부합니다.

## 외부 인가 서버

HTTP 설정은 유지하고 `auth`를 다음과 같이 교체하세요.

```yaml
auth:
  mode: external
  external:
    issuer: https://identity.example.com
    audience: https://example.com/mcp
    user_claim: sub
    users:
      - subject: claim:alice
        scopes: [ops:read, ops:write]
approval:
  web: false
  require_web: false # 웹 채널이 없어 클라이언트 내 확인을 계속 허용합니다.
```

opsgate는 issuer의 메타데이터와 JWKS를 검색해 JWT 서명, issuer, audience, 만료를 검증합니다. issuer와 JWKS 엔드포인트는 루프백 HTTP를 제외하면 HTTPS를 사용해야 합니다. 클라이언트 등록, 로그인, 동의, 토큰 발급은 외부 서버가 담당하며, 클라이언트와 호환되는 절차를 제공해야 합니다. 이 검증기는 opaque 액세스 토큰을 지원하지 않습니다. JWKS를 가져오는 시도는 모든 요청이 공유하고, 실패한 시도를 포함해 1분에 한 번으로 제한합니다. 캐시된 유효한 키는 계속 사용할 수 있지만, 새 서명 키는 대기 시간이 끝날 때까지 거부될 수 있습니다.

`audience`의 기본값은 `http.public_url`에 `/mcp`를 붙인 값입니다. `user_claim`의 기본값은 `sub`이며, 이 클레임의 값은 비어 있지 않은 문자열이어야 합니다. 호출자 ID와 감사 로그에 기록하는 ID는 `alice` 같은 클레임 값이고, `claim:` 접두사는 설정과 대조할 때만 사용합니다.

`users`가 비어 있으면 서버가 인식하는 토큰 스코프를 신뢰합니다. 사용자를 설정하면 목록에 있는 ID만 허용하고, 토큰과 설정에 공통으로 있는 스코프만 적용하므로 설정이 권한을 추가하지는 않습니다. `scope`는 공백으로 구분한 문자열이며, 그 외에는 문자열 또는 문자열 배열인 `scp`를 사용할 수 있습니다. 알 수 없는 스코프는 무시합니다. 웹 승인은 사용할 수 없으므로, 승인이 필요한 쓰기에는 클라이언트 내 확인을 지원하는 클라이언트가 필요합니다.

## Static Bearer 토큰

토큰과 해시를 생성하세요.

```sh
opsgate -gen-token
```

원본 토큰은 클라이언트에 보관하고, 설정에는 64자리 16진수 SHA-256 해시만 복사하세요. 아래 설정의 해시는 문법상 유효한 예시 값이므로, 사용하기 전에 생성한 해시로 교체하세요.

```yaml
auth:
  mode: static
  static:
    - name: alice
      sha256: "0000000000000000000000000000000000000000000000000000000000000000"
      scopes: [ops:read]
approval:
  web: false
  require_web: false # 웹 채널이 없어 클라이언트 내 확인을 계속 허용합니다.
```

HTTP 설정은 유지하고, `/mcp`에 `Authorization: Bearer <raw token>`을 보내세요. ID는 `static:alice`이며, static 토큰은 만료되지 않습니다. 토큰을 폐기하려면 설정에서 그 해시를 지우거나 바꾼 뒤 재시작하세요. 웹 승인은 사용할 수 없으므로 승인이 필요한 쓰기에는 클라이언트 내 확인이 필요합니다.

## 로컬 stdio

```yaml
stdio:
  user: alice
  scopes: [ops:read]
```

```sh
opsgate -config opsgate.yaml -transport stdio
```

이 설정은 HTTP/인증 설정 없이 읽을 수 있습니다. 전송 방식의 기본값은 stdio이고, ID 레이블의 기본값은 `local`, 스코프의 기본값은 `[ops:read]`입니다. `stdio.user`는 레이블일 뿐 OS 사용자를 전환하는 설정이 아니며, 실제 리소스 접근 권한은 프로세스를 실행한 OS 계정이 결정합니다. 승인이 필요한 쓰기에는 클라이언트 내 확인이 필요합니다. `require_web`을 생략하면 모드 파일이라도 `-transport stdio`에서 클라이언트 내 확인을 허용합니다. 사용자가 로컬에서 프로세스를 시작하기 때문입니다. 명시적 `approval.require_web: true`는 계속 stdio 쓰기를 거부합니다. `config show`는 HTTP의 웹 전용 기본값을 명시하므로, 그 출력으로 stdio 쓰기를 하려면 키를 지우거나 `false`로 바꾸세요.

## 방식 전환

1. 전환할 인증 provider나 토큰을 준비하고, 가능하면 HTTP origin을 유지하세요. 내장 상태를 파일로 저장하고 있다면 상태 파일을 백업하세요.
2. HTTP는 `auth.mode`를 바꾸고, stdio는 `-transport stdio`를 사용하세요. 내장 로그인에서는 `auth.builtin.upstream.provider`를 정확히 하나 지정하고 필수 항목을 설정하세요. 설정은 시작할 때 읽으므로 변경 사항을 적용하려면 재시작하세요.
3. `users`, `admins`, runner 관리자 목록의 subject와 권한을 갱신하세요. 예시는 `passkey:alice`, `github:alice`, `email:alice@example.com`, 사용자의 정확한 `oidc:<sub>`, 외부 설정의 `claim:alice`(호출자는 `alice`), `static:alice`입니다.
4. 내장 HTTP에서 다른 방식으로 바꿀 때는 `approval.web: false`로 설정하고 `approval.require_web`을 지우거나 `false`로 바꾸세요. 승인이 필요한 쓰기에는 클라이언트 내 확인을 지원하는 클라이언트를 선택하세요. 이 경로에서는 침해된 클라이언트가 자기 쓰기를 스스로 승인할 수 있습니다. stdio에는 웹 채널이 없으며 `require_web`을 생략하면 클라이언트 내 확인을 허용합니다. 내장 HTTP로 돌아올 때는 명시적 `require_web: false`를 지워 웹 전용 기본값을 복원하세요.
5. 서버를 재시작하고 새 방식으로 클라이언트를 다시 연결하거나 인가하세요. 쓰기를 요청하기 전에 읽기 작업으로 ID, 스코프, provider/도구 정책을 확인하세요.

provider를 바꿔도 저장된 내장 토큰이 모두 자동으로 폐기되지는 않습니다. 기존 토큰은 현재 허용된 사용자와 스코프를 기준으로 검사하므로, 이전 접근을 차단하려면 기존 subject/권한을 명시적으로 제거하세요. external/static 모드에서는 내장 상태 파일을 읽지 않습니다. 파일을 유지하면 호환되는 동일한 origin, subject, 자격 증명으로 패스키 로그인에 돌아왔을 때 등록한 패스키를 다시 사용할 수 있습니다. upstream을 바꾸기 위해 이 파일을 삭제하지 마세요. 공개 issuer/resource를 바꾸면 그에 맞는 새 상태가 필요하고, 클라이언트도 다시 연결해야 합니다.

알 수 없는 YAML 키가 있으면 `config.Load`가 실패합니다. 시작 과정에서 HTTP, 인증, provider를 추가로 검사하므로 YAML을 읽는 데 성공했다고 해서 정상적으로 배포할 수 있는 것은 아닙니다. [설정](configuration.md), [사람의 승인](approval.md), [배포](deploy.md)를 참고하세요.
