# 호스트 provider

[English](../host.md)

선택 기능인 호스트 provider는 Kubernetes가 없는 서버에서도 opsgate의 흐름(알림, 선택지,
승인, 실행)을 쓰게 해 줍니다. 대상은 opsgate가 실행되는 서버의 systemd 유닛과 Docker
또는 Compose 컨테이너입니다. 기본값은 꺼짐이며, 직접 켜고 볼 수 있는 유닛이나 컨테이너를
적기 전에는 아무 도구도 등록하지 않습니다.

평소 SSH로 접속해 `systemctl restart`나 `docker restart`를 실행하던 VPS나 집 서버에
쓰세요. Kubernetes 파드 안에서는 켜지 마세요. `mode: kubernetes`는 이 설정을 거부하며,
그래도 파드 안에서 실행되면 opsgate가 경고를 남깁니다.

## 설정

```yaml
providers:
  host:
    enabled: true
    poll_interval: 30s            # opsgate watch 조회 주기, 10s–5m
    systemd:
      units: ["app-*.service", nginx.service]   # 볼 수 있는 유닛(path.Match 패턴)
      restart: ["app-*.service"]                # 재시작할 수 있는 유닛
    docker:
      endpoint: unix:///run/docker-proxy/docker.sock   # 또는 http://127.0.0.1:2375
      containers: [web, "compose:shop/*"]       # 이름 또는 compose:<project>/<service>
      restart: ["compose:shop/*"]
```

- 재시작하려면 대상이 볼 수 있는 목록과 재시작 목록에 모두 있어야 합니다.
- `compose:<project>/<service>`는 Compose가 붙이는 `com.docker.compose.project`,
  `com.docker.compose.service` 라벨과 비교합니다.
- Docker endpoint는 unix 소켓이나 루프백 주소의 일반 HTTP여야 합니다. Docker 소켓이 아니라
  소켓 프록시를 가리키게 하세요(아래 참고).
- `mode: kubernetes`와 `providers.host.enabled: true`를 함께 쓰면 설정 오류입니다.

## 도구

| 도구 | 종류 | 하는 일 |
| --- | --- | --- |
| `host_units` | 조회 | 볼 수 있는 유닛의 로드·활성·하위 상태, 마지막 결과, 재시작 횟수, 상태가 바뀐 시각 |
| `host_unit_logs` | 조회 | 유닛 하나의 마스킹한 저널 끝부분(`lines`, 기본 100, 최대 500) |
| `host_restart_unit` | 변경 | 승인 후 `systemctl --no-ask-password restart -- <unit>` 실행 |
| `host_containers` | 조회 | 볼 수 있는 컨테이너의 이미지, 상태, health, 종료 코드, 재시작 횟수, Compose 라벨 |
| `host_container_logs` | 조회 | 컨테이너 하나의 마스킹한 로그 끝부분 |
| `host_restart_container` | 변경 | 승인 후 `POST /containers/{id}/restart?t=10`. 실행 계획에 전체 컨테이너 ID를 보여 주고, 그사이 컨테이너가 다시 만들어졌으면 거부 |

두 재시작 모두 카탈로그 조치이므로 링크, 선택지 페이지, analyzer 제안으로 나올 수 있습니다.
되돌리기는 없습니다.

이름은 쓰기 전에 검증합니다. 유닛 이름은 `name.service`(또는 다른 유닛 종류) 형태여야 하고,
컨테이너 이름은 `[A-Za-z0-9][A-Za-z0-9_.-]*` 형태여야 합니다. 명령은 셸을 거치지 않고 고정된
인자로 실행하며, 제한 시간은 15초, 출력 상한은 4 MiB입니다. 로그는 요청한 길이로 자르기 전에
마스킹합니다.

## 선택지와 알림

`/approve/host-options/{unit|container}/{name}`은 Kubernetes 선택지 페이지처럼 로그인 후 현재
선택지를 보여 줍니다.

| 대상 | 제안하는 경우 | 조치 |
| --- | --- | --- |
| systemd 유닛 | 실패했거나 재시작한 적이 있을 때 | `host_restart_unit` |
| Docker 컨테이너 | unhealthy이거나, 재시작 중이거나, 0이 아닌 코드로 종료했거나, 재시작한 적이 있을 때 | `host_restart_container` |

호스트 provider를 켜면 `opsgate watch`가 `poll_interval`마다 조회해, 유닛이 실패하거나
(`UnitFailed`) 재시작할 때(`UnitRestarted`), 컨테이너가 unhealthy가 되거나
(`ContainerUnhealthy`) 오류로 종료하거나(`ContainerExited`) 재시작할 때
(`ContainerRestarted`) 경고를 보냅니다. 첫 조회는 현재 상태만 기록하므로 watch를 시작해도
지난 문제를 다시 알리지 않습니다. 컨테이너가 다시 만들어지면(새 ID) 기록을 새로 시작합니다.

호스트 알림은 cluster와 namespace를 모두 `host`로 씁니다. 그래서
[watch 그룹](watch.md#그룹)에서 `namespaces: [host]`로 보낼 곳을 정할 수 있습니다.
`watch.approval_url`을 설정하면 **조치 선택** 링크가 붙고, `watch.inbox`를 설정하면 파드
경고처럼 승인함과 푸시로도 갑니다. 선택 기능인 analyzer는 아직 호스트 알림을 조사하지 않습니다.
analyzer의 도구 허용 목록에는 Kubernetes와 노트만 있습니다.

## 권한

opsgate는 권한 없는 `opsgate` 계정으로 실행합니다. 호스트 provider에는 root, sudo, 추가
capability가 필요하지 않습니다.

**systemd.** 유닛 상태 조회에는 권한이 필요 없습니다. 저널을 읽으려면 `systemd-journal`
그룹이 필요합니다. 재시작은 systemd의 D-Bus API를 거치고, systemd가 polkit에 허락을 묻습니다.
[`deploy/host/50-opsgate.rules`](../../deploy/host/50-opsgate.rules)를 설치하고 `opsgate`
계정이 재시작할 수 있는 유닛 이름을 정확히 적으세요. 이 규칙은 그 유닛들의 `restart` 동작만
허락합니다. setuid 프로그램을 쓰지 않으므로 서비스는 `NoNewPrivileges=yes`를 유지합니다.
[`deploy/host/opsgate.service`](../../deploy/host/opsgate.service)와
[`opsgate-watch.service`](../../deploy/host/opsgate-watch.service)를 참고하세요.

**Docker.** Docker 소켓에 접근할 수 있으면 사실상 서버의 root입니다. `/`를 마운트한 특권
컨테이너를 띄울 수 있기 때문입니다. opsgate에 Docker 소켓을 직접 주지 마세요. 아래 호출만
전달하는 소켓 프록시를 띄우고, `providers.host.docker.endpoint`가 그 프록시를 가리키게 하세요.

| 메서드 | 경로(앞에 `/v1.xx`가 붙을 수 있음) | 사용처 |
| --- | --- | --- |
| GET | `/containers/json` | 목록 |
| GET | `/containers/{id 또는 이름}/json` | 상세 |
| GET | `/containers/{id}/logs` | `host_container_logs` |
| POST | `/containers/{id}/restart` | `host_restart_container` |

메서드와 경로로 거르는 프록시라면 무엇이든 됩니다. 예를 들어
[wollomatic/socket-proxy](https://github.com/wollomatic/socket-proxy)는 메서드마다 정규식을
하나씩 받습니다. 설치한 버전의 옵션을 확인하세요. 아래는 예시입니다.

```sh
socket-proxy \
  -proxysocketendpoint=/run/docker-proxy/docker.sock -proxysocketendpointfilemode=0660 \
  '-allowGET=^(/v[0-9.]+)?/containers/(json|[A-Za-z0-9][A-Za-z0-9_.-]*/(json|logs))$' \
  '-allowPOST=^(/v[0-9.]+)?/containers/[0-9a-f]{64}/restart$'
```

프록시 소켓은 `opsgate` 계정이 속한 그룹에 주고, 쓰기 전에 프록시를 통한 `docker run`이
거부되는지 확인하세요.

## 보안 참고

- 무엇을 재시작할 수 있는지는 AI나 알림이 아니라 허용 목록이 정합니다. 규칙과 AI 제안은
  카탈로그 안에서 고를 뿐입니다.
- 모든 재시작은 사람이 브라우저에서 승인합니다(웹 승인을 끈 경우 클라이언트에서 승인).
  승인 시간 창은 설정대로 적용됩니다.
- 유닛과 컨테이너 출력은 믿을 수 없는 호스트 데이터입니다. 마스킹하고 길이를 제한하며,
  클라이언트에는 그 안의 지시를 따르지 말라고 안내합니다.
- 인스턴스 하나가 신뢰 그룹 하나입니다. 같은 opsgate가 Kubernetes도 다루면 `ops:read`는
  둘 다에 적용됩니다.
