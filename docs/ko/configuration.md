# 설정 레퍼런스

[English](../configuration.md)

아래 기본값은 `config.Default()`에서 가져옵니다. YAML은 기본값 위에 적용되며, 알려진 필드만 엄격하게 허용합니다. 알 수 없는 키가 있으면 실패합니다. HTTP, 인증, 승인 설정도 아래에 포함되어 있습니다. 빈 슬라이스는 `[]`로 표시합니다. Go에서는 일부가 nil입니다.

| YAML 키 | 기본값 | 설명 |
| --- | --- | --- |
| `server.name` | `opsgate` | MCP 서버 이름 |
| `server.audit_log` | `""` | 비어 있으면 stderr에 기록합니다. 그 외에는 JSONL 경로입니다 |
| `server.max_output_bytes` | `65536` | 도구 출력의 바이트 제한입니다. 양수여야 합니다 |
| `server.tool_timeout` | `30s` | 호출이 협조적으로 준수하는 제한 시간입니다. 쓰기의 계획과 적용에는 각각 별도의 제한 시간을 부여합니다. 양수여야 합니다 |
| `stdio.user` | `local` | 감사 로그의 사용자 식별 레이블입니다. OS 사용자가 신뢰 경계입니다 |
| `stdio.scopes` | `[ops:read]` | 허용하는 스코프는 ops:read, ops:write, dev:run입니다. 쓰기 스코프만으로 작업이 활성화되지는 않습니다 |
| `providers.host.enabled` | `false` | host provider를 활성화합니다 |
| `providers.host.units` | `[]` | 유닛 glob 허용 목록입니다. 비어 있으면 아무것도 허용하지 않습니다 |
| `providers.host.disk_paths` | `[/]` | 개요에 사용할 마운트 지점입니다 |
| `providers.host.max_log_lines` | `500` | 호출당 journal 로그 줄 수의 상한입니다 |
| `providers.kubernetes.enabled` | `false` | Kubernetes provider를 활성화합니다 |
| `providers.kubernetes.kubeconfig` | `""` | 비어 있으면 클러스터 내부 설정, $KUBECONFIG, ~/.kube/config 순으로 사용합니다 |
| `providers.kubernetes.context` | `""` | 컨텍스트를 재정의합니다. 비어 있으면 선택된 컨텍스트를 사용합니다 |
| `providers.kubernetes.namespaces` | `[]` | 네임스페이스 glob 허용 목록입니다. 비어 있으면 아무것도 허용하지 않으며, *는 모두 허용합니다 |
| `providers.kubernetes.deny_namespaces` | `[]` | 거부 glob이 허용 glob보다 우선합니다 |
| `providers.kubernetes.allow_logs` | `false` | Pod 로그를 활성화합니다 |
| `providers.kubernetes.show_configmap_data` | `false` | false이면 키만 노출하고 값은 숨깁니다 |
| `providers.kubernetes.max_log_lines` | `500` | 호출당 Pod 로그 줄 수의 상한입니다 |
| `http.listen` | `127.0.0.1:8787` | 백엔드 TCP 주소입니다 |
| `http.public_url` | `""` | 필수 HTTP origin입니다. 루프백 HTTP를 제외하면 HTTPS여야 합니다. 리소스는 origin + /mcp입니다 |
| `auth.mode` | `""` | HTTP에서 필수입니다. builtin, external, static 중 하나입니다 |
| `auth.builtin.state_file` | `""` | 클라이언트와 토큰 해시를 권한 0600으로 영속 저장하는 선택적 파일입니다. 비어 있으면 재시작 시 상태가 사라집니다 |
| `auth.builtin.users` | `[]` | 허용하는 업스트림 사용자입니다. 비어 있으면 모두 거부합니다 |
| `auth.builtin.users[].subject` | `""` | passkey:name, github:login, email:verified-email 또는 oidc:sub(정확한 OIDC subject)입니다 |
| `auth.builtin.users[].scopes` | `[]` | 부여할 스코프입니다. ops:read, ops:write, dev:run을 사용합니다 |
| `auth.builtin.upstream.provider` | `""` | passkey(인증 provider가 없고 클라이언트 ID나 시크릿이 필요하지 않음), github 또는 oidc입니다 |
| `auth.builtin.upstream.issuer` | `""` | 필수 OIDC 발급자입니다. 예: https://accounts.google.com |
| `auth.builtin.upstream.client_id` | `""` | 업스트림 애플리케이션의 클라이언트 ID입니다 |
| `auth.builtin.upstream.client_secret_file` | `""` | 시크릿 파일입니다. 이 설정 또는 client_secret_env를 사용합니다 |
| `auth.builtin.upstream.client_secret_env` | `""` | 시크릿을 담은 환경 변수의 이름입니다 |
| `auth.builtin.access_token_ttl` | `1h` | 액세스 토큰의 수명입니다 |
| `auth.builtin.refresh_token_ttl` | `720h` | 리프레시 토큰의 수명입니다 |
| `auth.builtin.max_clients` | `100` | 동적 클라이언트 등록(DCR)의 수용 한도입니다 |
| `auth.external.issuer` | `""` | discovery와 JWKS에 사용할 신뢰하는 발급자입니다 |
| `auth.external.audience` | `""` | 비어 있으면 public_url + /mcp를 사용합니다 |
| `auth.external.user_claim` | `sub` | 사용자를 식별하는 문자열 클레임입니다 |
| `auth.external.users` | `[]` | 비어 있으면 토큰 스코프를 신뢰합니다. 그 외에는 사용자를 제한하고 토큰 스코프와 부여된 스코프의 교집합을 적용합니다 |
| `auth.external.users[].subject` | `""` | 설정한 user_claim에 대한 claim:value입니다 |
| `auth.external.users[].scopes` | `[]` | 이 사용자에게 허용할 최대 스코프입니다 |
| `auth.static` | `[]` | 설정된 bearer 토큰입니다. 이 모드에는 OAuth 로그인이 없습니다 |
| `auth.static[].name` | `""` | 토큰의 사용자 식별 및 감사 로그 레이블입니다 |
| `auth.static[].sha256` | `""` | 16진수 SHA-256 다이제스트입니다. 원본 토큰을 넣지 않습니다 |
| `auth.static[].scopes` | `[]` | 토큰에 부여할 스코프입니다. HTTP 접근에는 ops:read를 포함해야 합니다 |
| `approval.ttl` | `10m` | 대기 중인 승인/입력의 수명입니다 |
| `approval.web` | `false` | 브라우저 대체 경로입니다. builtin 인증이 필요합니다 |
| `approval.require_web` | `false` | 웹 승인을 필수로 합니다. `approval.web`이 필요하며 stdio 쓰기를 거부합니다 |
| `approval.window` | `0s` | 사용자/도구별 자동 승인의 선택적 수명입니다. 활성화 시 1m–12h입니다 |
| `approval.window_tools` | `[]` | 승인 시간 창을 허용할 도구 이름의 `path.Match` glob입니다. 비어 있으면 아무것도 허용하지 않습니다 |
| `providers.host.restart_units` | `[]` | 재시작할 유닛의 별도 glob 허용 목록입니다. 비어 있으면 도구를 비활성화합니다 |
| `providers.host.command_prefix` | `[]` | 쓰기 argv 접두사입니다. 예: [/usr/bin/sudo, -n]. 첫 요소는 실행 파일의 절대 경로이며 나머지는 경로/플래그입니다 |
| `providers.kubernetes.rollout_restart` | `[]` | namespace/name glob입니다. 비어 있으면 도구를 비활성화합니다 |
| `providers.kubernetes.scale` | `[]` | namespace/name glob입니다. 비어 있으면 도구를 비활성화합니다 |
| `providers.kubernetes.delete_pod` | `[]` | namespace/name glob입니다. 비어 있으면 도구를 비활성화합니다 |
| `providers.kubernetes.max_replicas` | `10` | 스케일 조정의 최댓값입니다. 음수가 아니어야 하며, 0이면 복제본 수 0만 허용합니다 |
| `providers.note.enabled` | `false` | Markdown 폴더나 Obsidian 볼트용 note provider를 활성화합니다. 노트 앱이나 CLI는 필요하지 않습니다 |
| `providers.note.vault` | `""` | 기존 볼트 디렉터리의 절대 경로입니다. 모든 접근은 os.Root로 제한합니다 |
| `providers.note.exclude` | `[]` | 볼트 상대 경로에 적용할 추가 path.Match glob입니다. 경로나 그 상위 경로와 일치하면 제외합니다. 점으로 시작하는 경로 구성 요소와 동기화 충돌 사본은 항상 제외합니다 |
| `providers.note.allow_write` | `false` | 노트 쓰기 도구 4개를 등록합니다. 각각 ops:write와 사람의 승인이 필요합니다 |
| `providers.note.max_note_bytes` | `1048576` | 읽기/쓰기 시 노트별 크기 제한입니다. 양수여야 합니다. 콘텐츠 검색에서 제한을 넘는 노트는 건너뜁니다 |
| `providers.dev.enabled` | `false` | dev provider를 활성화합니다. 모든 도구에 dev:run이 필요합니다 |
| `providers.dev.runner_socket` | `""` | 별도 dev-runner의 Unix 소켓 절대 경로입니다 |
| `server.tools.allow` / `server.tools.deny` | `[]` / `[]` | 도구 이름의 `path.Match` glob입니다. allow가 비어 있으면 제공되는 모든 도구를 허용하며 deny가 우선합니다. 제외한 도구는 tools/list에 나타나지 않습니다 |
| `auth.builtin.users[].tools` | `[]` | 사용자 호출을 제한하는 선택적 도구 이름 glob입니다. 비어 있으면 추가 제한이 없습니다 |
| `auth.external.users[].tools` | `[]` | 매핑된 사용자 호출을 제한하는 선택적 도구 이름 glob입니다. 비어 있으면 추가 제한이 없습니다 |
| `auth.static[].tools` | `[]` | 이 토큰의 호출을 제한하는 선택적 도구 이름 glob입니다. 비어 있으면 추가 제한이 없습니다 |
| `auth.builtin.allowed_redirect_hosts` | `[]` | 정확한 리다이렉트 호스트 이름입니다(포트 제외). localhost는 루프백 IP와 모든 포트를 포함합니다. 지원하는 사설 스킴은 cursor:처럼 지정합니다. 비어 있으면 그 외 유효성 조건을 충족하는 모든 리다이렉트 호스트를 허용합니다 |
| `auth.builtin.unknown_redirect_hosts` | `""` | 호스트 목록이 있을 때 빈 값/deny는 목록에 없는 목적지를 거부합니다. approve는 관리자 검토까지 클라이언트를 대기 상태로 둡니다 |
| `auth.builtin.admins` | `[]` | /admin/clients에서 클라이언트를 관리할 수 있는 업스트림 subject입니다. users에 포함되어야 하며 approve 모드에서 필수입니다 |
| `auth.builtin.notify_url` | `""` | 대기 중인 등록에 대한 선택적 JSON POST 주소입니다. HTTPS 또는 루프백 HTTP여야 하며 URL 자격 증명/프래그먼트는 허용하지 않습니다 |

서버 정책 패턴은 활성화된 provider에서 작업별 활성화 조건을 적용한 뒤 제공되는 도구와 일치해야 합니다. 그렇지 않으면 시작에 실패합니다. 사용자/토큰별 도구 패턴은 구문을 검사하고 호출 시 적용합니다. tools/list를 필터링하지는 않습니다. 스코프, 서버 정책, 사용자별 도구 정책, provider 리소스 허용 목록을 모두 적용합니다. 빈 도구 허용 목록은 추가 제한을 두지 않습니다. 빈 provider 리소스 허용 목록과는 의미가 다릅니다. 설정을 변경하면 재시작해야 합니다.

모든 provider는 기본적으로 비활성화되어 있습니다. host provider를 활성화하면 허용한 유닛이 없어도 개요/프로세스 도구를 노출합니다. Kubernetes를 활성화하면 RBAC에 따라 클러스터 범위의 읽기를 허용합니다. `ops:read`는 선택적인 실험적 note provider를 포함해 활성화된 모든 읽기 provider에 적용됩니다. `dev:run`은 dev 도구에 적용되며, HTTP에서는 `ops:read`도 필요합니다. [신뢰 모델](security.md)을 참고하세요.

노트 쓰기는 서버 계정으로 실행합니다. 따라서 생성하거나 다시 쓴 노트는 해당 계정이 소유합니다. 소유권 대신 ACL로 접근 권한을 부여하고, 볼트 소유자가 해당 파일을 교체할 수 있도록 디렉터리 쓰기 권한을 유지하세요. 볼트 소유자로 실행하는 동기화 도구는 자신이 소유하지 않은 파일에 chmod를 적용할 수 없습니다. Syncthing에서는 볼트 폴더의 Ignore Permissions 옵션을 설정하세요. 그렇지 않으면 다른 기기의 권한만 변경된 내용을 반영할 때 "operation not permitted"로 실패합니다.

읽기 전용 시작점과 주석으로 분리된 쓰기 활성화 설정은 [전체 예제](../../opsgate.example.yaml)를 참고하세요. 인자, 승인, 출력 제한은 [도구 레퍼런스](tools.md)에서 설명합니다.

## 인증과 사람의 승인

업스트림 애플리케이션 콜백은 정확히 `<public_url>/oauth/callback`입니다. Host를 보존하는 프록시와 로컬 프로브 규칙은 [배포](deploy.md)를 참고하세요.

`builtin`은 패스키로 로그인하거나 GitHub 또는 OIDC(Google 포함)에 로그인을 위임한 뒤 불투명 MCP 토큰을 발급합니다. 패스키 등록에는 일회용 초대가 필요합니다. 시작할 때 패스키가 없는 사용자마다 초대 하나를 로그에 기록합니다(24h). `opsgate passkey invite -config <file> -subject <user> [-ttl 24h]`로 추가 발급할 수 있습니다. 초대는 상태 파일 옆에 파일로 저장하므로 서버 계정으로 실행하세요.

PKCE S256을 사용하는 authorization code만 지원합니다. 동적 클라이언트 등록(DCR), 클라이언트별 동의, 리프레시 토큰 교체, 재사용 시 폐기를 지원합니다. 발급자는 `http.public_url`이며 리소스는 `<public_url>/mcp`입니다. 선택적 상태 영속 저장을 사용하면 재시작 후에도 클라이언트 등록과 토큰 해시를 유지합니다. 디렉터리와 백업을 보호하고 상태 파일 하나당 프로세스 하나를 사용하세요. 상태 읽기와 쓰기는 512 MiB로 제한합니다. 제한을 초과하는 쓰기는 이전 스냅샷을 보존하며, 실행 중인 인스턴스는 인증을 거부합니다.

Builtin OIDC 사용자는 확인된 `email:<address>` 또는 설정된 발급자의 정확한 `oidc:<sub>`와 일치할 수 있습니다. `oidc:<sub>`는 대소문자를 구분하며 변경되지 않는 식별자입니다. 둘 다 일치하면 `oidc:<sub>`가 우선하며 이메일 클레임은 필요하지 않습니다. Google 이메일 subject는 `gmail.com`/`googlemail.com`이거나, 이메일 도메인과 일치하는 `hd` 클레임(Google Workspace)이 있어야 합니다. 다른 Google 주소에서는 `email_verified`만으로 충분하지 않습니다. README의 Google 예제에서는 Workspace 주소를 사용하거나 `oidc:<sub>`를 설정하세요.

OAuth GET 경로는 요청 본문을 거부합니다. POST 본문 읽기에는 10초 제한 시간이 있습니다. 동의 전 인가 오류는 로컬 페이지에 표시합니다. 동의 화면의 Deny는 리다이렉트할 수 있습니다. DCR 수용 한도에 도달하면 생성 후 10분이 넘었고 토큰을 발급한 적이 없으며 진행 중인 인가도 없는 등록을 오래된 순서대로 제거합니다. 대기 중인 인가는 클라이언트당 20개, 전체 1000개로 제한하며 해당 범위의 가장 오래된 항목을 제거합니다. 웹 승인 로그인은 별도로 100개를 수용합니다.

`external`은 발급자 discovery와 JWKS를 사용해 JWT 서명을 검증하고 발급자, audience, 만료, 허용 알고리즘을 검사합니다. 발급자, discovery, JWKS URL에는 HTTPS가 필요합니다. 루프백 HTTP는 예외입니다. JWKS 가져오기 시도는 모든 요청에서 공유하며, 실패한 시도를 포함하여 1분에 한 번으로 제한합니다. 캐시된 유효한 키는 계속 사용할 수 있습니다. 새 서명 키는 대기 시간이 끝날 때까지 거부될 수 있습니다. audience 기본값은 MCP 리소스 URL입니다. 스코프는 `scope` 또는 `scp`에서 가져옵니다. 선택적 `claim:<value>` 사용자 허용 목록으로 사용자를 추가 제한하고 스코프의 교집합을 적용합니다.

`static`은 SHA-256 다이제스트로 설정한 사전 공유 bearer 토큰을 허용합니다. `opsgate -gen-token`으로 토큰과 다이제스트를 생성하세요. 원본 토큰은 클라이언트의 시크릿 저장소에만 보관하세요. static 모드는 DCR이나 OAuth 로그인을 제공하지 않습니다.

Host, Kubernetes, 노트 읽기 도구에는 `ops:read`가 필요합니다. Host/Kubernetes 쓰기에는 별도 작업 허용 목록이 필요하고 노트 쓰기에는 `allow_write`가 필요합니다. 이 작업에는 `ops:write`, 기반 OS/RBAC 권한, 사람의 확인이 필요합니다. Dev 도구에는 대신 `dev:run`이 필요하며 `dev_task_start`에는 사람의 확인이 필요합니다. HTTP 접근에는 `ops:read`도 필요하므로 원격 쓰기 사용자에게는 두 스코프가 모두 필요합니다. 네임스페이스 허용/거부 규칙은 Kubernetes 쓰기에도 적용합니다. 읽기용 유닛 허용 목록은 재시작을 허가하지 않습니다.

서버는 확인 전에 입력을 검증하고 부수 효과가 없는 계획을 만듭니다. MCP 클라이언트 내 확인(elicitation)을 지원하는 클라이언트는 프로토콜 버전에 따른 여러 차례의 왕복 입력 요청을 포함해 클라이언트 안에서 승인 프롬프트를 표시합니다. 그 외에는 builtin 인증과 `approval.web: true`를 사용하면 승인 링크와 `approval_id`를 반환합니다. 사용자는 링크를 열고 동일한 업스트림 사용자로 로그인하여 정확한 작업을 검토합니다. 승인 후에는 같은 도구에 정확히 같은 인자와 `approval_id`를 추가하여 다시 호출합니다.

승인은 만료되며 한 번만 사용할 수 있습니다. 사용자, 도구, 마스킹하지 않은 정규화 인자의 SHA-256(`approval_id` 제외), 계획에 연결됩니다. 마스킹으로 요약이 바뀌면 쓰기를 거부합니다. 사용자가 대상을 명확히 볼 수 있어야 하기 때문입니다. Kubernetes 계획에는 대상 UID가 포함됩니다. 실행에는 해당 호출의 계획과 식별자/버전 사전 조건을 사용합니다. 확인 채널이 없으면 쓰기를 거부합니다. stdio는 클라이언트 내 확인만 지원합니다.

선택적 승인 시간 창에는 `approval.window`와 `approval.window_tools`가 모두 필요합니다. `approval.window`는 1m–12h이며 `0s`는 기능을 비활성화합니다. `approval.window_tools`는 `path.Match` glob 허용 목록이며 비어 있으면 어떤 도구도 허용하지 않습니다. 예를 들어 `window: 30m`과 `window_tools: [host_service_restart]`를 설정하면 브라우저에서 **Approve + allow for 30m**를 선택하거나 클라이언트 내 확인에서 선택적 체크박스 **Also auto-approve host_service_restart for 30m**를 선택할 수 있습니다.

이 권한은 정확히 해당 사용자와 도구에 적용됩니다. 이후 다른 클라이언트에서 호출하거나 다른 인자 또는 대상을 사용해도 적용됩니다. `dev_task_start`에는 승인 시간 창을 활성화하지 마세요. 각 작업을 별도로 검토해야 합니다. 이 기능은 스코프를 부여하거나 작업 허용 목록을 우회하거나 입력 검증 또는 계획을 생략하지 않습니다. 권한은 메모리에만 존재하며 사용해도 갱신되지 않고 만료됩니다. 최대 100개이며 재시작하면 사라집니다. 호출마다 `approval: window`가 포함된 별도 감사 로그를 남깁니다. 권한 생성 시에는 출처(`web` 또는 `elicitation`), 기간, 만료 시각을 담은 별도 `window_granted` 이벤트를 기록합니다. `require_web`을 사용하면 브라우저에서만 이 권한을 얻을 수 있으며 stdio 쓰기는 계속 거부합니다.

승인 페이지에는 diff 강조 표시가 있는 계획, 요청자, 전송 방식, 만료 시각, 접힌 인자가 표시됩니다. 결정한 뒤 채팅으로 돌아가 어시스턴트에게 재시도를 요청하세요. 자동 리다이렉트는 없습니다. 승인 요약은 최대 `max(64 KiB, argument limit + 16 KiB)`까지 허용합니다. 원시 JSON 인자의 기본 제한은 16 KiB입니다. 노트 콘텐츠 도구는 528 KiB(콘텐츠/대체 필드당 256 KiB), `dev_task_start`는 32 KiB 프롬프트를 위해 48 KiB를 허용합니다. JSON 이스케이프도 전송 크기 제한에 포함됩니다.

클라이언트가 클라이언트 내 확인을 지원해도 웹 승인을 필수로 하려면 `approval.web: true`와 함께 `approval.require_web: true`를 설정하세요. 이 설정은 stdio 쓰기를 거부합니다. 침해된 클라이언트 UI는 클라이언트 내 확인을 자동 승인할 수 있습니다. 프롬프트 인젝션은 여전히 모델이 쓰기를 요청하게 만들 수 있습니다. 승인 전에 사람이 요약을 읽어야 합니다. [SECURITY.md](security.md)를 참고하세요.

`allowed_redirect_hosts`로 클라이언트 등록을 제한할 수 있습니다. 목록이 비어 있지 않으면 `unknown_redirect_hosts: deny`는 다른 호스트를 거부합니다. `approve`는 허용 목록의 관리자가 `/admin/clients`에서 승인할 때까지 해당 클라이언트를 대기 상태로 유지합니다. 대기 중인 클라이언트는 인가를 받을 수 없습니다. 선택적 `notify_url`에는 이벤트, 클라이언트 ID/이름, 리다이렉트 호스트, 관리자 URL이 포함된 JSON POST를 보냅니다. 알림에는 제한이 있으며 최선형으로 전송합니다. 클라이언트 이름은 클라이언트가 직접 제공한 값입니다. 등록 승인은 사용자 스코프를 부여하거나 동의를 생략하지 않습니다. 호스트 목록이 비어 있으면 그 외 유효성 조건을 충족하는 모든 리다이렉트 목적지를 허용하며 미등록 호스트 정책은 무시합니다.

## Dev runner 설정

runner는 `providers` 아래에 중첩하지 않는 별도 YAML 파일을 사용합니다. runner 설정은 신뢰하는 설정이며 HTTP 계정이 쓸 수 없어야 합니다. [설정과 위험](dev.md), [예제](../../deploy/dev-runner.example.yaml)를 참고하세요.

| Runner YAML 키 | 기본값 | 설명 |
| --- | --- | --- |
| `admins` | `[]` | 모든 작업을 읽고 취소할 수 있는 요청자 subject의 정확한 목록입니다. 일반 사용자는 자신의 작업에만 접근합니다 |
| `socket` / `socket_group` | Required | Unix 소켓의 절대 경로와 허용할 피어 그룹입니다 |
| `work_dir` | Required | 비공개 상태/worktree 디렉터리의 절대 경로입니다 |
| `projects` | `[]` | 고유한 소문자 `name`, 절대 `path`, `base_ref`(기본값 `HEAD`)의 허용 목록입니다 |
| `agents` | `{}` | 에이전트 이름을 고정된 `command` argv와 선택적 `env` KEY=VALUE 쌍에 매핑합니다. 실행 파일 경로는 절대 경로여야 합니다 |
| `timeout` | `1h` | 작업 제한 시간입니다. 양수여야 합니다 |
| `max_concurrent` | `1` | FIFO worker 수입니다. 1..8입니다 |
| `max_tasks` | `100` | 보관할 작업 수의 상한입니다. 양수여야 합니다. 완료된 가장 오래된 worktree/기록을 제거하고 브랜치는 유지합니다 |

## 감사 로그 형식

공통 래퍼는 처리한 도구 호출마다 JSON 객체 하나를 `server.audit_log`에 추가합니다. 경로가 비어 있으면 stderr에 기록합니다. 새 파일은 umask의 영향을 받는 0600 권한으로 생성하며 기존 권한은 유지합니다. 심볼릭 링크, 일반 파일이 아닌 파일, 그룹/다른 사용자가 쓸 수 있는 파일은 거부합니다. Stdout은 MCP 메시지 전용입니다.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"host_service_status","args":{"unit":"ssh.service"},"outcome":"ok","duration_ms":12,"output_bytes":120}
```

`audit.Event`의 필드는 다음과 같습니다.

- `time`: 소수점 이하 초를 포함할 수 있는 RFC3339 타임스탬프입니다.
- `user`, `transport`, `tool`.
- `args`: 마스킹한 인자입니다. 선택적입니다.
- `outcome`: `ok`, `error`, `denied`, `pending` 중 하나입니다.
- `error`: 마스킹한 오류입니다. 선택적입니다.
- `duration_ms`.
- `output_bytes`: 성공 시 반환한 텍스트의 크기입니다. 오류 시 0입니다.

쓰기 이벤트에는 선택적으로 마스킹한 `plan`(사람에게 보여 준 요약)과 `approval`도 포함됩니다. `approval`은 확인 방식인 `elicitation`, `web`, `window` 중 하나이며 거부되거나 대기 중인 호출에도 포함될 수 있습니다. `pending`은 사람의 입력을 기다리는 상태입니다. 브라우저 재시도 안내는 `output_bytes`에 포함합니다. 클라이언트 내 확인의 입력 요청은 텍스트 출력을 반환하지 않습니다. 계획과 적용에는 각각 별도의 도구 제한 시간이 적용됩니다. 사람의 확인에는 더 오래 걸릴 수 있습니다.

웹 승인 결정은 별도 감사 이벤트로 기록합니다. `outcome`은 `approved` 또는 `rejected`이며 사용자, 도구와 8글자의 `args.approval_id_prefix`만 포함합니다. 여러 차례 왕복하는 승인 검증을 거부하면 요청 상태 없이 `denied` 이벤트를 기록합니다. 도구 출력 본문은 저장하지 않습니다. 감사 로그 쓰기 실패는 도구 호출을 실패시키지 않고 stderr에 보고합니다. 감사 로그는 변조를 방지하지 않습니다. 민감한 정보가 남아 있을 수 있으므로 로그를 보호하고 순환 관리하세요.

승인 시간 창 생성 시 원래 승인 방식과 `args.window`/`args.expires_at`을 담은 `window_granted`를 기록합니다. 이후 해당 권한을 사용하는 호출에는 `approval: window`를 기록합니다.
