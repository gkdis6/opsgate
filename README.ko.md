# opsgate

[English](README.md) · [클라이언트 설정](docs/clients.md) · [보안](SECURITY.md)

opsgate는 AI 클라이언트가 Linux 서버를 안전하게 조회하도록 돕는 오픈 소스 Go MCP 서버입니다. 최소 권한, 명시적 허용 목록, 기본 거부가 보안 모델의 핵심입니다. 기본적으로 제공자는 비활성화되며 유닛과 네임스페이스 허용 목록은 비어 있습니다. stdio는 실행한 OS 사용자의 권한으로 작동합니다. 제한된 Kubernetes RBAC와 일반 사용자 계정으로 실행하세요.

v0.1 계약은 셸, 임의 명령 실행, Kubernetes Secrets 접근 없는 읽기 전용 조회입니다. 공통 래퍼가 범위 검사, 협력적 컨텍스트 제한 시간, 최선형 비밀정보 마스킹, 출력 잘림, JSONL 감사를 처리합니다. 로그와 이벤트는 프롬프트 인젝션을 포함할 수 있는 신뢰할 수 없는 데이터입니다. 클라이언트가 악성 지시를 무시한다고 보장할 수는 없습니다.

**개발 상태:** v0.1은 호스트 및 Kubernetes 제공자, 감사 기록, 마스킹을 포함하여 구현 완료된 사전 릴리스입니다. 아직 제3자의 보안 검토를 받지 않았습니다. 운영 환경에 사용하기 전에 구현을 검토하세요. 향후 계획은 [로드맵](docs/roadmap.md)을 참고하세요.

## 빠른 시작

현재 stdio만 지원합니다. 호스트 조회는 systemd/journald 및 /proc가 있는 Linux가 필요합니다. Kubernetes 조회는 client-go와 제한된 자격 증명을 사용합니다.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest
# 또는 소스에서 빌드(Go 1.25 이상):
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

## v0.1 도구 계약

모든 도구에는 `ops:read`가 필요하며 해당 제공자가 활성화된 경우에만 등록됩니다. 호스트 유닛과 Kubernetes 네임스페이스는 설정으로 제한하며 네임스페이스 거부가 우선합니다. Kubernetes Secrets 도구는 없습니다. 읽기 전용 메타데이터와 로그에도 민감한 정보가 있을 수 있습니다.

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

숫자 제한에서 0은 기본값을 선택하며 그 외 범위를 벗어난 값은 상하한으로 조정됩니다. 모든 반환 텍스트에는 `server.max_output_bytes`(기본 65536)가 적용되며 호출에는 협력적 제한 시간(`server.tool_timeout`, 기본 30s)이 적용됩니다.

Kubernetes 네임스페이스 입력은 DNS-1123 레이블이어야 합니다. 객체 `name`, `pod`, `container` 입력은 DNS-1123 서브도메인으로 검증합니다.

`k8s_list`와 `k8s_get`의 지원 `kind` 값은 대소문자를 구분하지 않습니다(괄호 안은 별칭): pods (po, pod), deployments (deploy, deployment), statefulsets (sts, statefulset), daemonsets (ds, daemonset), replicasets (rs, replicaset), jobs (job), cronjobs (cronjob), services (svc, service), ingresses (ing, ingress), configmaps (cm, configmap), persistentvolumeclaims (pvc, persistentvolumeclaim), persistentvolumes (pv, persistentvolume), nodes (no, node), namespaces (ns, namespace).

Nodes, PersistentVolumes, Namespaces는 클러스터 범위이므로 `namespace`를 생략하세요. 네임스페이스 허용/거부 규칙은 Namespace 객체의 이름을 필터링하지만 Nodes와 PersistentVolumes는 제한하지 않습니다. `k8s_overview`에 표시되는 노드에도 동일하게 적용됩니다. 네임스페이스 범위 리소스와 이벤트는 네임스페이스로 필터링하며 빈 허용 목록은 모두 차단합니다. 클러스터 범위 접근은 RBAC로 제한하세요.

[읽기 전용 RBAC](deploy/kubernetes/rbac-readonly.yaml)를 출발점으로 사용하고 배포 전에 유효 권한을 검토하세요. 기존 `opsgate` 네임스페이스에 ServiceAccount를 만들고 ClusterRole과 ClusterRoleBinding으로 지원 리소스, 이벤트, 파드 로그에 클러스터 전체 get/list/watch 권한을 부여하며 Secrets는 제외합니다. 더 좁은 접근 범위에는 네임스페이스 RoleBinding을 사용하세요.

Kubeconfig의 `exec` 및 `auth-provider` 자격 증명 플러그인은 거부됩니다. 수명이 짧은 ServiceAccount 토큰처럼 플러그인이 필요 없는 자격 증명을 사용하세요. kubeconfig 소유권과 권한을 보호하세요.

## 마스킹 및 객체 정제

공통 `internal/redact` 계층은 PEM 개인 키, Authorization/Proxy-Authorization 헤더, Basic/Bearer 자격 증명, JWT 형태 토큰, URL 비밀번호와 알려진 토큰 패턴을 마스킹합니다. 토큰 패턴에는 AWS 액세스 키 ID, GitHub, GitLab, Slack, Stripe 라이브 키, OpenAI/Anthropic 형태 키, Google API 키, Telegram 봇이 포함됩니다. 민감한 `:`/`=` 할당과 구조화된 인수 키에는 password/passwd/pwd, secret/client_secret, token/access_token/refresh_token, api_key/apikey/access_key/secret_key/private_key, credential/credentials, auth_token, session_id, cookie, set-cookie와 `db_password` 같은 밑줄 접두사 변형이 포함됩니다(키는 대소문자를 구분하지 않음). 구조화된 인수 맵과 슬라이스는 재귀적으로 순회합니다. 성공 텍스트, 오류, 감사 인수에 마스킹이 적용됩니다. 이는 **최선형** 보호입니다. 알 수 없는 형식이나 임의 데이터에 포함된 비밀정보는 누출될 수 있습니다.

`k8s_get`은 추가로 템플릿을 포함한 메타데이터에서 `managedFields`와 `kubectl.kubernetes.io/last-applied-configuration`을 제거합니다. ConfigMap `data`와 `binaryData`는 `show_configmap_data=true`가 아니면 키를 유지하고 값을 바이트 수 표시로 바꿉니다. 환경 변수 이름에 pass, secret, token, key, credential, auth, dsn, private, cert, database_url이 포함되면(대소문자 무시) 리터럴 환경 `value` 필드를 `[REDACTED]`로 바꿉니다. Pods와 CronJobs를 포함한 지원 워크로드 템플릿의 일반, init, ephemeral 컨테이너에 적용됩니다. 다른 리터럴 값은 공통 마스킹 패턴에 해당하지 않으면 표시됩니다. `valueFrom` 참조는 표시되며 실제 값을 조회하지 않습니다.

## 설정 참조

아래 기본값은 `config.Default()`에서 가져왔습니다. YAML은 기본값 위에 적용되며 알 수 없는 키는 오류입니다. 상위 매핑은 `server`, `stdio`, `providers`, `providers.host`, `providers.kubernetes`입니다. 빈 슬라이스는 `[]`로 표시합니다(Go에서는 일부가 nil).

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

로그 제한은 제공자별 상한이므로 양수로 설정하세요. 현재 설정 검증은 출력 크기와 제한 시간의 양수 여부, 범위 이름을 확인합니다. `stdio.user`는 설정된 이름이며 인증 자격 증명이 아닙니다. `ops:write`를 부여해도 쓰기 도구나 확인 기능이 구현되지는 않습니다.

## 감사 형식

공통 래퍼는 처리한 도구 호출마다 JSON 객체 한 줄을 `server.audit_log`에 추가합니다. 빈 경로이면 stderr로 보냅니다. 새 파일은 0600 권한으로 생성되며(umask 적용) 기존 파일 권한은 유지됩니다. 심볼릭 링크, 일반 파일이 아닌 파일, 그룹/전체 쓰기 권한이 있는 파일은 거부합니다. stdout은 MCP 전용입니다.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"host_service_status","args":{"unit":"ssh.service"},"outcome":"ok","duration_ms":12,"output_bytes":120}
```

`audit.Event`의 필드: `time`(선택적 소수 초를 포함하는 RFC3339 타임스탬프), `user`, `transport`, `tool`, 선택적 마스킹된 `args`, `outcome`(`ok`, `error`, `denied`), 선택적 마스킹된 `error`, `duration_ms`, `output_bytes`(성공 반환 텍스트 크기, 오류 시 0). 도구 출력 본문은 저장하지 않습니다. 기록 실패는 도구 호출을 실패시키지 않고 stderr로 보고하며 감사는 위변조 방지를 보장하지 않습니다. 민감한 정보가 남을 수 있으므로 로그를 보호하고 순환 관리하세요.

## 개발 및 라이선스

[기여 안내](CONTRIBUTING.md)를 참고하세요. [Apache-2.0](LICENSE), copyright 2026 The opsgate Authors.
