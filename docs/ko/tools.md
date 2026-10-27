# 도구 참조

[English](../tools.md)

호스트, Kubernetes, 노트 읽기 도구에는 `ops:read`가 필요합니다. dev 도구에는 `dev:run`이 필요합니다. 도구는 해당 provider가 활성화된 경우에만 등록됩니다. 호스트 unit과 Kubernetes 네임스페이스는 설정으로 필터링하며, 네임스페이스 거부 목록이 우선합니다. Kubernetes Secrets 도구는 없습니다. 읽기 전용 메타데이터와 로그도 민감한 정보를 노출할 수 있습니다.

| 도구 | 용도 | 주요 인자, 기본값, 제한 |
| --- | --- | --- |
| `host_overview` | 호스트 지표와 설정된 디스크 경로 | 없음 |
| `host_services` | 허용 목록의 systemd unit | 없음 |
| `host_service_status` | 허용 목록의 unit 상태 | 필수 `unit`; 접미사가 없으면 `.service`를 붙입니다. 정규화한 이름은 최대 256바이트입니다. |
| `host_logs` | 허용 목록의 journald 로그 | 필수 `unit`(상태 조회와 같은 규칙); `lines=100`, 1–`max_log_lines`로 보정(기본 상한 500); `since=""`(시간 필터 없음), `d`를 포함한 양수 기간(최대 128바이트) 또는 RFC3339; `priority=""`(필터 없음), emerg/alert/crit/err/warning/notice/info/debug 또는 0–7; `grep=""`, 대소문자를 구분하지 않는 리터럴 부분 문자열, 최대 4096바이트 |
| `host_top_processes` | /proc의 프로세스 사용량을 500ms 동안 표본 수집. 이름만 표시하며 argv와 환경은 제외합니다. | `sort="cpu"`(cpu 또는 mem); `limit=15`, 1–50으로 보정 |
| `k8s_overview` | 노드 상태와 허용된 네임스페이스의 워크로드/이벤트 | 없음 |
| `k8s_list` | 지원하는 리소스 목록 | 필수 `kind`; `namespace=""`(허용된 모든 네임스페이스); `label_selector=""`(필터 없음), Kubernetes 레이블 선택자 문법; `limit=100`, 1–500으로 보정 |
| `k8s_get` | 민감한 정보를 정리한 객체 YAML과 최근 1시간의 관련 이벤트 최대 50개 | 필수 `kind`, `name`; 네임스페이스 범위 종류에는 `namespace`가 필수이며, 클러스터 범위 종류에는 생략합니다. |
| `k8s_logs` | Pod 로그. `allow_logs=true`일 때만 등록합니다. | 필수 `namespace`, `pod`; 일반/init/ephemeral 컨테이너가 통틀어 정확히 하나일 때만 `container` 생략 가능; `tail=200`, 1–`max_log_lines`로 보정(기본 상한 500); `since=""`(시간 필터 없음), 최대 168h의 양수 Go 기간; `previous=false`; `grep=""`, 대소문자를 구분하지 않는 리터럴 부분 문자열; 스트림 상한 1 MiB |
| `k8s_events` | 네임스페이스 이벤트 | `namespace=""`(허용된 모든 네임스페이스); `warnings_only=true`; `since="1h"`, 최대 168h의 양수 Go 기간; `limit=50`, 1–500으로 보정 |
| `host_service_restart` | 허용된 systemd unit 재시작 | 필수 `unit`(상태 조회와 같은 정규화 및 256바이트 제한); `restart_units` 조건 |
| `k8s_rollout_restart` | Pod 템플릿을 갱신해 워크로드 재시작 | 필수 `kind`(deployment/statefulset/daemonset), `namespace`, `name`; `rollout_restart` 조건 |
| `k8s_scale` | 워크로드 복제본 수 설정 | 필수 `kind`(deployment/statefulset), `namespace`, `name`, `replicas`(0–`max_replicas`, 기본 상한 10); `scale` 조건 |
| `k8s_delete_pod` | UID 사전 조건으로 특정 Pod 삭제 | 필수 `namespace`, `name`; `delete_pod` 조건 |
| `note_search` | 리터럴 노트 내용/파일명 검색. 발췌문과 검색한 항목 수를 제공합니다. | 필수 `query`(1–4096 UTF-8 바이트); 선택 `folder`, `tag`, `case_sensitive=false`; `limit=20`, 최대 100 |
| `note_read` | 노트 텍스트와 파일/줄 메타데이터 | 필수 `path`, `.md` 생략 가능; `offset_line=1`, `max_lines=400`, 최대 2000 |
| `note_list` | 폴더를 먼저 표시한 뒤 .md/.canvas/.base 파일 표시 | `folder=""`(볼트 루트), `recursive=false`, `limit=200`, 최대 1000; 잘림 여부 보고 |
| `note_recent` | 수정 시간순 노트 | `limit=20`, 최대 100; `days=7`, 최대 365 |
| `note_links` | 나가는 위키 링크/임베드/상대 Markdown 링크와 백링크 | 필수 `path`; 정확한 경로나 유일한 기본 파일명으로 해석; 해석하지 못한 대상 보고 |
| `note_tags` | frontmatter/인라인 태그 수 또는 일치하는 노트 | 선택 `tag`; 중첩 태그 포함; 코드 펜스와 제목 무시 |
| `note_create` | 노트와 없는 상위 폴더 생성 | 필수 `path`, `content`; `allow_write=true`, ops:write, 사람의 승인 필요 |
| `note_append` | 끝부분이나 유일하게 정확히 일치하는 제목 아래에 추가 | 필수 `path`, `content`; 선택 `heading`(최대 1024바이트); 같은 쓰기 조건 |
| `note_replace` | 정확한 부분 문자열 치환 | 필수 `path`, 비어 있지 않은 `old_text`, `new_text`; `expected_count=1`과 일치해야 함; 같은 쓰기 조건 |
| `note_trash` | 충돌 시 접미사를 붙여 로컬 .trash에 노트 보존 | 필수 `path`; 같은 쓰기 조건; 영구 삭제하지 않음 |
| `dev_projects` | 코딩 프로젝트와 에이전트 허용 목록(`dev:run`) | 없음; runner 제한 포함 |
| `dev_task_start` | 사람의 승인 후 에이전트 시작(`dev:run`) | 필수 `project`, `agent`, `prompt`; 선택 `approval_id`; 프롬프트 최대 32 KiB(MCP 인자 48 KiB, 승인 요약 64 KiB) |
| `dev_task_status` | 자신의 작업 상태와 최종 메시지(runner 관리자는 모든 작업에 접근 가능)(`dev:run`) | 필수 `id` |
| `dev_task_log` | 신뢰할 수 없는 작업 로그 페이지(`dev:run`) | 필수 `id`; `offset=0`(0–20 MiB), `limit=65536`, 최대 262144바이트; 원본 기준 `next_offset` |
| `dev_task_diff` | 고정된 기준 대비 신뢰할 수 없는 통계와 패치(`dev:run`) | 필수 `id`; MCP 출력 상한 적용 전 최대 256 KiB |
| `dev_task_list` | 보존된 작업을 최신순으로 표시(`dev:run`) | `limit=20`, 범위 1..100; 최종 메시지 제외 |
| `dev_task_cancel` | 새 승인 없이 소유한 작업 취소(runner 관리자는 모든 작업 취소 가능)(`dev:run`) | 필수 `id`; 상태 변경 및 실행 중인 프로세스 그룹 종료 |

노트 경로는 볼트 기준 상대 경로이며 슬래시를 사용합니다. Unicode 바이트를 보존하며 최대 1024바이트입니다. 제외된 경로는 없는 경로처럼 처리합니다. 노트 텍스트는 신뢰할 수 없는 데이터입니다. 노트 도구의 숫자 입력이 문서의 범위를 벗어나면 거부합니다. 볼트 내부의 안전한 심볼릭 링크를 지원하며 순환을 방지합니다. 검색은 항목 100000개와 디렉터리 깊이 128단계로 제한합니다. 링크와 백링크는 각각 최대 1000개를 반환합니다.

쓰기 미리보기에는 diff(생성 시 새 내용), 현재 해시, 결과 해시를 포함합니다. 약 8 KiB에서 명시적으로 잘림을 표시합니다. 적용 시 승인된 해시, 크기, 수정 시간을 다시 검사합니다. 생성은 동시에 생성하는 다른 작업을 덮어쓰지 않고 원자적으로 공개합니다. 갱신은 동기화한 임시 파일과 rename을 사용합니다. Linux에서는 기존 항목을 덮어쓰지 않고 원본을 승인된 .trash 경로로 원자적으로 이동한 뒤 이동한 노트를 검증합니다. 이동과 동시에 교체나 편집이 발생하면 .trash 내 복구 경로를 포함한 오류를 반환합니다. 필요한 경우 해당 파일을 복원합니다. 계획 후 대상 경로에 다른 항목이 생기면 이동이 실패하며 원본을 그대로 유지합니다. Linux에서는 원본과 .trash가 RENAME_NOREPLACE를 사용하는 renameat2를 지원하는 같은 파일시스템에 있어야 합니다. 이 조건을 충족하지 않으면 원본을 유지한 채 이동이 실패합니다. 다른 플랫폼에서는 승인된 내용의 내구성 있는 복사본을 보존한 뒤 원본을 검사하고 제거합니다. 마지막 검사와 제거 사이에 외부에서 교체한 내용은 여전히 유실될 수 있습니다. 해당 플랫폼에서는 버전별 백업이나 동기화 이력을 사용합니다. 기존의 마지막 줄바꿈 정책을 유지합니다. 각 내용/치환 입력의 상한은 256 KiB입니다. create, append, replace 도구는 이스케이프를 포함한 원시 JSON 인자를 528 KiB까지 허용합니다. trash는 기본 16 KiB 제한을 유지합니다.

갱신은 낙관적 상태 검사 후 rename을 수행합니다. 검사와 rename 사이의 외부 편집을 덮어쓸 수 있습니다. 버전별 백업이나 동기화 이력을 사용합니다. 큰 생성 미리보기도 잘리며, 생략한 줄 수를 명시합니다.

호스트/Kubernetes 읽기 도구의 숫자 제한에서 0은 기본값을 선택합니다. 그 밖의 범위 밖 값은 범위 안으로 보정합니다. 쓰기의 복제본 수는 보정하지 않고 검증합니다. 반환하는 모든 텍스트에는 `server.max_output_bytes`(기본값 65536)가 적용됩니다. 호출에는 협력적 기한(`server.tool_timeout`, 기본값 30s)이 적용됩니다.

Kubernetes 네임스페이스 입력은 DNS-1123 레이블이어야 합니다. 객체의 `name`, `pod`, `container` 입력은 DNS-1123 하위 도메인으로 검증합니다.

`k8s_list`와 `k8s_get`이 지원하는 `kind` 값은 대소문자를 구분하지 않습니다. 괄호 안은 별칭입니다: pods (po, pod), deployments (deploy, deployment), statefulsets (sts, statefulset), daemonsets (ds, daemonset), replicasets (rs, replicaset), jobs (job), cronjobs (cronjob), services (svc, service), ingresses (ing, ingress), configmaps (cm, configmap), persistentvolumeclaims (pvc, persistentvolumeclaim), persistentvolumes (pv, persistentvolume), nodes (no, node), namespaces (ns, namespace).

Nodes, PersistentVolumes, Namespaces는 클러스터 범위이므로 `namespace`를 생략합니다. 네임스페이스 허용/거부 규칙은 Namespace 객체를 이름으로 필터링합니다. Nodes나 PersistentVolumes는 제한하지 않으며, `k8s_overview`에 표시하는 노드도 마찬가지입니다. 네임스페이스 범위 리소스와 이벤트는 네임스페이스로 필터링합니다. 허용 목록이 비어 있으면 아무것도 허용하지 않습니다. 클러스터 범위 접근은 RBAC으로 제한합니다.

[읽기 전용 RBAC](../../deploy/kubernetes/rbac-readonly.yaml)을 출발점으로 삼고 배포 전에 실효 권한을 검토합니다. 이 매니페스트는 기존 `opsgate` 네임스페이스에 ServiceAccount를 만들고, ClusterRole과 ClusterRoleBinding을 만듭니다. Secrets를 제외한 지원 리소스, 이벤트, Pod 로그에 클러스터 전체 get/list/watch 권한을 부여합니다. 더 좁은 접근에는 네임스페이스 RoleBinding을 사용합니다.

kubeconfig의 `exec` 및 `auth-provider` 자격 증명 플러그인은 거부됩니다. 수명이 짧은 ServiceAccount 토큰처럼 플러그인이 필요 없는 자격 증명을 사용합니다. kubeconfig의 소유권과 권한을 보호합니다.

<a id="redaction-and-object-sanitization"></a>

## 마스킹과 객체 정리

공유 `internal/redact` 계층은 PEM 개인 키, Authorization/Proxy-Authorization 헤더, Basic/Bearer 자격 증명, JWT 형태의 토큰, URL 비밀번호를 마스킹합니다. AWS 액세스 키 ID, GitHub, GitLab, Slack, Stripe live 키, OpenAI/Anthropic 형식 키, Google API 키, Telegram 봇의 알려진 토큰 패턴도 마스킹합니다.

민감한 `:`/`=` 할당과 구조화된 인자 키에는 password/passwd/pwd, secret/client_secret, token/access_token/refresh_token, api_key/apikey/access_key/secret_key/private_key, credential/credentials, auth_token, session_id, cookie, set-cookie가 포함됩니다. `db_password`처럼 밑줄로 연결된 접두사가 있는 변형도 포함하며, 키 일치는 대소문자를 구분하지 않습니다. 구조화된 인자 맵과 슬라이스는 재귀적으로 순회합니다. 성공 텍스트, 오류, 감사 로그 인자를 마스킹합니다. 이는 **최선의 노력** 방식입니다. 알려지지 않은 형식과 임의 데이터에 포함된 시크릿은 빠져나갈 수 있습니다.

`k8s_get`은 템플릿을 포함한 메타데이터에서 `managedFields`와 `kubectl.kubernetes.io/last-applied-configuration`도 제거합니다. ConfigMap의 `data`와 `binaryData`는 키를 유지하되, `show_configmap_data=true`가 아니면 값을 바이트 수 표시로 대체합니다. 환경 변수 이름에 pass, secret, token, key, credential, auth, dsn, private, cert, database_url이 포함되면 리터럴 `value` 필드를 `[REDACTED]`로 대체합니다. 대소문자는 구분하지 않습니다. Pods와 지원하는 워크로드 템플릿(CronJobs 포함)의 일반/init/ephemeral 컨테이너에 적용합니다. 다른 리터럴 값은 공유 마스킹에 일치하지 않으면 그대로 보입니다. `valueFrom` 참조도 그대로 보이며 값을 조회하지 않습니다.

<a id="shared-limits"></a>

## 공통 제한

승인이 필요한 도구 9개는 모두 웹 재시도용 선택적 `approval_id`를 받습니다. 첫 호출에서는 생략합니다. dev 작업 ID는 소문자 base32 문자 20개입니다. 작업 상태, 로그, diff, 목록, 취소는 요청자의 소유권을 검사합니다. runner 관리자는 모든 작업에 접근할 수 있습니다.

읽기 도구와 `dev_task_cancel`은 원시 JSON 인자를 최대 16 KiB까지 받습니다. 쓰기 인자의 기본 상한은 16 KiB입니다. `note_create`, `note_append`, `note_replace`는 528 KiB, `dev_task_start`는 48 KiB를 허용합니다. JSON 이스케이프도 이 제한에 포함됩니다. 승인 요약의 상한은 `max(64 KiB, argument limit + 16 KiB)`입니다. 기본 도구와 dev 시작은 64 KiB, 노트 내용 도구는 544 KiB입니다. 마스킹한 표시용 인자도 해당 도구의 인자 제한 안에 들어야 합니다. 노트 미리보기에는 별도로 약 8 KiB의 표시 제한이 있습니다.

공유 래퍼는 4 MiB를 초과하는 provider 출력을 통째로 생략합니다. 그 밖의 텍스트는 마스킹한 뒤 `server.max_output_bytes`(기본값 64 KiB)에 맞춰 자릅니다. 오류도 같은 경로로 처리합니다. 더 작은 provider별 수집 제한도 적용됩니다. 기한은 협력적입니다. 쓰기 계획과 적용에는 각각 별도의 `server.tool_timeout`(기본값 30s)이 주어지며, 사람의 응답을 기다리는 시간은 제외합니다.

호스트 명령을 취소하면 프로세스 그룹에 SIGKILL을 보내며, 실패하면 직접 실행한 자식 프로세스의 종료를 시도합니다. 취소되거나 직접 실행한 자식이 종료된 뒤에는 출력 수집을 최대 250ms 동안 기다리므로, 하위 프로세스가 출력 파이프를 열어 두어도 호출이 멈추지 않습니다. 자식이 성공적으로 종료해도 이 제한을 넘겨 출력 파이프가 열려 있으면 오류로 보고합니다. sudo 같은 권한 상승 도우미나 프로세스 그룹을 벗어난 하위 프로세스는 취소 후에도 남을 수 있지만, 출력 대기 시간은 계속 제한됩니다.
