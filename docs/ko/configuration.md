# 설정 레퍼런스

[English](../configuration.md)

아래 기본값은 `config.Default()`에서 가져오며, `mode`를 정하면 일부가 바뀝니다([모드](#모드와-opsgate-init) 참고). YAML을 읽을 때 기본값에 설정을 덮어쓰며, 정의된 필드만 허용하므로 알 수 없는 키가 있으면 실패합니다. HTTP, 인증, 승인 설정도 함께 설명합니다. 빈 슬라이스는 `[]`로 표시하며, Go에서는 일부가 nil입니다.

## 모드와 `opsgate init`

`mode`는 opsgate를 어디서 서비스로 실행하는지 정하고, 그에 맞는 기본값을 채웁니다.
파일에 직접 적은 값이 항상 우선합니다. 모드가 없으면 아래 표의 기본값을 쓰고,
전송 방식의 기본값은 stdio입니다.

| 키 | `mode: kubernetes` (파드) |
| --- | --- |
| `-transport` 기본값 | `http` |
| `http.listen` | `0.0.0.0:8787` |
| `auth.mode` | `builtin` |
| `auth.builtin.upstream.provider` | `passkey` |
| `auth.builtin.state_file` | `/var/lib/opsgate/oauth.json` |
| `approval.web` | `auth.mode`가 builtin이면 `true` |
| `server.audit_log` | `""` (stderr. 클러스터 로그 수집용) |
| `providers.kubernetes.enabled` | `true`. 파드의 ServiceAccount를 씁니다(`kubeconfig: ""`) |

네임스페이스, 사용자, 쓰기 대상은 모드가 채우지 않습니다.
`opsgate init`은 네임스페이스, 사용자, 공개 URL, 로그인 방식(패스키, Google, GitHub)을
묻고, `mode: kubernetes`인 짧은 파일을 권한 0600으로 만듭니다. 쓰기 도구는 설정에
대상을 나열하기 전까지 꺼져 있습니다. 사용자에게 `ops:write`를
부여할지 묻습니다. 이미 있는 파일은 `-force` 없이
덮어쓰지 않습니다. 모든 질문에 대응하는 플래그가 있습니다(`opsgate init -h`).
터미널이 없거나 `-yes`를 주면 답하지 않은 질문은 기본값을 쓰고, 공개 URL과 사용자는
플래그로 줘야 합니다. 파일을 쓰기 전에 HTTP 서버와 같은 방식으로 검증합니다.
Kubernetes 도구를 활성화하면 `opsgate init`의 기본 네임스페이스 거부 목록은
`[kube-system, kube-public, kube-node-lease, opsgate]`이며, `-deny-namespaces`로 바꿀 수 있습니다.

`opsgate config show -config <파일>`은 모드 기본값까지 포함한 최종 설정을 opsgate가
그대로 읽을 수 있는 YAML로 출력합니다.

웹 승인이 켜져 있으면 출력에 `require_web: true`가 명시됩니다. 그 출력을 `-transport stdio`로 실행하면 쓰기를 거부합니다. 클라이언트 내 확인을 허용하려면 키를 지우거나 `false`로 바꾸세요.

| YAML 키 | 기본값 | 설명 |
| --- | --- | --- |
| `mode` | `""` | 비어 있거나 `kubernetes`입니다. 위 설명을 참고하세요 |
| `server.name` | `opsgate` | MCP 서버 이름입니다 |
| `server.audit_log` | `""` | JSONL 로그 파일 경로입니다. 비어 있으면 stderr에 기록합니다 |
| `server.max_output_bytes` | `65536` | 도구 출력의 바이트 수 제한이며, 양수여야 합니다 |
| `server.tool_timeout` | `30s` | 도구가 취소 신호를 따를 때 적용되는 호출 제한 시간입니다. 쓰기는 계획(Plan)과 실행(Apply)에 각각 따로 적용하며, 양수여야 합니다 |
| `stdio.user` | `local` | 감사 로그의 사용자 식별 레이블이며, 신뢰 경계는 OS 사용자입니다 |
| `stdio.scopes` | `[ops:read]` | ops:read, ops:write, dev:run을 허용합니다. 쓰기 스코프만으로 작업을 활성화하지는 않습니다 |
| `providers.kubernetes.enabled` | `false` | Kubernetes provider를 활성화합니다 |
| `providers.kubernetes.clusters` | 생략 | 여러 클러스터의 연결·정책 목록입니다. 단일 클러스터 필드와 함께 사용할 수 없습니다 |
| `providers.kubernetes.name` / `clusters[].name` | `default` / 필수 | 호출의 `cluster` 인자로 선택하는 이름입니다 |
| `providers.kubernetes.protected` / `clusters[].protected` | `false` | 쓰기마다 승인을 새로 받으며 승인 창을 허용하지 않습니다 |
| `providers.kubernetes.kubeconfig` | `""` | 비어 있으면 클러스터 내부 설정, $KUBECONFIG, ~/.kube/config 순으로 사용합니다 |
| `providers.kubernetes.context` | `""` | 사용할 컨텍스트를 지정합니다. 비어 있으면 현재 선택된 컨텍스트를 사용합니다 |
| `providers.kubernetes.namespaces` | `[]` | 네임스페이스 glob 허용 목록입니다. 비어 있으면 어떤 네임스페이스도 허용하지 않으며, *는 모두 허용합니다 |
| `providers.kubernetes.deny_namespaces` | `[]` | 거부 목록의 glob이 허용 목록의 glob보다 우선합니다 |
| `providers.kubernetes.allow_logs` | `false` | 파드 로그를 활성화합니다 |
| `providers.kubernetes.show_configmap_data` | `false` | false이면 키만 보여 주고 값은 숨깁니다 |
| `providers.kubernetes.max_log_lines` | `500` | 호출당 파드 로그의 최대 줄 수입니다 |
| `http.listen` | `127.0.0.1:8787` | 백엔드 TCP 주소입니다 |
| `http.public_url` | `""` | HTTP 사용 시 필수인 origin입니다. 루프백 HTTP를 제외하면 HTTPS여야 하며, 리소스는 origin + /mcp입니다 |
| `auth.mode` | `""` | HTTP 사용 시 필수이며, builtin, external, static 중 하나입니다 |
| `auth.builtin.state_file` | `""` | 클라이언트와 토큰 해시를 저장해 재시작 후에도 유지하는 파일이며, 권한은 0600입니다. 선택 사항으로, 비어 있으면 재시작 시 상태가 사라집니다 |
| `auth.builtin.users` | `[]` | 허용할 업스트림 사용자 목록이며, 비어 있으면 모두 거부합니다 |
| `auth.builtin.users[].subject` | `""` | passkey:name, github:login, email:verified-email 또는 oidc:sub(정확히 일치하는 OIDC subject)입니다 |
| `auth.builtin.users[].scopes` | `[]` | 사용자에게 줄 스코프이며, ops:read, ops:write, dev:run을 사용합니다 |
| `auth.builtin.upstream.provider` | `""` | passkey, github 또는 oidc입니다. passkey는 외부 인증 provider를 사용하지 않으며 클라이언트 ID나 비밀값이 필요하지 않습니다 |
| `auth.builtin.upstream.issuer` | `""` | OIDC 사용 시 필수인 발급자이며, 예로 https://accounts.google.com 등이 있습니다 |
| `auth.builtin.upstream.client_id` | `""` | 업스트림 애플리케이션의 클라이언트 ID입니다 |
| `auth.builtin.upstream.client_secret_file` | `""` | 비밀값을 담은 파일이며, 이 설정이나 client_secret_env 중 하나를 사용합니다 |
| `auth.builtin.upstream.client_secret_env` | `""` | 비밀값을 담은 환경 변수의 이름입니다 |
| `auth.builtin.access_token_ttl` | `1h` | 액세스 토큰의 유효 기간입니다 |
| `auth.builtin.refresh_token_ttl` | `720h` | 리프레시 토큰의 유효 기간입니다 |
| `auth.builtin.max_clients` | `100` | 동적 클라이언트 등록(DCR)의 수용 한도입니다 |
| `auth.external.issuer` | `""` | discovery와 JWKS에 사용할 신뢰할 수 있는 발급자입니다 |
| `auth.external.audience` | `""` | 비어 있으면 public_url + /mcp를 사용합니다 |
| `auth.external.user_claim` | `sub` | 사용자를 식별하는 문자열 클레임입니다 |
| `auth.external.users` | `[]` | 비어 있으면 토큰 스코프를 신뢰합니다. 목록이 있으면 사용자를 제한하고 토큰 스코프와 사용자에게 허용한 스코프의 교집합을 적용합니다 |
| `auth.external.users[].subject` | `""` | 설정한 user_claim 값에 맞춘 claim:value입니다 |
| `auth.external.users[].scopes` | `[]` | 이 사용자에게 허용할 최대 스코프입니다 |
| `auth.static` | `[]` | 사용할 Bearer 토큰 목록이며, 이 모드에는 OAuth 로그인이 없습니다 |
| `auth.static[].name` | `""` | 토큰의 사용자 식별 및 감사 로그 레이블입니다 |
| `auth.static[].sha256` | `""` | 원본 토큰 대신 사용하는 16진수 SHA-256 다이제스트입니다 |
| `auth.static[].scopes` | `[]` | 토큰에 줄 스코프이며, HTTP 접근에는 ops:read가 필요합니다 |
| `approval.ttl` | `10m` | 대기 중인 승인/입력의 유효 기간입니다 |
| `approval.web` | `false` | 브라우저 승인을 활성화하며 builtin 인증이 필요합니다. 모드에서는 기본으로 켭니다 |
| `approval.require_web` | `approval.web`이 true이면 `true`, 아니면 `false` | HTTP에서 웹 승인을 필수로 합니다. 명시적 `false`는 클라이언트 내 확인을 허용하고, 명시적 `true`는 stdio 쓰기도 거부합니다 |
| `approval.window` | `0s` | 사용자/도구/대상별 자동 승인 시간 창의 유효 기간입니다. 선택 사항이며, 활성화하면 1m–12h 범위로 설정합니다 |
| `approval.window_tools` | `[]` | 승인 시간 창을 허용할 도구 이름의 `path.Match` glob이며, 비어 있으면 어떤 도구도 허용하지 않습니다 |
| `providers.kubernetes.rollout_restart` | `[]` | namespace/name glob이며, 비어 있으면 도구를 비활성화합니다 |
| `providers.kubernetes.scale` | `[]` | namespace/name glob이며, 비어 있으면 도구를 비활성화합니다 |
| `providers.kubernetes.delete_pod` | `[]` | namespace/name glob이며, 비어 있으면 도구를 비활성화합니다 |
| `providers.kubernetes.max_replicas` | `10` | 스케일 조정의 최댓값입니다. 음수는 허용하지 않으며, 0이면 복제본 수 0만 허용합니다 |
| `providers.note.enabled` | `false` | Markdown 폴더나 Obsidian 볼트용 note provider를 활성화하며, 노트 앱이나 CLI는 필요하지 않습니다 |
| `providers.note.vault` | `""` | 이미 있는 볼트 디렉터리의 절대 경로이며, 모든 접근 범위는 os.Root로 제한합니다 |
| `providers.note.exclude` | `[]` | 볼트 기준 상대 경로나 그 상위 경로에 일치하면 제외하는 추가 path.Match glob입니다. 점으로 시작하는 경로 구성 요소와 동기화 충돌 복사본은 항상 제외합니다 |
| `providers.note.allow_write` | `false` | 노트 쓰기 도구 4개를 등록하며, 각 도구에는 ops:write와 사람의 승인이 필요합니다 |
| `providers.note.max_note_bytes` | `1048576` | 노트별 읽기/쓰기 크기 제한이며, 양수여야 합니다. 내용 검색 시 제한을 넘는 노트는 건너뜁니다 |
| `providers.dev.enabled` | `false` | dev provider를 활성화하며, 모든 도구에 dev:run이 필요합니다 |
| `providers.dev.runner_socket` | `""` | 별도 dev-runner의 Unix 소켓 절대 경로입니다 |
| `server.tools.allow` / `server.tools.deny` | `[]` / `[]` | 도구 이름의 `path.Match` glob입니다. allow가 비어 있으면 제공하는 모든 도구를 허용하고 deny가 우선하며, 제외한 도구는 tools/list에 표시하지 않습니다 |
| `auth.builtin.users[].tools` | `[]` | 사용자가 호출할 수 있는 도구를 제한하는 도구 이름 glob이며, 비어 있으면 추가 제한이 없습니다 |
| `auth.external.users[].tools` | `[]` | 매핑된 사용자가 호출할 수 있는 도구를 제한하는 도구 이름 glob이며, 비어 있으면 추가 제한이 없습니다 |
| `auth.static[].tools` | `[]` | 이 토큰으로 호출할 수 있는 도구를 제한하는 도구 이름 glob이며, 비어 있으면 추가 제한이 없습니다 |
| `auth.builtin.allowed_redirect_hosts` | `[]` | 포트를 제외하고 정확히 일치해야 하는 리다이렉트 호스트 이름입니다. localhost는 루프백 IP와 모든 포트를 포함하며, 지원하는 사설 스킴은 cursor:처럼 지정합니다. 비어 있으면 다른 유효성 조건을 충족하는 모든 리다이렉트 호스트를 허용합니다 |
| `auth.builtin.unknown_redirect_hosts` | `""` | 호스트 목록이 있을 때 빈 값/deny는 목록에 없는 목적지를 거부하고, approve는 관리자 검토까지 클라이언트를 대기 상태로 둡니다 |
| `auth.builtin.admins` | `[]` | /admin/clients에서 클라이언트를 관리할 수 있는 업스트림 subject입니다. users에 포함되어야 하며 approve 모드에서는 필수입니다 |
| `auth.builtin.notify_url` | `""` | 대기 중인 등록의 JSON POST 알림을 받을 주소이며, 선택 사항입니다. HTTPS 또는 루프백 HTTP여야 하며, URL에 자격 증명이나 프래그먼트를 넣을 수 없습니다 |

서버 정책의 패턴은 활성화된 provider가 작업별 활성화 조건을 적용한 뒤 제공하는 도구와 일치해야 합니다. 일치하는 도구가 없으면 시작에 실패합니다. 사용자별/토큰별 도구 패턴은 구문을 검사한 뒤 호출할 때 적용하며, tools/list는 필터링하지 않습니다. 스코프, 서버 정책, 사용자별 도구 정책, provider 리소스 허용 목록을 모두 적용합니다. 도구 허용 목록이 비어 있으면 추가 제한을 두지 않으므로, 비어 있는 provider 리소스 허용 목록과는 의미가 다릅니다. 설정을 바꾸면 재시작하세요.

모든 provider는 기본적으로 꺼져 있습니다. Kubernetes를 활성화하면 RBAC이 허용하는 범위에서 클러스터 범위 리소스를 읽을 수 있습니다. `ops:read`는 선택 사항인 실험 단계의 note provider를 포함해 활성화된 모든 읽기 provider에 적용됩니다. `dev:run`은 dev 도구에 적용되며, HTTP에서는 `ops:read`도 필요합니다. [신뢰 모델](security.md)을 참고하세요.

노트 쓰기는 서버 계정으로 실행하므로, 생성하거나 다시 쓴 노트는 서버 계정이 소유합니다. 소유권을 넘기는 대신 ACL로 서버 계정에 접근 권한을 주고, 볼트 소유자가 이 파일을 교체할 수 있도록 파일이 있는 디렉터리의 쓰기 권한을 유지하세요. 볼트 소유자로 실행하는 동기화 도구는 자신이 소유하지 않은 파일에 chmod를 적용할 수 없습니다. Syncthing에서는 볼트 폴더의 Ignore Permissions 옵션을 설정하세요. 그렇지 않으면 다른 기기에서 권한만 바꾼 변경 사항을 반영할 때 "operation not permitted" 오류가 발생합니다.

읽기 전용으로 시작하는 설정과 주석으로 구분한 쓰기 활성화 설정은 [전체 예제](../../opsgate.example.yaml)를 참고하세요. 인자, 승인, 출력 제한은 [도구 레퍼런스](tools.md)에서 설명합니다.

## Kubernetes 클러스터

기존 단일 클러스터 설정도 그대로 사용할 수 있습니다. `providers.kubernetes` 바로 아래에 `kubeconfig`, `context`, 정책 필드를 지정합니다. `name`의 기본값은 `default`이고, `protected`의 기본값은 `false`입니다. 여러 클러스터를 연결하려면 대신 `clusters`를 사용합니다. 각 항목에는 같은 연결·정책 필드를 지정하며, `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`에 맞는 고유한 `name`이 필요합니다(1–63자). 숫자 기본값을 제외한 단일 클러스터 필드와 `clusters`를 함께 지정하면 오류가 발생합니다. provider가 활성화된 상태에서 `clusters: []`도 허용하지 않습니다. `clusters` 항목에서 `max_log_lines`나 `max_replicas`를 생략하거나 0으로 두면 각각 500과 10을 사용합니다. 클러스터에서 스케일 조정을 막으려면 `scale`을 비워 두세요.

```yaml
providers:
  kubernetes:
    enabled: true
    clusters:
      - name: kind
        kubeconfig: /etc/opsgate/kind.yaml
        context: kind-kind
        namespaces: [apps]
        allow_logs: true
        rollout_restart: [apps/web]
        scale: [apps/web]
        max_replicas: 3
      - name: prod
        kubeconfig: /etc/opsgate/prod.yaml
        protected: true
        namespaces: [apps]
        deny_namespaces: [kube-system]
        allow_logs: true
        # 쓰기 허용 목록을 비워 두어 운영 클러스터는 읽기만 허용합니다.
```

`protected: true`이면 승인 창이 있어도 쓰기마다 사람의 승인을 새로 받아야 합니다. 정책은 해당 클러스터에만 적용합니다. 로그나 쓰기 도구를 하나 이상의 클러스터에서 활성화하면 도구를 등록하지만, 해당 기능을 꺼 둔 클러스터로 호출하면 거부합니다. 클러스터가 여러 개이면 `k8s_clusters`를 제외한 모든 호출에 `cluster`를 지정해야 합니다. 하나일 때는 생략할 수 있으며, 이름을 지정하면 설정과 일치해야 합니다. `k8s_clusters`로 정책과 연결 상태를 확인한 뒤 대상을 선택하고, 쓰기 전에 사용자에게 대상 클러스터를 명시합니다.

### 관리형 클러스터(EKS, GKE, AKS)

opsgate는 kubeconfig의 자격 증명 플러그인을 허용하지 않습니다. `aws eks get-token` 같은 `exec`와 `auth-provider` 설정은 kubeconfig를 통해 프로그램을 실행할 수 있기 때문입니다. 전용 ServiceAccount를 만들고, 토큰을 사용하는 별도의 kubeconfig를 준비합니다. 대상 클러스터를 가리키는 관리자 컨텍스트에서 기존 [읽기 전용 RBAC](../../deploy/kubernetes/rbac-readonly.yaml)을 적용합니다. 이 설정은 시크릿을 제외한 클러스터 전체 읽기 권한을 부여합니다. 범위를 줄이려면 네임스페이스별 RoleBinding을 사용합니다.

```sh
kubectl create namespace opsgate
kubectl apply -f deploy/kubernetes/rbac-readonly.yaml
umask 077
kubectl --kubeconfig=opsgate.kubeconfig config set-cluster managed \
  --server=https://API_ENDPOINT --certificate-authority=cluster-ca.crt --embed-certs=true
kubectl --kubeconfig=opsgate.kubeconfig config set-credentials opsgate \
  --token="$(kubectl -n opsgate create token opsgate --duration=24h)"
kubectl --kubeconfig=opsgate.kubeconfig config set-context managed \
  --cluster=managed --user=opsgate --namespace=opsgate
kubectl --kubeconfig=opsgate.kubeconfig config use-context managed
```

`API_ENDPOINT`와 `cluster-ca.crt`에는 관리자 kubeconfig에서 확인한 API 주소와 CA 인증서 파일을 지정합니다. 네임스페이스가 이미 있으면 생성 명령은 생략합니다. 새 kubeconfig의 권한은 0600으로 제한하고 opsgate에서 이 파일을 사용하도록 설정합니다. API 서버가 요청한 토큰 유효 기간을 줄일 수 있으므로 실제 만료 전에 토큰을 교체하고 opsgate를 재시작해야 합니다. 이 kubeconfig에는 자격 증명 플러그인이 없으므로 실행 중에는 클라우드 CLI가 필요하지 않습니다.

## 인증과 사람의 승인

업스트림 애플리케이션의 콜백 주소는 정확히 `<public_url>/oauth/callback`입니다. Host를 보존하는 프록시와 로컬 상태 확인 요청의 규칙은 [배포](deploy.md)를 참고하세요.

`builtin`은 패스키로 로그인하거나 GitHub 또는 OIDC(Google 포함)에 로그인을 위임한 뒤 불투명 MCP 토큰을 발급합니다. 패스키 등록에는 일회용 초대가 필요하며, 시작할 때 패스키가 없는 사용자마다 초대 하나를 로그에 기록합니다(24h). `opsgate passkey invite -config <file> -subject <user> [-ttl 24h]`로 초대를 추가 발급할 수 있습니다. 초대는 상태 파일 옆에 파일로 저장하므로 이 명령은 서버 계정으로 실행하세요.

PKCE S256을 사용하는 authorization code 방식만 지원합니다. 동적 클라이언트 등록(DCR), 클라이언트별 동의, 리프레시 토큰 교체, 재사용 시 폐기도 지원합니다. 발급자는 `http.public_url`이며 리소스는 `<public_url>/mcp`입니다. 상태 저장은 선택 사항이며, 사용하면 재시작 후에도 클라이언트 등록과 토큰 해시를 유지합니다. 저장 디렉터리와 백업을 보호하고, 상태 파일 하나당 프로세스 하나만 사용하세요. 상태 읽기와 쓰기는 512 MiB로 제한합니다. 쓰기가 제한을 초과하면 이전 스냅샷을 보존하고, 실행 중인 인스턴스는 인증을 거부합니다.

Builtin OIDC 사용자는 검증된 `email:<address>` 또는 설정된 발급자의 `oidc:<sub>`로 식별할 수 있습니다. `oidc:<sub>`는 대소문자를 구분해 정확히 일치해야 하며, 변경되지 않는 식별자입니다. 둘 다 일치하면 `oidc:<sub>`가 우선하며 이메일 클레임은 필요하지 않습니다. Google 이메일 subject는 도메인이 `gmail.com`/`googlemail.com`이거나 이메일 도메인과 일치하는 `hd` 클레임(Google Workspace)이 있어야 합니다. 다른 Google 주소에서는 `email_verified`만으로 충분하지 않습니다. README의 Google 예제에서는 Workspace 주소를 사용하거나 `oidc:<sub>`를 설정하세요.

OAuth GET 경로는 요청 본문을 거부하며, POST 본문 읽기에는 10초 제한 시간을 적용합니다. 동의 전 인가 오류는 로컬 페이지에 표시하고, 동의 화면의 Deny는 리다이렉트할 수 있습니다. DCR 수용 한도에 도달하면 생성 후 10분이 넘었고 토큰을 발급한 적이 없으며 진행 중인 인가도 없는 등록을 오래된 순서대로 제거합니다. 대기 중인 인가는 클라이언트당 20개, 전체 1000개로 제한하며, 한도에 도달한 범위에서 가장 오래된 항목을 제거합니다. 웹 승인 및 관리자 로그인은 별도로 최대 256개까지 수용하며, 한 번에 20개와 이후 3초당 1개의 새 로그인을 허용하고, 만료된 로그인 항목만 제거합니다.

`external`은 발급자 discovery와 JWKS로 JWT 서명을 검증하고 발급자, audience, 만료 시간, 허용 알고리즘을 검사합니다. 발급자, discovery, JWKS URL에는 HTTPS가 필요하며 루프백 HTTP는 예외입니다. JWKS 가져오기 시도는 모든 요청이 공유하며, 실패한 시도까지 포함해 1분에 한 번으로 제한합니다. 캐시에 있는 유효한 키는 계속 사용할 수 있지만, 새 서명 키는 대기 시간이 끝날 때까지 거부할 수 있습니다. audience의 기본값은 MCP 리소스 URL입니다. 스코프는 `scope` 또는 `scp`에서 가져옵니다. `claim:<value>` 사용자 허용 목록을 설정하면 사용자를 추가로 제한하고 스코프의 교집합을 적용합니다.

`static`은 SHA-256 다이제스트로 설정한 사전 공유 Bearer 토큰을 허용합니다. `opsgate -gen-token`으로 토큰과 다이제스트를 생성하고, 원본 토큰은 클라이언트의 비밀값 저장소에만 보관하세요. static 모드는 DCR이나 OAuth 로그인을 제공하지 않습니다.

Kubernetes, 노트 읽기 도구에는 `ops:read`가 필요합니다. Kubernetes 쓰기에는 별도의 작업 허용 목록이, 노트 쓰기에는 `allow_write`가 필요합니다. 이 쓰기 작업에는 `ops:write`, 실제 OS/RBAC 권한, 사람의 승인이 필요합니다. dev 도구에는 대신 `dev:run`이 필요하며, `dev_task_start`에는 사람의 승인도 필요합니다. HTTP 접근에는 `ops:read`도 필요하므로 원격 쓰기 사용자에게는 두 스코프가 모두 필요합니다. 네임스페이스 허용/거부 규칙은 Kubernetes 쓰기에도 적용합니다.

서버는 승인 전에 입력을 검증하고 부수 효과가 없는 계획(Plan)을 만듭니다. builtin 인증과 `approval.web: true`를 사용하는 HTTP에서는 기본적으로 웹 승인을 요구하고 승인 링크와 `approval_id`를 반환합니다. `approval.require_web: false`를 명시하면 지원하는 클라이언트에서 클라이언트 내 확인(elicitation)을 허용합니다. 프로토콜 버전에 따라 여러 차례 왕복하는 입력 요청도 포함하며, 지원하지 않는 클라이언트는 계속 웹 링크를 사용합니다. 링크를 열고 같은 업스트림 사용자로 로그인한 뒤, 실행할 작업을 정확히 검토하세요. 승인 후에는 같은 도구에 정확히 같은 인자와 `approval_id`를 추가해 다시 호출하세요.

승인은 유효 기간이 있고 한 번만 사용할 수 있으며, 사용자, 도구, 마스킹하지 않은 정규화 인자의 SHA-256(`approval_id` 제외), 계획에 연결됩니다. 사용자가 대상을 명확히 볼 수 있어야 하므로 마스킹으로 요약이 바뀌면 쓰기를 거부합니다. Kubernetes 계획에는 대상 UID를 포함하며, 실행(Apply)에는 같은 호출에서 만든 계획과 식별자/버전 사전 조건을 사용합니다. 확인 채널이 없으면 쓰기를 거부하고, stdio는 클라이언트 내 확인만 지원합니다.

승인 시간 창은 선택 사항이며, 사용하려면 `approval.window`와 `approval.window_tools`가 모두 필요합니다. `approval.window`는 1m–12h 범위로 설정하세요. `0s`이면 기능을 비활성화합니다. `approval.window_tools`는 `path.Match` glob 허용 목록이며 비어 있으면 어떤 도구도 허용하지 않습니다. 예를 들어 `window: 30m`과 `window_tools: [k8s_rollout_restart]`를 설정하면 브라우저에서 **Approve + allow for 30m**를 선택하거나, 클라이언트 내 확인에서 **Also auto-approve k8s_rollout_restart for 30m** 체크박스를 선택할 수 있습니다. 체크박스 선택은 필수가 아닙니다.

이 권한은 정확히 그 사용자, 도구, 대상에 적용되며, 이후 다른 클라이언트에서 호출하거나 다른 인자를 사용해도 적용됩니다. 다른 대상에는 적용되지 않습니다. `dev_task_start`에는 승인 시간 창을 활성화하지 말고, 각 작업을 별도로 검토하세요. 이 기능은 스코프를 주거나 작업 허용 목록을 우회하지 않으며, 입력 검증이나 계획을 생략하지도 않습니다. 권한은 메모리에만 보관하고 최대 100개까지 허용합니다. 사용해도 유효 기간이 늘어나지 않으며, 만료되거나 재시작하면 사라집니다. 호출마다 `approval: window`가 포함된 별도의 감사 로그를 남깁니다. 권한을 만들 때는 승인 방식(`web` 또는 `elicitation`), 기간, 만료 시각을 담은 별도의 `window_granted` 이벤트를 기록합니다. `require_web`을 사용하면 이 권한도 브라우저에서만 받을 수 있으며 stdio 쓰기는 계속 거부합니다.

승인 페이지에는 diff를 강조한 계획, 요청자, 전송 방식, 만료 시각을 보여 주며, 인자는 접힌 상태로 표시합니다. 승인 여부를 결정한 뒤 채팅으로 돌아가 어시스턴트에게 재시도를 요청하세요. 자동 리다이렉트는 없습니다. 승인 요약은 최대 `max(64 KiB, argument limit + 16 KiB)`까지 허용하며, 원시 JSON 인자의 기본 제한은 16 KiB입니다. 노트 내용 도구는 528 KiB(내용/치환 필드당 256 KiB)를 허용합니다. `dev_task_start`는 32 KiB 프롬프트를 담을 수 있도록 48 KiB를 허용합니다. JSON 이스케이프도 전송 크기 제한에 포함됩니다.

`approval.web`이 활성화되면 `approval.require_web`의 기본값은 `true`입니다. builtin 인증을 쓰는 모드가 웹 승인을 켠 경우도 포함합니다. `approval.require_web: false`를 명시하면 클라이언트 내 확인을 허용하며, 침해된 클라이언트가 자기 쓰기를 스스로 승인할 수 있습니다. external/static 인증이나 `approval.web: false`에서는 웹 승인을 사용할 수 없어 기본값이 `false`입니다. `-transport stdio`는 사용자가 로컬에서 시작한 프로세스이고 웹 채널이 없으므로, 모드 파일이라도 `require_web`을 생략하면 클라이언트 내 확인을 허용합니다. 명시적 `true`는 계속 stdio 쓰기를 거부합니다. HTTP 시작 시 클라이언트 내 확인이 허용되어 있으면 경고를 남깁니다. 프롬프트 인젝션은 여전히 모델이 쓰기를 요청하게 만들 수 있으므로, 사람이 승인 전에 요약을 읽어야 합니다. [SECURITY.md](security.md)를 참고하세요.

`allowed_redirect_hosts`로 클라이언트 등록을 제한할 수 있습니다. 목록이 비어 있지 않으면 `unknown_redirect_hosts: deny`는 목록에 없는 호스트를 거부하고, `approve`는 허용 목록의 관리자가 `/admin/clients`에서 승인할 때까지 그 클라이언트를 대기 상태로 둡니다. 대기 중인 클라이언트는 인가를 받을 수 없습니다. `notify_url`은 선택 사항이며, 설정하면 이벤트, 클라이언트 ID/이름, 리다이렉트 호스트, 관리자 URL이 포함된 JSON POST 알림을 보냅니다. 알림에는 제한이 있으며 전달을 보장하지는 않습니다. 클라이언트 이름은 클라이언트가 직접 제공한 값입니다. 등록을 승인해도 사용자 스코프를 주거나 동의를 생략하지는 않습니다. 호스트 목록이 비어 있으면 다른 유효성 조건을 충족하는 모든 리다이렉트 목적지를 허용하며, 목록에 없는 호스트에 대한 정책은 무시합니다.

## Watch 알림

`watch` 섹션은 별도 프로세스인 `opsgate watch`만 읽고, MCP 서버는 무시합니다. `opsgate watch`를 실행하려면 `providers.kubernetes.enabled`를 켜고 `watch.webhook_url`, `watch.outputs`, `watch.email.host` 중 하나 이상을 설정해야 합니다.

| 키 | 기본값 | 의미 |
| --- | --- | --- |
| `webhook_url` | 없음 | https이거나 루프백 주소로 가는 http. 자격 증명과 프래그먼트는 쓸 수 없습니다 |
| `approval_url` | 없음 | opsgate 서버의 공개 https 오리진. 설정하면 재시작 제안에 승인 링크를 붙입니다 |
| `allow_private_http` | `false` | 사설 네트워크(Service 이름, 사설 IP)로 가는 http도 허용합니다. 연결은 사설 주소에 닿아야 합니다 |
| `secret_file` | 없음 | 선택. `X-Opsgate-Signature` HMAC 키(16바이트 이상) |
| `clusters` | 전체 | watch할 클러스터 이름 |
| `problems_only` | `false` | 모든 채널에 대해 경고만 생성합니다 |
| `batch` | `30s` | Webhook 알림을 모아 보내는 간격 (1s–10m) |
| `cooldown` | `15m` | 같은 객체의 같은 경고는 이 시간 동안 다시 보내지 않고 횟수만 셉니다 (0–24h) |
| `max_alerts` | `50` | 채널별 묶음에 담는 최대 알림 수 (1–500) |
| `email.host` | 없음 | SMTP 활성화. 공백, 스킴, 포트, 슬래시 없는 호스트 이름 |
| `email.port` | `587` | 1–65535. 465는 연결 즉시 TLS, 다른 모든 포트는 STARTTLS 필수 |
| `email.username` | 없음 | 선택. `email.password_file`과 함께 설정 |
| `email.password_file` | 없음 | 시작 시 읽는 SMTP 비밀번호. 앞뒤 공백을 제거하며, 읽을 수 없거나 비어 있으면 시작 실패 |
| `email.from` | 없음 | 표시 이름과 CR/LF 없는 발신 주소. 필수 |
| `email.to` | 없음 | 표시 이름과 CR/LF 없는 수신 주소 1–20개 |
| `email.batch` | `5m` | 이메일을 모아 보내는 간격 (1m–1h) |
| `email.problems_only` | `false` | 이메일로는 경고만 전송 |

다른 이메일 항목을 설정하려면 `email.host`가 필요합니다. Webhook과 이메일은 독립적이므로 하나만 쓰거나 둘 다 쓸 수 있습니다. 각 채널은 알림 묶음, 쿨다운 상태, 재시도를 따로 관리하며 `watch.cooldown`과 `watch.max_alerts`를 공통 설정값으로 사용합니다. TLS 1.2 이상과 서버 인증서 검증은 필수입니다. 포트 465에서는 연결 즉시 TLS를 사용하고, 다른 모든 포트에서는 STARTTLS가 필요합니다. 앱 비밀번호처럼 독립적으로 폐기할 수 있는 전용 자격 증명을 사용하세요. 알림은 저장하지 않으며, Webhook과 마찬가지로 재시도 후에도 전송하지 못하면 유실됩니다.

네임스페이스·파드·컨테이너 이름과 민감 정보를 마스킹한 요약이 클러스터 외부의 메일 제공업체를 거칩니다. 최대 발송량은 묶음 간격마다 메시지 한 통으로, `5m`이면 하루 최대 288통, `15m`이면 최대 96통입니다. 특히 다른 서비스와 계정을 공유한다면 계정의 발송 한도를 확인하세요.

알리는 내용과 본문 형식은 [watch 알림](watch.md)을 보세요.

## Dev runner 설정

runner는 별도 YAML 파일을 사용하며, `providers` 아래에 중첩하지 않습니다. runner 설정은 신뢰할 수 있어야 하며, HTTP 계정이 쓸 수 없어야 합니다. [설정과 위험](dev.md), [예제](../../deploy/dev-runner.example.yaml)를 참고하세요.

| Runner YAML 키 | 기본값 | 설명 |
| --- | --- | --- |
| `admins` | `[]` | 모든 작업을 읽고 취소할 수 있는 요청자 subject를 정확히 지정한 목록입니다. 일반 사용자는 자신의 작업에만 접근합니다 |
| `socket` / `socket_group` | Required | Unix 소켓의 절대 경로와 허용할 피어 그룹입니다 |
| `work_dir` | Required | 비공개 상태/worktree 디렉터리의 절대 경로입니다 |
| `projects` | `[]` | 고유한 소문자 `name`, 절대 경로인 `path`, `base_ref`(기본값 `HEAD`)로 구성한 허용 목록입니다 |
| `agents` | `{}` | 에이전트 이름에 고정된 `command` argv와 필요에 따라 `env` KEY=VALUE 쌍을 지정합니다. 실행 파일은 절대 경로로 지정합니다 |
| `timeout` | `1h` | 작업 제한 시간이며, 양수여야 합니다 |
| `max_concurrent` | `1` | FIFO worker 수이며, 1..8 범위입니다 |
| `max_tasks` | `100` | 보관할 작업 수의 상한이며, 양수여야 합니다. 완료된 worktree/기록을 오래된 순서로 제거하고 브랜치는 유지합니다 |

## 감사 로그 형식

공통 래퍼는 처리한 도구 호출마다 JSON 객체 하나를 `server.audit_log`에 추가하며, 경로가 비어 있으면 stderr에 기록합니다. 새 파일은 umask의 영향을 받는 0600 권한으로 만들고 기존 파일의 권한은 유지합니다. 심볼릭 링크, 일반 파일이 아닌 파일, 그룹이나 다른 사용자가 쓸 수 있는 파일은 거부합니다. Stdout은 MCP 메시지 전용입니다.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"k8s_get","args":{"kind":"deployment","namespace":"default","name":"web"},"outcome":"ok","duration_ms":12,"output_bytes":120}
```

`audit.Event`의 필드는 다음과 같습니다.

- `time`: 소수점 이하 초를 포함할 수 있는 RFC3339 타임스탬프입니다.
- `user`, `transport`, `tool`.
- `args`: 마스킹한 인자이며, 선택 사항입니다.
- `outcome`: `ok`, `error`, `denied`, `pending` 중 하나입니다.
- `error`: 마스킹한 오류이며, 선택 사항입니다.
- `duration_ms`.
- `output_bytes`: 성공 시 반환한 텍스트의 크기이며, 오류 시 0입니다.

쓰기 이벤트에는 마스킹한 `plan`(사람에게 보여 준 요약)과 `approval`을 추가로 포함할 수 있습니다. `approval`은 확인 방식인 `elicitation`, `web`, `window` 중 하나이며, 거부되거나 대기 중인 호출에도 포함할 수 있습니다. `pending`은 사람의 입력을 기다리는 상태입니다. 브라우저 재시도 안내는 `output_bytes`에 포함하며, 클라이언트 내 확인의 입력 요청은 텍스트 출력을 반환하지 않습니다. 계획(Plan)과 실행(Apply)에는 각각 별도의 도구 제한 시간을 적용합니다. 사람의 확인에는 더 오래 걸릴 수 있습니다.

웹 승인 결정은 별도의 감사 이벤트로 기록합니다. `outcome`은 `approved` 또는 `rejected`이며, 사용자, 도구와 8글자의 `args.approval_id_prefix`만 포함합니다. 여러 차례 왕복하는 승인 과정에서 검증을 거부하면 요청 상태 없이 `denied` 이벤트를 기록합니다. 도구 출력 본문은 저장하지 않습니다. 감사 로그 쓰기에 실패하면 도구 호출을 실패시키지 않고 stderr에 오류를 보고합니다. 감사 로그는 변조를 막지 못하며 민감한 정보가 남아 있을 수 있으므로, 로그를 보호하고 순환 관리하세요.

승인 시간 창을 만들 때는 원래 승인 방식과 `args.window`/`args.expires_at`을 담은 `window_granted`를 기록합니다. 이후 이 권한을 사용하는 호출에는 `approval: window`를 기록합니다.

### 여러 웹훅 대상에 직접 전송

`watch.outputs`는 최대 20개의 전송 대상을 받으며, 기존 웹훅·이메일과 함께 쓸 수
있습니다. `type: discord`는 n8n 없이 Discord로 직접 전송하고, `type: webhook`은
원본 JSON이나 `body_template`으로 만든 JSON을 POST합니다.
각 대상은 별도 큐·쿨다운·재시도를 사용합니다.

대상별 설정은 `name`(중복 없는 ASCII 영숫자·밑줄·하이픈 1–64자),
`type`(기본 `webhook`), `url` 또는 `url_file`(둘 중 하나 필수), `headers`,
`header_files`, `secret_file`, `body_template`(최대 64 KiB),
`allow_private_http`(기본 false), `problems_only`(기본 false)입니다.
공통 `watch.batch`, `watch.cooldown`, `watch.max_alerts`를 사용합니다.

[한글 사용 예시와 제한](watch.md#discord-직접-전송과-커스텀-웹훅),
[전체 설정 표](../configuration.md#direct-webhook-destinations)를 참고하세요.
