# 인증

[English](../authentication.md)

HTTP는 `auth.mode`를 `builtin`, `external`, `static` 중 하나로 선택합니다. 로컬 사용은 대신 `stdio` 전송 방식을 선택합니다. stdio에는 로그인이 없으며 인증 모드가 아닙니다.

## 로그인 방식 선택

| 선택 | ID 출처 | 선택 기준 | 웹 승인 |
| --- | --- | --- | --- |
| 내장 + 패스키 | 로컬 WebAuthn 자격 증명, 보통 `passkey:alice` | 외부 identity provider 없이 개인 사용 | 활성화하면 가능 |
| 내장 + GitHub | `github:<login>` | 여러 기기에서 기존 GitHub ID 사용. 계정 보호는 GitHub에서 설정 | 활성화하면 가능 |
| 내장 + OIDC | 명시적으로 허용한 `oidc:<sub>` 또는 검증된 `email:<address>` | 조직 로그인 또는 Google 계정 사용. MCP 클라이언트용 OAuth는 opsgate가 제공 | 활성화하면 가능 |
| External | 발급자 JWT 액세스 토큰의 문자열 클레임 | 기존 인가 서버가 클라이언트 로그인과 토큰 관리 | 불가 |
| Static | `static:<name>` | Bearer 헤더를 설정할 수 있는 스크립트/클라이언트 | 불가 |
| stdio | `stdio.user` 라벨. 실행한 OS 계정이 경계 | 로컬 프로세스 연동 및 자동화 | 불가. 클라이언트 내 확인만 가능 |

claude.ai, ChatGPT처럼 OAuth를 쓰는 원격 클라이언트에는 내장 또는 호환되는 외부 인가 서버를 선택합니다. static 모드는 OAuth 로그인을 제공하지 않습니다. Claude Code, Cursor, 스크립트처럼 헤더를 직접 설정하는 클라이언트에서 사용합니다. 클라이언트 설정은 [클라이언트](clients.md)를 참고합니다.

```text
builtin: 클라이언트 -> opsgate OAuth -> 패스키 / GitHub / OIDC 로그인
                                   -> 동의 -> opsgate 액세스/리프레시 토큰
external: 클라이언트 -> 외부 인가 서버 -> JWT -> opsgate JWKS 검증
static: 클라이언트 -> 설정한 Bearer 토큰 -> opsgate SHA-256 비교
stdio: 클라이언트 -> 로컬 opsgate 프로세스 -> 설정된 stdio ID/스코프
```

모든 HTTP 모드는 `/mcp`에서 `ops:read`를 요구합니다. 추가 스코프는 도구를 인가합니다. 보통 쓰기에는 `ops:write`, 코딩 도구에는 `dev:run`이 필요합니다. 스코프는 provider를 활성화하거나 사람의 승인을 우회하지 않습니다. 선택적인 사용자/토큰 `tools` 글로브는 서버 정책 위에 제한을 더합니다.

## 내장 OAuth

opsgate는 클라이언트 등록(DCR), S256 PKCE, 동의 화면, 액세스 토큰, 회전하는 리프레시 토큰, 폐기를 처리합니다. 사용자는 `auth.builtin.users`에 있어야 합니다. 한 번에 하나의 `upstream.provider`만 활성화합니다. 한 프로세스에서 패스키, GitHub, OIDC를 함께 제공할 수 없습니다.

다음 최소 전체 설정은 `config.Load`로 읽을 수 있습니다. provider는 꺼져 있습니다. 필요한 provider는 별도로 활성화합니다.

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

공개 HTTPS는 리버스 프록시로 제공합니다. MCP URL은 `https://example.com/mcp`이고 내장 issuer는 `https://example.com`입니다. 로컬 개발에는 루프백 HTTP를 허용합니다. HTTP 리스너 자체는 TLS를 종료하지 않습니다.

### 패스키

패스키 로그인에는 외부 애플리케이션, client ID, secret이 필요하지 않습니다. `passkey:alice` 같은 subject를 설정합니다. 시작할 때 등록된 패스키가 없는 사용자의 설정 초대를 프로세스 로그에 남깁니다. 유효 기간은 24시간입니다. 초대 링크는 자격 증명처럼 취급합니다.

패스키를 추가하려면 서버의 OS 계정으로 실행합니다.

```sh
opsgate passkey invite -config opsgate.yaml -subject passkey:alice -ttl 24h
```

명령에는 `state_file`이 필요합니다. 그 옆의 `passkey-invites`에 해시로 관리하는 초대 파일을 씁니다. 사용자는 설정에 있어야 하며 활성 provider는 `passkey`여야 합니다. 초대는 일회용입니다. 기본 유효 기간은 24시간이고 최대 7일까지 가능합니다. 영속성을 설정하면 등록한 패스키는 상태 파일에 저장합니다.

### GitHub

콜백을 `https://example.com/oauth/callback`으로 하는 GitHub OAuth App을 만듭니다. 내장 upstream과 사용자를 다음으로 교체합니다. 전체 예시의 HTTP origin과 상태 파일은 유지합니다.

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

서버 프로세스에 해당 환경 변수를 설정합니다. 서버는 GitHub 로그인 이름을 얻어 소문자 `github:<login>` subject를 사용합니다. 계정 인증은 GitHub가 수행합니다. opsgate는 GitHub 2FA 정책을 강제하지 않습니다.

### Google을 포함한 OIDC

콜백을 `https://example.com/oauth/callback`으로 하는 OIDC 애플리케이션을 등록합니다. upstream과 사용자를 다음과 같이 교체합니다.

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

명시적으로 허용한 `oidc:<sub>`가 우선합니다. 이메일 제공 여부나 소유권에 의존하지 않습니다. 그 외에는 ID 토큰에 검증된 이메일이 있어야 합니다. 서버는 이를 소문자로 정규화해 `email:<address>`를 사용합니다. Google 이메일 매핑은 Gmail/Googlemail 주소 또는 `hd`가 이메일 도메인과 일치하는 Workspace 주소를 허용합니다. 검증된 타사 메일함만으로는 이 Google 이메일 매핑을 통과하지 못합니다. 따라서 예시 주소를 Google로 사용하려면 해당 Workspace 클레임이 필요합니다. 또는 사용자의 정확한 `oidc:<sub>`를 설정합니다.

upstream 비밀값은 YAML에 직접 넣지 않고 `client_secret_file` 또는 `client_secret_env`에 둡니다. 둘 다 설정하면 파일이 우선합니다. 패스키 외 upstream은 시작 시 비어 있지 않은 client ID와 secret이 필요합니다. OIDC 검색과 엔드포인트 검증도 초기화 중 수행합니다.

### 클라이언트 등록과 관리자

다음은 내장 설정 아래에 합칠 선택적 설정 조각입니다.

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

모든 관리자는 허용된 사용자이기도 해야 합니다. upstream에 맞게 관리자 subject를 바꿉니다. 리다이렉트 호스트 목록이 비어 있지 않으면 목록 밖의 호스트를 기본적으로 거부합니다. `unknown_redirect_hosts: approve`는 대신 클라이언트를 대기 상태로 두고 `/admin/clients`에서 관리자 검토를 받게 합니다. 호스트 목록이 비어 있으면 다른 검증을 통과한 모든 리다이렉트 호스트를 허용합니다. 정확한 호스트와 지원되는 private scheme을 받습니다. `localhost`는 루프백 IP와 모든 포트도 포함합니다. 등록 정책과 선택적 `notify_url` 알림은 [설정](configuration.md)을 참고합니다.

클라이언트 검토는 개별 작업에 대한 사람의 승인과 별개입니다. 내장 토큰의 기본 유효 기간은 액세스 토큰 1시간, 리프레시 토큰 30일입니다. `state_file`은 클라이언트 기록, 토큰 해시, 폐기/코드 상태, 패스키를 `0600` 모드로 저장합니다. 비어 있으면 재시작 시 상태가 사라집니다. 브라우저 세션은 메모리에 있습니다. 기존 상태 파일의 issuer/resource가 호환되지 않으면 시작하지 않습니다.

## 외부 인가 서버

HTTP 설정을 유지하고 `auth`를 다음으로 교체합니다.

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
  require_web: false
```

opsgate는 issuer의 메타데이터와 JWKS를 검색하고 JWT 서명, issuer, audience, 만료를 검증합니다. issuer와 JWKS 엔드포인트는 HTTPS가 필요합니다. 루프백 HTTP만 예외입니다. 외부 서버가 클라이언트 등록, 로그인, 동의, 토큰 발급을 담당합니다. 클라이언트와 호환되는 흐름을 제공해야 합니다. 이 검증기는 opaque 액세스 토큰을 지원하지 않습니다.

`audience`의 기본값은 `http.public_url`에 `/mcp`를 붙인 값입니다. `user_claim`의 기본값은 `sub`이며 비어 있지 않은 문자열이어야 합니다. 호출자/감사 ID는 `alice` 같은 클레임 값입니다. `claim:` 접두사는 설정 매칭에만 사용합니다.

`users`가 비어 있으면 인식하는 토큰 스코프를 신뢰합니다. 사용자를 설정하면 목록의 ID만 허용합니다. 유효 스코프는 토큰 스코프와 설정 스코프의 교집합입니다. 추가 권한 부여가 아닙니다. `scope`는 공백으로 구분한 문자열입니다. 없으면 `scp`를 문자열 또는 문자열 배열로 받을 수 있습니다. 모르는 스코프는 무시합니다. 웹 승인은 불가능합니다. 승인이 필요한 쓰기는 클라이언트 내 확인을 지원하는 클라이언트가 필요합니다.

## Static Bearer 토큰

토큰과 해시를 생성합니다.

```sh
opsgate -gen-token
```

원본 토큰은 클라이언트에 보관합니다. 설정에는 64자리 16진수 SHA-256만 복사합니다. 다음 조각의 해시는 문법상 유효한 자리표시자입니다. 사용 전에 생성한 해시로 교체합니다.

```yaml
auth:
  mode: static
  static:
    - name: alice
      sha256: "0000000000000000000000000000000000000000000000000000000000000000"
      scopes: [ops:read]
approval:
  web: false
  require_web: false
```

HTTP 설정은 유지합니다. `/mcp`에 `Authorization: Bearer <raw token>`을 보냅니다. ID는 `static:alice`입니다. static 토큰은 만료되지 않습니다. 토큰을 폐기하려면 설정에서 해당 해시를 지우거나 바꾼 뒤 재시작합니다. 웹 승인은 불가능하므로 승인이 필요한 쓰기에는 클라이언트 내 확인이 필요합니다.

## 로컬 stdio

```yaml
stdio:
  user: alice
  scopes: [ops:read]
```

```sh
opsgate -config opsgate.yaml -transport stdio
```

이 설정 조각은 HTTP/인증 설정 없이 읽을 수 있습니다. 전송 방식의 기본값은 stdio입니다. ID 라벨의 기본값은 `local`, 스코프의 기본값은 `[ops:read]`입니다. `stdio.user`는 라벨이며 OS 사용자 전환 설정이 아닙니다. 실행한 OS 계정이 실제 리소스 접근을 제어합니다. 승인이 필요한 쓰기는 클라이언트 내 확인이 필요합니다. stdio에서 `approval.require_web`은 해당 쓰기를 거부합니다.

## 방식 전환

1. 전환할 identity provider나 토큰을 준비합니다. 가능하면 HTTP origin을 유지합니다. 내장 영속성을 사용한다면 상태 파일을 백업합니다.
2. HTTP는 `auth.mode`를 바꾸고, stdio는 `-transport stdio`를 사용합니다. 내장 로그인은 `auth.builtin.upstream.provider` 하나와 필수 설정을 지정합니다. 설정은 시작 시 읽으므로 적용하려면 재시작합니다.
3. `users`, `admins`, runner 관리자 목록의 subject와 권한을 갱신합니다. 예시는 `passkey:alice`, `github:alice`, `email:alice@example.com`, 정확한 `oidc:<sub>`, 외부 설정 `claim:alice`(호출자는 `alice`), `static:alice`입니다.
4. 내장 HTTP를 떠날 때는 `approval.web: false`, `approval.require_web: false`로 설정합니다. 승인이 필요한 쓰기는 클라이언트 내 확인을 지원하는 클라이언트를 선택합니다. stdio는 해당 플래그에 관계없이 웹 승인을 사용할 수 없습니다.
5. 재시작하고 새 방식으로 클라이언트를 재연결하거나 다시 인가합니다. 쓰기를 요청하기 전에 읽기 작업으로 ID, 스코프, provider/도구 정책을 확인합니다.

provider 변경이 저장된 모든 내장 토큰을 자동으로 폐기하지는 않습니다. 기존 토큰은 현재 허용된 사용자와 스코프로 검사합니다. 기존 접근을 중단해야 한다면 이전 subject/권한을 명시적으로 제거합니다. external/static 모드는 내장 상태 파일을 읽지 않습니다. 파일을 유지하면 같은 호환 origin, subject, 자격 증명으로 패스키 로그인에 돌아올 때 등록된 패스키를 다시 사용할 수 있습니다. upstream을 바꾸기 위해 파일을 삭제하지 않습니다. 공개 issuer/resource를 바꾸면 호환되는 새 상태와 클라이언트 재연결이 필요합니다.

모르는 YAML 키는 `config.Load`에서 실패합니다. 추가 HTTP, 인증, provider 검사는 시작 중 수행합니다. YAML 로드 성공만으로 배포가 정상 동작한다는 뜻은 아닙니다. [설정](configuration.md), [사람의 승인](approval.md), [배포](deploy.md)를 참고합니다.
