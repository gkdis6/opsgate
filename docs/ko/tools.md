# 도구 참조

[English](../tools.md)

Kubernetes, 노트 읽기 도구에는 `ops:read`가 필요하고, dev 도구에는 `dev:run`이 필요합니다. 각 도구는 provider를 활성화했을 때만 등록됩니다. Kubernetes 네임스페이스는 설정에 따라 필터링하며, 네임스페이스 거부 목록이 우선합니다. Kubernetes 시크릿을 다루는 도구는 없습니다. 읽기 전용 메타데이터와 로그에도 민감한 정보가 드러날 수 있습니다.

| 도구 | 용도 | 주요 인자, 기본값, 제한 |
| --- | --- | --- |
| `k8s_clusters` | 설정 순서대로 클러스터 이름, 컨텍스트, 보호 여부, 네임스페이스·로그·쓰기 정책과 연결 상태 | 없음; 클러스터마다 discovery 조회 제한 2초 |
| `k8s_overview` | 노드 상태와 허용된 네임스페이스의 워크로드/이벤트 | `cluster`(아래 규칙 적용) |
| `k8s_list` | 지원하는 리소스 목록 | 필수 `kind`; `namespace=""`(허용된 모든 네임스페이스); `label_selector=""`(필터 없음), Kubernetes 레이블 선택자 문법; `limit=100`, 1–500으로 보정 |
| `k8s_get` | 민감한 정보를 정리한 객체 YAML과 최근 1시간의 관련 이벤트 최대 50개 | 필수 `kind`, `name`; 네임스페이스 스코프 리소스는 `namespace` 필수, 클러스터 스코프 리소스는 생략 |
| `k8s_env` | 컨테이너가 실제로 받는 환경 변수와 마운트된 설정, 각 값의 출처 | 필수 `namespace`, `name`; `kind="pod"`(pod, deployment, statefulset, daemonset, replicaset, job, cronjob 및 별칭); `container=""`(일반·init 컨테이너 전체); ConfigMap 값은 `show_configmap_data=true`일 때만 표시; 마운트된 텍스트는 키당 8 KiB, 호출당 64 KiB로 제한 |
| `k8s_service_check` | 서비스 호스트 이름의 DNS 설정과 준비된 백엔드 점검(설정만 확인하며 DNS 질의·연결을 보내지 않고 NetworkPolicy는 평가하지 않음) | 필수 `namespace`(조회하는 쪽 네임스페이스), `host`(소문자 DNS 이름, 최대 253바이트, IP 주소 불가); 선택 `port`(서비스 포트 번호 또는 이름), `pod`(DNS 설정을 가져올 같은 네임스페이스의 파드); 클러스터 도메인은 `cluster.local`로 가정하고, 도메인까지 적은 이름은 다른 도메인도 허용; 조회 후보, Service, 선택자에 맞는 파드(최대 500개), EndpointSlice를 확인; 다른 허용 네임스페이스의 같은 이름 Service를 최대 10개 제안 |
| `k8s_logs` | 파드 로그(하나 이상의 클러스터에서 `allow_logs=true`일 때 도구 등록) | 필수 `namespace`, `pod`; 일반/init/ephemeral 컨테이너를 통틀어 정확히 하나일 때만 `container` 생략 가능; `tail=200`, 1–`max_log_lines`로 보정(기본 상한 500); `since=""`(시간 필터 없음), 양수 Go 기간 표기(최대 168h); `previous=false`; `grep=""`, 문자열 그대로 대소문자 구분 없이 부분 검색; 스트림 상한 1 MiB |
| `k8s_events` | 네임스페이스 이벤트 | `namespace=""`(허용된 모든 네임스페이스); `warnings_only=true`; `since="1h"`, 양수 Go 기간 표기(최대 168h); `limit=50`, 1–500으로 보정 |
| `k8s_rollout_restart` | 파드 템플릿을 갱신해 워크로드 재시작 | 필수 `kind`(deployment/statefulset/daemonset), `namespace`, `name`; `rollout_restart` 허용 설정 필요 |
| `k8s_scale` | 워크로드 복제본 수 설정 | 필수 `kind`(deployment/statefulset), `namespace`, `name`, `replicas`(0–`max_replicas`, 기본 상한 10); `scale` 허용 설정 필요 |
| `k8s_delete_pod` | UID 사전 조건을 적용한 특정 파드 삭제 | 필수 `namespace`, `name`; `delete_pod` 허용 설정 필요 |
| `note_search` | 노트 내용/파일명에서 문자열 그대로 검색, 발췌문과 검색한 항목 수 제공 | 필수 `query`(1–4096 UTF-8 바이트); 선택 `folder`, `tag`, `case_sensitive=false`; `limit=20`, 최대 100 |
| `note_read` | 노트 텍스트와 파일/줄 메타데이터 | 필수 `path`, `.md` 생략 가능; `offset_line=1`, `max_lines=400`, 최대 2000 |
| `note_list` | 폴더를 먼저 표시한 뒤 .md/.canvas/.base 파일 표시 | `folder=""`(볼트 루트), `recursive=false`, `limit=200`, 최대 1000; 잘림 여부 보고 |
| `note_recent` | 수정 시간순 노트 | `limit=20`, 최대 100; `days=7`, 최대 365 |
| `note_links` | 노트에 포함된 위키 링크/임베드/상대 Markdown 링크와 이 노트를 가리키는 백링크 | 필수 `path`; 정확한 경로나 중복되지 않는 기본 파일명으로 대상 확인; 찾지 못한 대상 보고 |
| `note_tags` | frontmatter/인라인 태그 수 또는 일치하는 노트 | 선택 `tag`; 중첩 태그 포함; 코드 블록과 제목 무시 |
| `note_create` | 노트와 아직 없는 상위 폴더 생성 | 필수 `path`, `content`; `allow_write=true`, ops:write, 사람의 승인 필요 |
| `note_append` | 노트 끝이나 정확히 일치하는 제목이 하나뿐일 때 그 아래에 내용 추가 | 필수 `path`, `content`; 선택 `heading`(최대 1024바이트); 같은 쓰기 조건 |
| `note_replace` | 정확한 부분 문자열 치환 | 필수 `path`, 비어 있지 않은 `old_text`, `new_text`; 치환 횟수는 `expected_count=1`과 일치해야 함; 같은 쓰기 조건 |
| `note_trash` | 충돌 시 접미사를 붙여 로컬 .trash에 노트 보존 | 필수 `path`; 같은 쓰기 조건; 영구 삭제하지 않음 |
| `dev_projects` | 코딩 프로젝트와 에이전트 허용 목록(`dev:run`) | 없음; runner 제한 포함 |
| `dev_task_start` | 사람의 승인 후 에이전트 시작(`dev:run`) | 필수 `project`, `agent`, `prompt`; 선택 `approval_id`; 프롬프트 최대 32 KiB(MCP 인자 48 KiB, 승인 요약 64 KiB) |
| `dev_task_status` | 본인 작업의 상태와 최종 메시지(runner 관리자는 모든 작업에 접근 가능)(`dev:run`) | 필수 `id` |
| `dev_task_log` | 신뢰할 수 없는 작업 로그 페이지(`dev:run`) | 필수 `id`; `offset=0`(0–20 MiB), `limit=65536`, 최대 262144바이트; 원본 기준 `next_offset` |
| `dev_task_diff` | 고정한 기준과 비교한 변경 통계와 패치(신뢰할 수 없는 데이터)(`dev:run`) | 필수 `id`; MCP 출력 상한 적용 전 최대 256 KiB |
| `dev_task_list` | 보존된 작업을 최신순으로 표시(`dev:run`) | `limit=20`, 범위 1..100; 최종 메시지 제외 |
| `dev_task_cancel` | 새 승인 없이 본인 작업 취소(runner 관리자는 모든 작업 취소 가능)(`dev:run`) | 필수 `id`; 상태 변경 및 실행 중인 프로세스 그룹 종료 |

노트 검색, 태그 및 링크 도구는 마스킹된 텍스트를 대상으로 동작합니다.

노트 경로는 슬래시로 구분한 볼트 기준 상대 경로이며, Unicode 바이트를 그대로 유지합니다. 경로 길이는 최대 1024바이트이고, 제외된 경로는 없는 경로처럼 처리합니다. 노트 텍스트는 신뢰할 수 없는 데이터로 취급합니다. 노트 도구는 문서에 명시한 범위를 벗어난 숫자 입력을 거부합니다. 볼트 내부의 안전한 심볼릭 링크를 지원하되 순환 참조를 방지합니다. 검색 범위는 항목 100000개와 디렉터리 깊이 128단계로 제한하며, 링크와 백링크는 각각 최대 1000개를 반환합니다.

쓰기 미리보기에는 diff(생성 시 새 내용), 현재 해시, 결과 해시가 들어갑니다. 약 8 KiB에서 출력을 자르고 잘렸음을 표시합니다. 실행(Apply) 단계에서는 승인 당시의 해시, 크기, 수정 시간을 다시 검사합니다. 새 노트는 원자적으로 생성하며, 동시에 다른 작업이 생성한 파일을 덮어쓰지 않습니다. 노트를 갱신할 때는 임시 파일을 동기화한 뒤 rename으로 교체합니다.

Linux에서는 원본을 승인된 .trash 경로로 원자적으로 이동한 뒤 노트를 검증하며, 기존 항목을 덮어쓰지 않습니다. 이동 도중 다른 작업이 노트를 교체하거나 편집하면 .trash 내 복구 경로와 함께 오류를 반환합니다. 필요하면 그 파일을 복원하세요. 계획(Plan) 후 대상 경로에 다른 항목이 생기면 이동이 실패하고 원본은 그대로 남습니다. 원본과 .trash는 RENAME_NOREPLACE를 사용하는 renameat2를 지원하는 같은 파일시스템에 있어야 하며, 이 조건을 충족하지 않으면 원본을 유지한 채 이동이 실패합니다.

다른 플랫폼에서는 승인된 내용의 복사본을 디스크에 안전하게 저장한 뒤 원본을 검사하고 제거합니다. 마지막 검사와 제거 사이에 외부에서 원본을 교체하면 교체한 내용이 유실될 수 있으므로, 이런 플랫폼에서는 버전별 백업이나 동기화 이력을 사용하세요.

기존의 마지막 줄바꿈 정책은 유지합니다. 내용이나 치환에 쓰는 각 입력의 상한은 256 KiB입니다. create, append, replace 도구는 이스케이프를 포함한 원시 JSON 인자를 528 KiB까지 허용하고, trash는 기본 16 KiB 제한을 유지합니다.

노트를 갱신할 때는 현재 상태가 바뀌지 않았는지 확인한 뒤 rename으로 교체합니다. 검사와 rename 사이에 외부에서 편집한 내용은 덮어쓸 수 있으므로, 버전별 백업이나 동기화 이력을 사용하세요. 새 노트의 미리보기도 크면 자르고 생략한 줄 수를 표시합니다.

Kubernetes 읽기 도구의 숫자 입력에 0을 지정하면 기본값을 사용하고, 그 외에 범위를 벗어난 값은 범위 안으로 보정합니다. 쓰기 작업의 복제본 수는 보정 없이 검증합니다. 반환하는 모든 텍스트에는 `server.max_output_bytes`(기본값 65536)가 적용됩니다. 호출의 제한 시간(`server.tool_timeout`, 기본값 30s)은 도구가 취소 신호를 따를 때 적용됩니다.

Kubernetes 네임스페이스 입력은 DNS-1123 레이블이어야 합니다. 객체의 `name`, `pod`, `container` 입력은 DNS-1123 하위 도메인으로 검증합니다.

`k8s_list`와 `k8s_get`이 지원하는 `kind` 값은 대소문자를 구분하지 않습니다. 괄호 안은 별칭입니다: pods (po, pod), deployments (deploy, deployment), statefulsets (sts, statefulset), daemonsets (ds, daemonset), replicasets (rs, replicaset), jobs (job), cronjobs (cronjob), services (svc, service), ingresses (ing, ingress), configmaps (cm, configmap), persistentvolumeclaims (pvc, persistentvolumeclaim), persistentvolumes (pv, persistentvolume), nodes (no, node), namespaces (ns, namespace).

Nodes, PersistentVolumes, Namespaces는 클러스터 스코프 리소스이므로 `namespace`를 생략하세요. 네임스페이스 허용 목록과 거부 목록은 Namespace 객체를 이름으로 필터링하지만, Nodes와 PersistentVolumes는 제한하지 않습니다. `k8s_overview`에 표시하는 노드도 이 제한을 받지 않습니다. 네임스페이스 스코프 리소스와 이벤트는 네임스페이스에 따라 필터링하며, 허용 목록이 비어 있으면 아무것도 허용하지 않습니다. 클러스터 스코프 접근은 RBAC으로 제한하세요.

[읽기 전용 RBAC](../../deploy/kubernetes/rbac-readonly.yaml)을 바탕으로 설정하고, 배포 전에 실제로 허용하는 권한을 검토하세요. 이 매니페스트는 기존 `opsgate` 네임스페이스에 ServiceAccount를 만들고, ClusterRole과 ClusterRoleBinding을 생성합니다. 시크릿을 제외한 지원 리소스, 이벤트, 파드 로그와 `discovery.k8s.io`의 EndpointSlice에 클러스터 전체 get/list/watch 권한을 부여합니다. 접근 범위를 좁히려면 네임스페이스 RoleBinding을 사용하세요.

kubeconfig의 `exec` 및 `auth-provider` 자격 증명 플러그인은 허용하지 않습니다. 수명이 짧은 ServiceAccount 토큰처럼 플러그인이 필요 없는 자격 증명을 사용하세요. kubeconfig의 소유권과 접근 권한도 안전하게 관리하세요.

<a id="redaction-and-object-sanitization"></a>

`k8s_clusters`를 제외한 모든 Kubernetes 도구는 `cluster` 인자를 받습니다. 클러스터가 여러 개이면 필수이며, 하나이면 생략할 수 있습니다. 지정한 이름이 없거나 필수 인자를 생략하면 설정된 이름 목록과 함께 호출을 거부합니다. `k8s_overview`도 한 클러스터만 조회합니다. 먼저 `k8s_clusters`로 대상을 확인하고, 쓰기 전에 사용자에게 클러스터 이름을 명시합니다. 이전 출력의 클러스터를 다음 호출의 대상으로 추정하지 않습니다.

각 Kubernetes 도구의 출력은 `cluster: <name>` 줄로 시작합니다. `k8s_clusters`는 클러스터마다 이 줄을 표시하며 kubeconfig 경로, 서버 URL, 자격 증명은 출력하지 않습니다. 연결 실패 시 민감한 연결 정보가 드러나지 않도록 진단 메시지를 제한합니다. 한 클러스터의 실패는 나머지 결과에 영향을 주지 않습니다. 로그·쓰기 도구는 하나 이상의 클러스터에서 허용하면 등록하고, 선택한 클러스터에서 꺼져 있으면 호출을 거부합니다. 네임스페이스 규칙, ConfigMap 값 공개 여부, 로그 제한, 쓰기 허용 목록, 최대 복제본 수는 모두 선택한 클러스터의 설정을 따릅니다. 보호된 클러스터는 쓰기마다 사람의 승인을 새로 받아야 합니다.

## 마스킹과 객체 정리

`k8s_logs`의 grep은 마스킹된 텍스트를 검색합니다.

공유 `internal/redact` 계층은 PEM 개인 키, Authorization/Proxy-Authorization 헤더, Basic/Bearer 자격 증명, JWT 형태의 토큰, URL 비밀번호를 마스킹합니다. AWS 액세스 키 ID, GitHub, GitLab, Slack, Stripe live 키, OpenAI/Anthropic 형식 키, Google API 키, Telegram 봇, opsgate의 `og_at_`, `og_rt_`, `og_st_` 토큰(접두사 뒤 base64url 문자 43개)의 알려진 패턴도 마스킹합니다.

`:`/`=`로 값을 지정하거나 구조화된 인자로 값을 전달할 때는 password/passwd/pwd, secret/client_secret, token/access_token/refresh_token, api_key/apikey/access_key/secret_key/private_key, credential/credentials, auth_token, session_id, cookie, set-cookie, invite를 민감한 키로 취급합니다. `db_password`처럼 밑줄로 연결한 접두사가 붙은 키도 포함하며, 대소문자는 구분하지 않습니다. 구조화된 인자 맵과 슬라이스는 재귀적으로 순회해 검사합니다. 성공 응답의 텍스트, 오류, 감사 로그 인자를 마스킹하지만, **모든 비밀값을 가린다고 보장하지는 않습니다**. 알려지지 않은 형식이나 임의의 데이터 속에 들어 있는 비밀값은 마스킹을 놓칠 수 있습니다.

`k8s_get`은 템플릿을 포함한 메타데이터에서 `managedFields`와 `kubectl.kubernetes.io/last-applied-configuration`도 제거합니다. ConfigMap의 `data`와 `binaryData`는 키를 유지하되, `show_configmap_data=true`가 아니면 값 대신 바이트 수를 표시합니다. 환경 변수 이름에 pass, secret, token, key, credential, auth, dsn, private, cert, database_url이 포함되면 리터럴 `value` 필드를 `[REDACTED]`로 바꾸며, 대소문자는 구분하지 않습니다. 파드와 지원하는 워크로드 템플릿(CronJobs 포함)의 일반/init/ephemeral 컨테이너에 이 규칙을 적용합니다. 다른 리터럴 값은 공통 마스킹 규칙에 일치하지 않으면 그대로 보입니다. `valueFrom` 참조도 그대로 보여 주며, 참조하는 값을 조회하지는 않습니다.

`k8s_env`는 같은 네임스페이스의 ConfigMap을 호출당 한 번씩 조회하고, 환경 변수 덮어쓰기 순서와 `$(VAR)` 치환 결과를 표시합니다. 치환한 값을 다시 치환하지 않으며, 마운트에 지정한 항목과 `subPath` 파일만 보여 줍니다. 민감한 환경 변수·키 이름에 해당하는 값과 Secret 값은 숨깁니다. Secret API를 호출하지 않으므로 Secret `envFrom`의 키 목록은 알 수 없으며, 기존 변수와 이름이 겹칠 가능성을 표시합니다. 파드의 downward 필드는 실제 값으로 표시하지만, 워크로드의 필드와 리소스 참조는 자리표시자로 남깁니다. 바이너리 파일은 크기만 표시하고 `subPathExpr`는 해석하지 않습니다. 이미지에 포함된 파일은 확인할 수 없습니다.

`k8s_env`는 `k8s_get`의 민감한 키 규칙에 해당하는 downward API 레이블·어노테이션 값을 조회하거나 치환하기 전에 마스킹하고, `k8s_list`는 같은 규칙에 해당하는 키를 사용하는 레이블 선택자를 거부합니다.

<a id="shared-limits"></a>

## 공통 제한

승인이 필요한 도구 8개는 모두 브라우저에서 재시도할 때 쓰는 `approval_id`를 선택 인자로 받습니다. 첫 호출에서는 생략하세요. dev 작업 ID는 소문자 base32 문자 20개로 구성됩니다. 작업 상태, 로그, diff, 목록을 조회하거나 작업을 취소할 때는 요청자 본인의 작업인지 검사하며, runner 관리자는 모든 작업에 접근할 수 있습니다.

읽기 도구와 `dev_task_cancel`은 원시 JSON 인자를 최대 16 KiB까지 받습니다. 쓰기 인자의 기본 상한도 16 KiB이지만, `note_create`, `note_append`, `note_replace`는 528 KiB, `dev_task_start`는 48 KiB까지 허용합니다. JSON 이스케이프도 이 크기에 포함됩니다. 승인 요약의 상한은 `max(64 KiB, argument limit + 16 KiB)`로, 기본 도구와 dev 작업 시작은 64 KiB, 노트 내용을 다루는 도구는 544 KiB입니다. 화면에 표시할 마스킹한 인자도 각 도구의 인자 제한을 넘을 수 없습니다. 노트 미리보기에는 별도로 약 8 KiB의 표시 제한이 있습니다.

공유 래퍼는 4 MiB를 초과하는 provider 출력을 통째로 생략합니다. 그 이하는 텍스트를 마스킹한 뒤 `server.max_output_bytes`(기본값 64 KiB)에 맞춰 자르며, 오류도 같은 방식으로 처리합니다. provider별 수집 제한이 더 작으면 그 제한도 적용됩니다. 제한 시간은 도구가 취소 신호를 따를 때 적용됩니다. 쓰기 작업은 계획(Plan)과 실행(Apply)에 각각 별도의 `server.tool_timeout`(기본값 30s)을 적용하며, 사람이 승인할 때까지 기다리는 시간은 제외합니다.
