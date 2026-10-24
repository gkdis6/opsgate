# opsgate

[English](README.md) · [클라이언트 설정](docs/clients.md) · [보안](SECURITY.md)

opsgate는 AI 클라이언트가 Linux 서버를 안전하게 조회하도록 돕는 오픈 소스 Go MCP 서버입니다. 최소 권한, 명시적 허용 목록, 기본 거부가 보안 모델의 핵심입니다. 기본적으로 제공자는 비활성화되며 유닛과 네임스페이스 허용 목록은 비어 있습니다. stdio는 실행한 OS 사용자의 권한으로 작동합니다. 제한된 Kubernetes RBAC와 일반 사용자 계정으로 실행하세요.

v0.1 계약은 셸, 임의 명령 실행, Kubernetes Secrets 접근 없는 읽기 전용 조회입니다. 공통 래퍼가 범위 검사, 협력적 컨텍스트 제한 시간, 최선형 비밀정보 마스킹, 출력 잘림, JSONL 감사를 처리합니다. 로그와 이벤트는 프롬프트 인젝션을 포함할 수 있는 신뢰할 수 없는 데이터입니다. 클라이언트가 악성 지시를 무시한다고 보장할 수는 없습니다.

**개발 상태:** v0.1 개발 중입니다. 이 체크아웃에서 제공자 등록, 감사 기록, 마스킹은 TODO 스텁입니다. 아래 표는 스텁의 실제 기능이 아닌 v0.1 계약입니다. 이 스냅샷의 보안 기능에 의존하여 운영 환경에 배포하지 마세요. 향후 계획은 [로드맵](docs/roadmap.md)을 참고하세요.

## 빠른 시작

현재 stdio만 지원합니다. 호스트 조회는 systemd/journald 및 /proc가 있는 Linux가 필요합니다. Kubernetes 조회는 client-go와 제한된 자격 증명을 사용합니다.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest
# Or build from a checkout (Go 1.25+):
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

모든 도구에는 `ops:read`가 필요합니다. 호스트 유닛과 Kubernetes 네임스페이스는 설정으로 제한하며 네임스페이스 거부가 우선합니다. ConfigMap 값은 기본적으로 숨기며 민감한 환경 변수 값은 마스킹하도록 설계됩니다. Secrets 도구는 없습니다. 읽기 전용 메타데이터와 로그에도 민감한 정보가 있을 수 있습니다.

| 도구 | 용도 |
| --- | --- |
| `host_overview` | 호스트 지표 및 설정된 디스크 경로 |
| `host_services` | 허용된 systemd 유닛 |
| `host_service_status` | 허용된 유닛 상태 |
| `host_logs` | 허용된 journald 로그 |
| `host_top_processes` | /proc 프로세스 사용량 |
| `k8s_overview` | 허용된 네임스페이스 개요 |
| `k8s_list` | 지원 리소스 목록 |
| `k8s_get` | 지원 리소스 조회, Secrets 제외 |
| `k8s_logs` | 활성화된 경우 파드 로그 |
| `k8s_events` | 네임스페이스 이벤트 |

[읽기 전용 RBAC](deploy/kubernetes/rbac-readonly.yaml)를 출발점으로 사용하고 배포 전에 유효 권한을 검토하세요. 참조된 매니페스트는 이 체크아웃에 아직 없습니다.

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

감사 계약은 호출당 JSON 객체 한 줄을 `server.audit_log`에 추가하는 것입니다. 빈 경로이면 stderr로 보냅니다. 새 파일은 0600 권한으로 생성하도록 설계됩니다. stdout은 MCP 전용입니다. 이 체크아웃의 감사 기록은 현재 스텁입니다.

```json
{"time":"2026-10-24T12:00:00Z","user":"local","transport":"stdio","tool":"host_overview","outcome":"ok","duration_ms":12,"output_bytes":120}
```

필드: `time`(RFC3339), `user`, `transport`, `tool`, 선택적 마스킹된 `args`, `outcome`(`ok`, `error`, `denied`), 선택적 마스킹된 `error`, `duration_ms`, `output_bytes`(성공 출력 크기, 오류 시 0). 도구 출력 본문은 저장하지 않습니다. 기록 실패는 stderr로 보고하도록 설계되며 위변조 방지를 보장하지 않습니다. 민감한 정보가 남을 수 있으므로 로그를 보호하고 순환 관리하세요.

## 개발 및 라이선스

[기여 안내](CONTRIBUTING.md)를 참고하세요. [Apache-2.0](LICENSE), copyright 2026 The opsgate Authors.
