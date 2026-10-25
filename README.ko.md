# opsgate

[English](README.md) · [클라이언트 설정](docs/clients.md) · [보안](SECURITY.md)

opsgate는 AI 클라이언트가 Linux와 Kubernetes를 조회하고 제한된 운영 작업을 요청하도록 돕는 오픈 소스 Go MCP 서버입니다. 최소 권한, 명시적 허용 목록, 기본 거부를 사용합니다. 제공자는 기본 비활성화이고 허용 목록은 비어 있으며 쓰기에는 정책과 사람의 승인이 모두 필요합니다. 일반 OS 계정과 좁은 Kubernetes RBAC로 실행하세요. 셸이나 임의 명령 도구는 없으며 내부 명령은 고정 argv와 exec.CommandContext를 사용합니다. 설정과 자격 증명은 신뢰하는 소유자가 관리해야 합니다.

stdio 및 Streamable HTTP, Protected Resource Metadata, GitHub/OIDC 로그인 기반 builtin OAuth 2.1, 외부 JWT 및 static 토큰 인증, 조회와 네 가지 쓰기 도구, 마스킹과 JSONL 감사를 지원합니다. GoReleaser, cosign keyless 서명, SBOM, provenance 증명, 컨테이너 이미지 및 강화된 systemd/Kubernetes 템플릿도 제공합니다. [배포 안내](docs/deploy.md)를 참고하세요.

**개발 상태:** 1–4단계가 구현된 사전 릴리스이며 제3자의 보안 검토는 아직 받지 않았습니다. 로그와 이벤트는 프롬프트 인젝션을 포함할 수 있는 신뢰할 수 없는 데이터입니다. 마스킹은 최선형 보호입니다.

## stdio 빠른 시작

호스트 조회에는 systemd/journald 및 /proc가 있는 Linux가 필요합니다. Kubernetes는 client-go와 제한된 자격 증명을 사용합니다. stdio는 실행 OS 계정을 신뢰합니다.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest
# 또는 소스에서 빌드(Go 1.26 이상):
make build
```

```yaml
stdio:
  scopes: ["ops:read"]
providers:
  host:
    enabled: true
    units: ["ssh.service", "nginx*"]
```

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

YAML을 `/path/opsgate.yaml`에 저장하고 실행 파일과 설정 경로를 절대 경로로 바꾸세요. 소스 빌드는 `bin/opsgate`를 생성합니다. 설정이 없으면 두 제공자 모두 비활성화됩니다. 전체 설정은 [예제](opsgate.example.yaml), Desktop, Cursor, SSH 연결은 [클라이언트 설정](docs/clients.md)을 참고하세요.

## Google 기반 HTTP 빠른 시작

Google OIDC 웹 애플리케이션을 만들고 정확한 redirect URI **`https://mcp.example.com/oauth/callback`**을 등록하세요. 아래 origin, client ID, 검증된 이메일을 교체하고 비밀 파일과 쓰기 가능한 비공개 상태 디렉터리를 준비하세요.

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

TLS 프록시는 `/mcp`, `/.well-known/*`, `/oauth/*`, `/approve/*`를 전달해야 합니다. claude.ai와 ChatGPT에는 `/mcp` URL의 사용자 지정 OAuth 커넥터를 추가하세요. DCR이 클라이언트를 등록합니다. [클라이언트](docs/clients.md) 및 [배포](docs/deploy.md)를 참고하세요.

## 도구 계약

조회 도구에는 `ops:read`가 필요하며 해당 제공자가 활성화된 경우에만 등록됩니다. 호스트 유닛과 Kubernetes 네임스페이스는 설정으로 제한하며 네임스페이스 거부가 우선합니다. Kubernetes Secrets 도구는 없습니다. 읽기 전용 메타데이터와 로그에도 민감한 정보가 있을 수 있습니다.

| 도구 | 용도 | 주요 인수, 기본값 및 제한 |
| --- | --- | --- |
| `host_overview` | 호스트 지표 및 설정된 디스크 경로 | 없음 |
| `host_services` | 허용된 systemd 유닛 | 없음 |
| `host_service_status` | 허용된 유닛 상태 | `unit` 필수. 접미사 생략 시 `.service` 추가, 정규화된 이름은 최대 256바이트 |
| `host_logs` | 허용된 journald 로그 | `unit` 필수(상태 조회와 동일한 규칙). `lines=100`, 1–`max_log_lines`로 제한(기본 상한 500). `since=""`(시간 필터 없음), `d`를 포함한 양수 기간(최대 128바이트) 또는 RFC3339. `priority=""`(필터 없음), emerg/alert/crit/err/warning/notice/info/debug 또는 0–7. `grep=""`, 대소문자 구분 없는 리터럴 부분 문자열, 최대 4096바이트 |
| `host_top_processes` | /proc 프로세스 사용량을 500ms 동안 측정. 이름만 표시하며 인수와 환경은 제외 | `sort="cpu"`(cpu 또는 mem). `limit=15`, 1–50으로 제한 |
| `k8s_overview` | 노드 상태 및 허용된 네임스페이스의 워크로드/이벤트 | 없음 |
| `k8s_list` | 지원 리소스 목록 | `kind` 필수. `namespace=""`(허용된 모든 네임스페이스). `label_selector=""`(필터 없음), Kubernetes 레이블 선택자 문법. `limit=100`, 1–500으로 제한 |
| `k8s_get` | 정제된 객체 YAML과 최근 1시간의 관련 이벤트 최대 50개 | `kind`, `name` 필수. 네임스페이스 범위 종류는 `namespace` 필수, 클러스터 범위 종류는 생략 |
| `k8s_logs` | 파드 로그. `allow_logs=true`일 때만 등록 | `namespace`, `pod` 필수. 일반/init/ephemeral 컨테이너를 합쳐 하나뿐인 경우에만 `container` 생략 가능. `tail=200`, 1–`max_log_lines`로 제한(기본 상한 500). `since=""`(시간 필터 없음), 최대 168h의 양수 Go 기간. `previous=false`. `grep=""`, 대소문자 구분 없는 리터럴 부분 문자열. 스트림은 1 MiB로 제한 |
| `k8s_events` | 네임스페이스 이벤트 | `namespace=""`(허용된 모든 네임스페이스). `warnings_only=true`. `since="1h"`, 최대 168h의 양수 Go 기간. `limit=50`, 1–500으로 제한 |
| `host_service_restart` | 허용된 systemd 유닛 재시작 | `unit` 필수(상태 조회와 같은 정규화, 256바이트 제한); `restart_units` 필요 |
| `k8s_rollout_restart` | 파드 템플릿 갱신으로 워크로드 재시작 | `kind`(deployment/statefulset/daemonset), `namespace`, `name` 필수; `rollout_restart` 필요 |
| `k8s_scale` | 복제본 수 지정 | `kind`(deployment/statefulset), `namespace`, `name`, `replicas`(0–`max_replicas`, 기본 상한 10) 필수; `scale` 필요 |
| `k8s_delete_pod` | UID 전제조건으로 특정 파드 삭제 | `namespace`, `name` 필수; `delete_pod` 필요 |

조회 도구의 숫자 제한에서 0은 기본값을 선택하며 그 외 범위를 벗어난 값은 상하한으로 조정됩니다. 쓰기 복제본 수는 조정하지 않고 범위를 검증합니다. 모든 반환 텍스트에는 `server.max_output_bytes`(기본 65536)가 적용되며 호출에는 협력적 제한 시간(`server.tool_timeout`, 기본 30s)이 적용됩니다.

Kubernetes 네임스페이스 입력은 DNS-1123 레이블이어야 합니다. 객체 `name`, `pod`, `container` 입력은 DNS-1123 서브도메인으로 검증합니다.

`k8s_list`와 `k8s_get`의 지원 `kind` 값은 대소문자를 구분하지 않습니다(괄호 안은 별칭): pods (po, pod), deployments (deploy, deployment), statefulsets (sts, statefulset), daemonsets (ds, daemonset), replicasets (rs, replicaset), jobs (job), cronjobs (cronjob), services (svc, service), ingresses (ing, ingress), configmaps (cm, configmap), persistentvolumeclaims (pvc, persistentvolumeclaim), persistentvolumes (pv, persistentvolume), nodes (no, node), namespaces (ns, namespace).

Nodes, PersistentVolumes, Namespaces는 클러스터 범위이므로 `namespace`를 생략하세요. 네임스페이스 허용/거부 규칙은 Namespace 객체의 이름을 필터링하지만 Nodes와 PersistentVolumes는 제한하지 않습니다. `k8s_overview`에 표시되는 노드에도 동일하게 적용됩니다. 네임스페이스 범위 리소스와 이벤트는 네임스페이스로 필터링하며 빈 허용 목록은 모두 차단합니다. 클러스터 범위 접근은 RBAC로 제한하세요.

[읽기 전용 RBAC](deploy/kubernetes/rbac-readonly.yaml)를 출발점으로 사용하고 배포 전에 유효 권한을 검토하세요. 기존 `opsgate` 네임스페이스에 ServiceAccount를 만들고 ClusterRole과 ClusterRoleBinding으로 지원 리소스, 이벤트, 파드 로그에 클러스터 전체 get/list/watch 권한을 부여하며 Secrets는 제외합니다. 더 좁은 접근 범위에는 네임스페이스 RoleBinding을 사용하세요.

Kubeconfig의 `exec` 및 `auth-provider` 자격 증명 플러그인은 거부됩니다. 수명이 짧은 ServiceAccount 토큰처럼 플러그인이 필요 없는 자격 증명을 사용하세요. kubeconfig 소유권과 권한을 보호하세요.

## 마스킹 및 객체 정제

공통 `internal/redact` 계층은 PEM 개인 키, Authorization/Proxy-Authorization 헤더, Basic/Bearer 자격 증명, JWT 형태 토큰, URL 비밀번호와 알려진 토큰 패턴을 마스킹합니다. 토큰 패턴에는 AWS 액세스 키 ID, GitHub, GitLab, Slack, Stripe 라이브 키, OpenAI/Anthropic 형태 키, Google API 키, Telegram 봇이 포함됩니다. 민감한 `:`/`=` 할당과 구조화된 인수 키에는 password/passwd/pwd, secret/client_secret, token/access_token/refresh_token, api_key/apikey/access_key/secret_key/private_key, credential/credentials, auth_token, session_id, cookie, set-cookie와 `db_password` 같은 밑줄 접두사 변형이 포함됩니다(키는 대소문자를 구분하지 않음). 구조화된 인수 맵과 슬라이스는 재귀적으로 순회합니다. 성공 텍스트, 오류, 감사 인수에 마스킹이 적용됩니다. 이는 **최선형** 보호입니다. 알 수 없는 형식이나 임의 데이터에 포함된 비밀정보는 누출될 수 있습니다.

`k8s_get`은 추가로 템플릿을 포함한 메타데이터에서 `managedFields`와 `kubectl.kubernetes.io/last-applied-configuration`을 제거합니다. ConfigMap `data`와 `binaryData`는 `show_configmap_data=true`가 아니면 키를 유지하고 값을 바이트 수 표시로 바꿉니다. 환경 변수 이름에 pass, secret, token, key, credential, auth, dsn, private, cert, database_url이 포함되면(대소문자 무시) 리터럴 환경 `value` 필드를 `[REDACTED]`로 바꿉니다. Pods와 CronJobs를 포함한 지원 워크로드 템플릿의 일반, init, ephemeral 컨테이너에 적용됩니다. 다른 리터럴 값은 공통 마스킹 패턴에 해당하지 않으면 표시됩니다. `valueFrom` 참조는 표시되며 실제 값을 조회하지 않습니다.

## 인증 및 사람의 승인

`builtin`은 GitHub 또는 Google 등의 OIDC로 로그인한 뒤 불투명 MCP 토큰을 발급합니다. PKCE S256 전용 authorization code, 동적 클라이언트 등록(DCR), 클라이언트별 동의 화면, refresh 회전 및 재사용 시 폐기를 지원합니다. issuer는 `http.public_url`, resource는 `<public_url>/mcp`입니다. 선택적 상태 파일은 등록 정보와 토큰 해시를 재시작 후에도 유지합니다. 디렉터리와 백업을 보호하고 파일당 프로세스 하나만 사용하세요.

Builtin OIDC 사용자는 검증된 `email:<주소>` 또는 설정한 issuer의 변경되지 않는 `oidc:<sub>`로 지정합니다. `oidc:<sub>`는 대소문자를 구분하며, 둘 다 일치하면 우선하고 이메일 claim은 필요하지 않습니다. Google 이메일은 `gmail.com`/`googlemail.com`이거나 이메일 도메인과 `hd` claim이 일치하는 Google Workspace 계정이어야 합니다. 다른 Google 주소에는 `email_verified`만으로 충분하지 않습니다. 위 Google 예시에는 Workspace 주소 또는 `oidc:<sub>`를 사용하세요.

OAuth GET 경로는 요청 본문을 거부하고 POST 본문 읽기는 10초로 제한합니다. 동의 전 인증 오류는 로컬 페이지에 표시하며 동의 화면의 Deny는 리디렉션할 수 있습니다. DCR 상한에서는 토큰 발급 이력과 진행 중 인증이 없는 10분 초과 등록을 오래된 순서로 제거합니다. 대기 중 인증은 클라이언트당 20개, 전체 1000개로 제한하고 해당 풀의 가장 오래된 항목을 제거합니다. 브라우저 승인 로그인은 별도 100개 풀을 사용합니다.

`external`은 issuer discovery 및 JWKS로 JWT 서명, issuer, audience, 만료, 허용 알고리즘을 검증합니다. issuer, discovery 및 JWKS URL은 루프백 HTTP를 제외하고 HTTPS가 필요합니다. audience 기본값은 MCP resource URL입니다. 범위는 `scope` 또는 `scp`에서 읽으며 선택적 `claim:<value>` 사용자 목록을 설정하면 신원을 제한하고 토큰 범위와 허용 범위의 교집합을 사용합니다. `static`은 SHA-256 해시로 설정한 공유 Bearer 토큰을 사용합니다. `opsgate -gen-token`으로 토큰과 해시를 생성하고 원본은 클라이언트 비밀 저장소에만 보관하세요. static 모드에는 OAuth 로그인이나 DCR이 없습니다.

조회 도구에는 `ops:read`가 필요합니다. 쓰기는 별도 작업 허용 목록, `ops:write`, OS/RBAC 권한 및 사람의 승인이 모두 필요합니다. HTTP 진입에는 `ops:read`도 필요하므로 원격 쓰기 사용자에게 두 범위를 부여하세요. Kubernetes 네임스페이스 허용/거부 규칙은 쓰기에도 적용됩니다. 조회 유닛 목록은 재시작 권한을 부여하지 않습니다.

서버는 입력을 검증하고 부작용 없는 계획을 만든 뒤 확인을 요청합니다. MCP elicitation 지원 클라이언트에는 앱 내 승인 요청을 표시하며 프로토콜 버전에 걸친 여러 차례의 입력 요청을 지원합니다. 미지원 시 builtin 인증과 `approval.web: true`가 있으면 브라우저 승인 링크와 `approval_id`를 반환합니다. 사람은 링크를 열어 동일한 사용자로 upstream에 로그인하고 작업을 검토합니다. 승인 후 정확히 같은 인수에 `approval_id`를 추가하여 재호출하세요. 승인은 만료되며 일회용이고 사용자, 도구, 마스킹 전 정규 JSON 인수의 SHA-256(`approval_id` 제외) 및 계획에 결합됩니다. 요약이 마스킹으로 바뀌면 대상을 명확히 표시할 수 없어 쓰기를 거부합니다. Kubernetes 계획에는 대상 UID가 포함되며 실행은 같은 호출의 계획과 식별자/버전 사전 조건을 사용합니다. 확인 채널이 없으면 쓰기를 거부합니다. stdio는 elicitation만 지원합니다.

`approval.web: true`와 `approval.require_web: true`를 설정하면 elicitation 지원 여부와 관계없이 브라우저 승인이 필요하며 stdio 쓰기는 거부됩니다. 침해된 클라이언트 UI는 elicitation을 자동 승인할 수 있습니다. 프롬프트 인젝션은 모델이 쓰기를 요청하게 만들 수 있으므로 사람이 요약을 읽어야 합니다. [SECURITY.md](SECURITY.md)를 참고하세요.

## 설정 참조

아래 기본값은 `config.Default()`에서 가져왔습니다. YAML은 기본값 위에 적용되며 알 수 없는 키는 오류입니다. HTTP, 인증 및 승인 설정도 아래에 포함됩니다. 빈 슬라이스는 `[]`로 표시합니다(Go에서는 일부가 nil).

| YAML 키 | 기본값 | 의미 |
| --- | --- | --- |
| `server.name` | `opsgate` | MCP 서버 이름 |
| `server.audit_log` | `""` | 빈 값이면 stderr, 그 외에는 JSONL 파일 경로 |
| `server.max_output_bytes` | `65536` | 도구 출력 바이트 제한, 양수 필수 |
| `server.tool_timeout` | `30s` | 호출별 제한 시간, 양수 필수 |
| `stdio.user` | `local` | 감사 식별자, 신뢰 경계는 실행 OS 사용자 |
| `stdio.scopes` | `[ops:read]` | 허용 범위: ops:read, ops:write. 쓰기 범위만으로 작업이 활성화되지는 않음 |
| `providers.host.enabled` | `false` | 호스트 제공자 활성화 |
| `providers.host.units` | `[]` | 유닛 glob 허용 목록, 빈 목록은 모두 차단 |
| `providers.host.disk_paths` | `[/]` | 개요에 표시할 마운트 지점 |
| `providers.host.max_log_lines` | `500` | 호출별 저널 행 제한 |
| `providers.kubernetes.enabled` | `false` | Kubernetes 제공자 활성화 |
| `providers.kubernetes.kubeconfig` | `""` | 빈 값: 클러스터 내부 설정, $KUBECONFIG, ~/.kube/config 순서 |
| `providers.kubernetes.context` | `""` | 컨텍스트 재정의, 빈 값이면 선택된 컨텍스트 |
| `providers.kubernetes.namespaces` | `[]` | 네임스페이스 glob 허용 목록, 빈 목록은 모두 차단, *는 모두 허용 |
| `providers.kubernetes.deny_namespaces` | `[]` | 거부 glob이 허용 glob보다 우선 |
| `providers.kubernetes.allow_logs` | `false` | 파드 로그 활성화 |
| `providers.kubernetes.show_configmap_data` | `false` | false이면 키만 표시하고 값은 숨김 |
| `providers.kubernetes.max_log_lines` | `500` | 호출별 파드 로그 행 제한 |
| `http.listen` | `127.0.0.1:8787` | 백엔드 TCP 주소 |
| `http.public_url` | `""` | HTTP 사용 시 필수 origin; loopback 외에는 HTTPS, resource는 origin + /mcp |
| `auth.mode` | `""` | HTTP 사용 시 필수: builtin, external 또는 static |
| `auth.builtin.state_file` | `""` | 선택적 0600 상태 파일; 빈 값이면 재시작 시 등록 및 토큰 상태 소실 |
| `auth.builtin.users` | `[]` | 허용 upstream 사용자 목록; 빈 목록은 모두 거부 |
| `auth.builtin.users[].subject` | `""` | github:로그인, email:검증된이메일 또는 oidc:sub (대소문자 구분) |
| `auth.builtin.users[].scopes` | `[]` | ops:read 및 선택적 ops:write 권한 |
| `auth.builtin.upstream.provider` | `""` | github 또는 oidc |
| `auth.builtin.upstream.issuer` | `""` | OIDC issuer 필수, 예: https://accounts.google.com |
| `auth.builtin.upstream.client_id` | `""` | Upstream 애플리케이션 client ID |
| `auth.builtin.upstream.client_secret_file` | `""` | 비밀 파일 경로; client_secret_env와 둘 중 하나 사용 |
| `auth.builtin.upstream.client_secret_env` | `""` | 비밀 값이 들어 있는 환경 변수의 이름 |
| `auth.builtin.access_token_ttl` | `1h` | Access 토큰 수명 |
| `auth.builtin.refresh_token_ttl` | `720h` | Refresh 토큰 수명 |
| `auth.builtin.max_clients` | `100` | 동적 클라이언트 등록 상한 |
| `auth.external.issuer` | `""` | Discovery 및 JWKS에 사용할 신뢰하는 issuer |
| `auth.external.audience` | `""` | 빈 값이면 public_url + /mcp 사용 |
| `auth.external.user_claim` | `sub` | 사용자를 식별하는 문자열 claim |
| `auth.external.users` | `[]` | 빈 목록이면 토큰 범위를 신뢰; 목록이 있으면 사용자 제한 및 범위 교집합 적용 |
| `auth.external.users[].subject` | `""` | 설정한 user_claim의 claim:값 |
| `auth.external.users[].scopes` | `[]` | 해당 신원의 최대 허용 범위 |
| `auth.static` | `[]` | 공유 Bearer 토큰 목록; OAuth 로그인 없음 |
| `auth.static[].name` | `""` | 토큰 신원 및 감사 이름 |
| `auth.static[].sha256` | `""` | 16진수 SHA-256 해시; 원본 토큰 저장 금지 |
| `auth.static[].scopes` | `[]` | 토큰 권한; HTTP 접근에 ops:read 필요 |
| `approval.ttl` | `10m` | 브라우저 승인 대기 수명 |
| `approval.web` | `false` | 브라우저 대체 승인; builtin 인증 필수 |
| `approval.require_web` | `false` | 브라우저 승인 필수; `approval.web` 필요, stdio 쓰기 거부 |
| `providers.host.restart_units` | `[]` | 별도 재시작 유닛 glob 목록; 빈 목록이면 도구 비활성화 |
| `providers.host.command_prefix` | `[]` | 쓰기 argv 접두사, 예: [/usr/bin/sudo, -n]; 첫 항목은 절대 실행 경로, 나머지는 경로/플래그 |
| `providers.kubernetes.rollout_restart` | `[]` | namespace/name glob 목록; 빈 목록이면 도구 비활성화 |
| `providers.kubernetes.scale` | `[]` | namespace/name glob 목록; 빈 목록이면 도구 비활성화 |
| `providers.kubernetes.delete_pod` | `[]` | namespace/name glob 목록; 빈 목록이면 도구 비활성화 |
| `providers.kubernetes.max_replicas` | `10` | 0 이상의 복제본 상한; 0이면 0개로만 조정 가능 |

로그 상한은 양수로 설정하세요. 쓰기의 원본 인수는 마스킹 및 인수 감사 기록 전에 16 KiB로 제한합니다. 네 가지 쓰기 도구는 브라우저 재호출용 선택 인수 `approval_id`를 받습니다. 작업 허용 목록이 비어 있으면 해당 도구는 등록되지 않습니다. 설정 변경 후 재시작해야 합니다.

## 감사 형식

공통 래퍼는 처리한 도구 호출마다 JSON 객체 한 줄을 `server.audit_log`에 추가합니다. 빈 경로이면 stderr로 보냅니다. 새 파일은 0600 권한으로 생성되며(umask 적용) 기존 파일 권한은 유지됩니다. 심볼릭 링크, 일반 파일이 아닌 파일, 그룹/전체 쓰기 권한이 있는 파일은 거부합니다. stdout은 MCP 전용입니다.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"host_service_status","args":{"unit":"ssh.service"},"outcome":"ok","duration_ms":12,"output_bytes":120}
```

`audit.Event`의 필드: `time`(선택적 소수 초를 포함하는 RFC3339 타임스탬프), `user`, `transport`, `tool`, 선택적 마스킹된 `args`, `outcome`(`ok`, `error`, `denied`, `pending`), 선택적 마스킹된 `error`, `duration_ms`, `output_bytes`(성공 반환 텍스트 크기, 오류 시 0). 쓰기 이벤트에는 선택적 마스킹된 `plan`(사람에게 표시한 요약), `approval`(확인 방법인 `elicitation` 또는 `web`; 거부나 대기 호출에도 포함될 수 있음)이 추가됩니다. `pending`은 브라우저 승인 대기이며 반환 안내 텍스트는 `output_bytes`에 포함됩니다. 계획과 실행에는 각각 제한 시간이 적용되며 사람의 확인은 더 오래 걸릴 수 있습니다. 브라우저 결정은 `approved` 또는 `rejected` 결과, 사용자, 도구 및 승인 ID 앞 8자(`args.approval_id_prefix`)만 포함하는 별도 감사 이벤트로 기록합니다. 거부된 다중 왕복 승인 검증도 요청 상태 없이 `denied`로 기록합니다. 도구 출력 본문은 저장하지 않습니다. 기록 실패는 도구 호출을 실패시키지 않고 stderr로 보고하며 감사는 위변조 방지를 보장하지 않습니다. 민감한 정보가 남을 수 있으므로 로그를 보호하고 순환 관리하세요.

## 개발 및 라이선스

[기여 안내](CONTRIBUTING.md)를 참고하세요. [Apache-2.0](LICENSE), copyright 2026 The opsgate Authors.
