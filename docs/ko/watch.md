# Kubernetes watch 알림

[English](../watch.md)

`opsgate watch`는 MCP 서버와 따로 계속 실행되는 프로세스입니다. Kubernetes를 watch로 지켜보다가 문제나 변경이 생기면 webhook이나 SMTP 이메일로 알림을 보냅니다. 두 채널을 함께 쓸 수도 있습니다. 예를 들어 webhook은 n8n 워크플로우로 받아 채팅으로 전달할 수 있습니다. MCP 클라이언트를 받지 않고 도구도 없습니다. 사람이 알림을 보고 나면 AI 클라이언트가 기존 조회 도구로 원인을 살핍니다. MCP 서버와 분리되어 있어 stdio 사용자도 쓸 수 있고, 별도 계정과 kubeconfig로 실행할 수 있습니다.

```sh
opsgate watch -config /etc/opsgate/opsgate.yaml
```

클러스터와 네임스페이스 정책은 `providers.kubernetes`에서, 전송 설정은 `watch`에서 읽습니다.

```yaml
watch:
  outputs:
    - name: webhook
      type: webhook
      url: https://n8n.example.com/webhook/opsgate-watch
      secret_file: /etc/opsgate/watch-secret  # 선택. HMAC 키, 16바이트 이상
      allow_private_http: false
  email:                           # 선택. host를 설정하면 활성화됩니다
    host: smtp.example.com
    port: 587                      # 기본값. 465는 연결 즉시 TLS를 사용합니다
    username: alerts@example.com   # 선택. password_file과 함께 설정합니다
    password_file: /etc/opsgate/smtp-password
    from: alerts@example.com
    to: [me@example.com]
    batch: 5m                      # 기본값. 1m–1h
    problems_only: false           # true면 이메일로는 경고만 보냅니다
  clusters: []          # 기본값: 설정된 모든 클러스터
  problems_only: false  # true면 경고만 보냅니다
  batch: 30s            # 이 시간 동안 모은 알림을 한 번에 보냅니다 (1s–10m)
  cooldown: 15m         # 같은 객체의 같은 경고는 이 시간 동안 다시 보내지 않고 횟수만 셉니다 (0–24h)
  max_alerts: 50        # 한 번에 보내는 최대 알림 수 (1–500). 넘치면 dropped로 셉니다
```

각 출력의 `url`은 https이거나 루프백 주소로 가는 http여야 하고, 자격 증명이나 프래그먼트를 넣을 수 없습니다. 출력에 `allow_private_http: true`로 두면 Service 이름(`http://n8n.n8n.svc.cluster.local:5678/...`)이나 사설 IP 주소처럼 사설 네트워크로 가는 http도 허용합니다. 이때 모든 연결은 DNS 조회 후 사설 주소나 루프백 주소에 닿아야 하고, 검사한 주소가 실제 수신 측 주소가 되도록 프록시 설정은 무시합니다. 공인 IP를 직접 적으면 여전히 거부합니다. 알림은 그 네트워크를 암호화 없이 지나갑니다. URL 쿼리에 비밀 키를 넣어도 로그에는 남지 않습니다.

`watch.webhook_url`, `watch.secret_file`, `watch.allow_private_http`는 제거되었습니다. 이 키가 있으면 설정을 거부합니다. `watch.outputs`의 `type: webhook` 항목으로 옮기고 `url`, `secret_file`, `allow_private_http`를 대상별로 설정하세요. 기본 웹훅 JSON 본문과 HMAC 서명은 유지됩니다.

## 호스트 알림

선택 기능인 [호스트 provider](host.md)를 켜면 `opsgate watch`가 systemd 유닛과 Docker
컨테이너도 조회해 `UnitFailed`, `UnitRestarted`, `ContainerUnhealthy`, `ContainerExited`,
`ContainerRestarted` 경고를 보냅니다. cluster와 namespace는 `host`이므로 그룹에서는 `host`나
`*`로 맞춥니다. 호스트 provider를 켜면 watch에 Kubernetes가 없어도 됩니다.

## 그룹

`watch.groups`로 알림의 namespace에 따라 수신 출력, 경고 필터와 묶음 주기를
정합니다. 아래 예시는 기존 `watch.outputs`의 `discord` 항목과 설정된
`watch.inbox`, `watch.email`을 참조합니다.

```yaml
watch:
  groups:
    - name: customer
      namespaces: [bid, chagok, "shop-*"]
      outputs: [discord, inbox]
      problems_only: true
      batch: 1s
    - name: rest
      namespaces: ["*"]
      outputs: [email]
      problems_only: false
      batch: 15m
```

전체 namespace를 Go `path.Match` glob으로 비교해 **처음 일치한 그룹**을
선택합니다. 대소문자를 구분하고 `*`는 `/`를 넘지 않습니다. 필터로 제외된
알림은 다음 그룹으로 넘어가지 않습니다. Node 등 namespace가 없는 알림은
리터럴 패턴 `""` 또는 `"*"`에만 일치하며, `"**"` 등 다른 패턴에는 일치하지
않습니다. 모든 namespace와 클러스터 범위 알림을 받으려면 마지막에 `"*"` 그룹을
둡니다. 그룹은 이미 허용된 알림만 분배하며 provider namespace 정책을 넓히지 않습니다.

`groups`를 생략하면 기존처럼 모든 출력이 각자의 필터와 주기로 알림을 받습니다.
명시적인 빈 목록은 거부합니다. 그룹을 설정하면 선택된 출력에만 전송합니다.
어느 그룹에도 일치하지 않는 알림은 한 번만 누락으로 집계하며, 기존 `dropped`
요약 형식을 사용해 1분마다 그리고 종료 시 로컬 `watch unmatched` 로그로 남깁니다.
이 요약에는 개수만 담고 알림 내용은 어떤 출력에도 보내지 않습니다.

`watch.problems_only`는 계속 알림 생성 자체를 제한합니다. 그룹과 출력의
`problems_only`는 추가 필터로, 하나라도 `true`면 정보 알림을 제외합니다.
승인함은 여전히 선택지 링크가 있는 파드 경고만 받습니다. 그룹은 이 조건이나
서명된 notice 스키마를 바꾸지 않습니다.

그룹의 batch는 선택한 출력의 주기를 대체합니다. Webhook/Discord와 승인함은
**1s–10m**, 이메일은 **1m–1h**입니다. 이메일과 webhook/승인함을 함께 선택하면
**1m–10m**를 사용해야 합니다. 가장 빠른 주기는 `1s`(이메일 `1m`)이며 0은
허용하지 않습니다. 그룹이 없으면 승인함은 기존 고정 1s를 유지합니다.
각 `(그룹, 출력)`은 별도 묶음, cooldown 상태, 크기가 제한된 큐와 재시도를
사용하므로 한 출력이나 그룹의 실패가 다른 전송을 지연시키지 않습니다.
`watch.cooldown`, `watch.max_alerts`를 계속 사용하며 승인함은 묶음당 최대 50개입니다.
그룹의 cooldown 캐시는 쌍마다 경고 키 1000개까지 보관하며, 가득 차면 새 키는
공간이 생길 때까지 cooldown 추적 없이 전송합니다. 같은 출력으로 가는 다른 그룹도
항상 별도 묶음으로 보냅니다. Discord 제목과 이메일 제목에 그룹 이름이 표시되고,
기본 webhook JSON에는 `group` 필드가 추가됩니다. 커스텀 템플릿은
`{{json .Group}}`을 사용할 수 있습니다. 그룹이 없으면 필드도 생략합니다.

그룹은 1–50개이며 이름은 중복 없이 ASCII 영문·숫자·`_`·`-` 1–64자로 정합니다.
각 그룹에는 유효한 namespace 패턴 1–100개(각 최대 253바이트), 중복 없는 출력
이름 1–22개, 명시적인 유효 batch 주기가 필요합니다. 출력 이름은 `watch.outputs`에
있어야 하며, `email`과 `inbox`는 활성화된 전용 설정만 가리키는 예약 이름입니다.
그룹 사용 시 이 이름을 일반 출력에 쓸 수 없습니다. 존재하지 않거나 비활성인 출력,
중복 이름, 잘못된 glob과 범위를 벗어난 주기는 시작 시 거부합니다.

## 이메일 전송

Webhook과 이메일은 독립된 채널입니다. 둘 중 하나만 설정하거나 둘 다 설정할 수 있습니다. 각 채널이 알림 묶음, 쿨다운 상태, 재시도를 따로 관리하므로 한쪽이 느리거나 실패해도 다른 쪽 전송은 지연되지 않습니다. 두 채널 모두 `watch.cooldown`과 `watch.max_alerts`를 사용합니다. Webhook 전송 간격은 `watch.batch`, 이메일 전송 간격은 `watch.email.batch`로 정합니다. 이메일의 기본값은 `5m`이며 `1m`부터 `1h`까지 허용합니다.

`watch.email.problems_only: true`는 이메일에서만 정보 알림을 제외합니다. 반면 `watch.problems_only: true`는 watcher가 정보 알림을 만들지 않게 하므로 모든 채널에 적용됩니다.

TLS는 필수이며, `email.host`를 기준으로 서버 인증서를 검증합니다. TLS 1.2 이상만 사용합니다. 포트 `465`에서는 연결 즉시 TLS를 사용하고, 그 밖의 모든 포트에서는 STARTTLS가 필요합니다. 서버가 STARTTLS를 지원하지 않거나 거부하면 전송을 중단합니다. TLS 연결이 성립하기 전에는 자격 증명이나 메시지 내용을 보내지 않습니다. TLS나 인증서 검증을 끄는 옵션은 없습니다.

인증이 필요하면 `username`과 `password_file`을 함께 설정하세요. 인증 없는 릴레이에서는 둘 다 생략합니다. 비밀번호 파일은 시작할 때 읽고 앞뒤 공백을 제거합니다. 파일을 읽을 수 없거나 내용이 비어 있으면 시작하지 않습니다. 앱 비밀번호처럼 별도로 폐기할 수 있는 전용 자격 증명을 사용하세요. Kubernetes에서는 Secret으로 마운트할 수 있습니다. `deploy/kubernetes/opsgate-watch.yaml`의 주석 처리된 예제를 참고하세요.

`host`에는 공백, 스킴, 포트, 슬래시 없이 호스트 이름만 적습니다. `port`의 기본값은 `587`이며 1–65535 범위를 허용합니다. `from`과 `to`에는 표시 이름이나 줄바꿈 없이 이메일 주소만 적어야 합니다. 수신자는 1–20명까지 지정할 수 있습니다. 다른 이메일 항목을 설정하려면 `host`가 필요합니다.

이메일은 일반 텍스트이며 본문은 quoted-printable로 인코딩합니다. Webhook의 `text`와 같은 내용과 누락된 알림 수를 담습니다. 알림이 하나면 그 내용을 제목으로 사용하고, 여러 개면 경고 수, 변경 수, 정렬된 클러스터 이름을 제목에 넣습니다. 제목에서는 제어 문자를 제거하고 유니코드 문자 120자에서 자른 뒤 MIME 인코딩을 적용합니다. HTML이나 첨부 파일은 보내지 않습니다.

네트워크 오류, TLS 핸드셰이크 오류, SMTP 4xx 응답은 1초와 4초 뒤 두 번 재시도합니다. 비밀번호 거부를 포함한 SMTP 5xx 응답과 STARTTLS 거부는 재시도하지 않습니다. 한 번의 전송에는 최대 30초를 허용하며, 컨텍스트 기한이 더 짧으면 그 기한을 따릅니다. 알림은 저장하지 않습니다. Webhook과 마찬가지로 재시도 후에도 전송하지 못한 알림은 유실되며 로그에 남깁니다. 종료할 때는 각 채널의 종료 제한 시간 안에서 대기 중인 알림을 보냅니다.

네임스페이스·파드·컨테이너 이름과 민감 정보를 마스킹한 요약이 클러스터 외부의 메일 제공업체를 거칩니다. 이메일을 활성화하기 전에 해당 클러스터의 정보 처리 정책에 맞는지 확인하세요.

최대 발송량은 묶음 간격마다 메시지 한 통입니다. `5m`이면 하루 최대 288통, `15m`이면 최대 96통을 보낼 수 있습니다. 특히 다른 서비스와 계정을 함께 쓰는 경우 계정의 발송 한도를 확인하세요.

## 알리는 내용

watcher가 시작할 때 이미 있던 객체는 기준점일 뿐 알리지 않습니다. 그 뒤에 일어난 상태 변화만 알립니다. 파드의 생성과 삭제는 알리지 않습니다.

| 종류 | 경고 | 정보 (`problems_only`면 생략) |
| --- | --- | --- |
| Pod | 컨테이너 재시작(마지막 종료 사유와 종료 코드 포함: `OOMKilled`, `Error` 등), `ImagePullBackOff`·`ErrImagePull`·`InvalidImageName`·`CreateContainerConfigError`·`CreateContainerError`·`RunContainerError` 대기, `Failed` 단계 진입(`Evicted` 등), 스케줄 불가 | |
| Deployment | 롤아웃 정체(`ProgressDeadlineExceeded`) | 이미지 변경, 복제본 수 변경, 생성, 삭제 |
| StatefulSet, DaemonSet | | 이미지 변경, 복제본 수 변경(StatefulSet), 생성, 삭제 |
| Node | Ready가 `False`나 `Unknown`이 됨, 메모리·디스크·PID 압박이나 네트워크 불가가 `True`가 됨 | Ready 복구, cordon, uncordon, 노드 추가, 노드 제거 |
| ConfigMap | | 바뀐 키·추가된 키·삭제된 키 이름, 생성, 삭제 |

`CrashLoopBackOff`는 그 앞의 재시작 알림으로 이미 알 수 있어 따로 보내지 않습니다. ConfigMap 알림에는 키 이름만 들어갑니다. watch 캐시에는 값 대신 값의 다이제스트만 저장하므로 값은 절대 전송되지 않습니다.

네임스페이스 허용·거부 규칙은 도구와 똑같이 적용됩니다. 네임스페이스를 정확한 이름으로 적으면 그 네임스페이스만 각각 watch합니다. 글롭 패턴(`*` 포함)이 하나라도 있으면 클러스터 전체를 watch하고 로컬에서 거르므로, 나중에 생긴 네임스페이스도 포함됩니다. 이때는 클러스터 전체의 list/watch RBAC 권한이 필요합니다. 노드는 항상 watch합니다.

### 힌트와 재시작 제안

경고에는 `hint`가 붙습니다. 경고 종류에 따라 고른 고정 문구이며, 클러스터에서 온 텍스트는 들어가지 않습니다. 삭제되는 중에 실패한 파드는 알리지 않습니다.

호환성을 위해 다음 조건을 모두 만족하면 `restart`에 워크로드 이름도 넣습니다.

- 컨테이너가 3회 이상 재시작했습니다.
- 종료 사유가 `OOMKilled`, `Unknown`, `ContainerStatusUnknown`, `Completed`가 아닙니다.
- 파드가 Job이 아니라 Deployment, StatefulSet, DaemonSet에 속합니다.
- watch 설정의 `providers.kubernetes.rollout_restart`에 그 워크로드가 있습니다. 서버와 같은 목록을 쓰세요.

watch는 제안만 합니다. 쓰기 권한이 없고, 제안을 자동으로 실행하는 구성요소도 없습니다. 사람이 MCP 클라이언트에 재시작을 요청하면, `k8s_rollout_restart`가 허용 목록을 다시 확인하고 평소처럼 승인을 요구합니다.

재시작 제안은 위 조건을 만족한 뒤 처음 보내는 알림에 붙습니다. 쿨다운이 반복 알림을 막으므로, 계속 죽는 파드라면 보통 첫 알림에서 쿨다운 시간쯤 지난 뒤에 나타납니다.

`watch.approval_url`을 설정하면 `restart.url`에 서버의 승인 페이지 링크도 넣으므로, 채팅 버튼으로 열 수 있습니다. 링크에 비밀 값은 없습니다. 링크를 열려면 브라우저 로그인이 필요하고, MCP 경로와 같은 스코프·도구 규칙을 적용하며, 최신 계획을 보여 줍니다. 승인을 눌러야만 워크로드를 한 번 재시작하고, 그사이 계획이 바뀌면 실행하지 않습니다. 승인 창(window)은 이 경로에 적용되지 않습니다. 서버가 내장 로그인과 웹 승인을 써야 합니다. 새 재시작 URL은 공통 카탈로그 빌더의
`/approve/action/k8s_rollout_restart?...&source=rule` 형식입니다. 영어 승인
화면에는 “Restart workload”와 “Suggested by: rule”을 표시합니다. 기존 재시작
링크도 별칭으로 동작합니다. 다른 카탈로그 조치는 `action_link`와 분석기로
제안할 수 있습니다. 기존 재시작 조건은 보수적으로 유지하며, 현재 선택지 페이지에서는
시간만 벌 수 있다는 경고와 함께 반복 OOM 재시작도 제공합니다.

`watch.approval_url`이 있으면 허용된 namespace의 **파드 경고** 중 소유자가
Deployment·StatefulSet·DaemonSet으로 해석되는 알림에 `options_url`을 추가합니다.
**조치 선택** / **Choose an action** 링크는
`/approve/options/{cluster}/{namespace}/{pod}?workload=kind/name` 형식입니다.
정보 알림, 소유자 없는 파드, 지원하지 않는 소유자에는 붙이지 않습니다. 재시작 허용
목록과 일치할 필요는 없습니다. 로그인 후 서버가 현재 클러스터 상태에서 활성화되고
허용된 조치를 계산합니다. watcher가 넘긴 워크로드는 이동용 힌트일 뿐이며 서버가
현재 소유 관계를 UID로 재검증합니다. 이미지 받기 실패에는 이미지 복구, 실패 중인
새 배포에는 복구 우선 후 재시작, 반복 OOM에는 임시 재시작, 멈췄거나 연결할 수 없는
컨트롤러 소유 파드에는 삭제를 제공할 수 있습니다. [전체 규칙](approval.md#장애별-현재-선택지).

Discord embed, 웹훅 JSON·텍스트, 이메일 텍스트가 같은 선택지 링크를 출력별 언어로
표시합니다. 외부 n8n/Telegram 수신기를 위해 기존 `restart`, `restart.url`은 유지하고
`options_url`, `workload`를 추가합니다. 사용자 웹훅 템플릿에서는 알림의 `.OptionsURL`을
쓸 수 있습니다. 링크에는 비밀 값이 없고 승인을 부여하지 않습니다. 복구되거나 정책이
바뀌면 선택지가 없을 수 있습니다. 조치를 열면 새 계획을 만들고, 승인해야만 적용합니다.

## Webhook 본문

```json
{
  "source": "opsgate",
  "sent_at": "2026-10-29T03:00:30Z",
  "alerts": [
    {
      "time": "2026-10-29T03:00:12Z",
      "cluster": "default",
      "severity": "warning",
      "kind": "Pod",
      "namespace": "apps",
      "name": "web-7d9f8-abcde",
      "reason": "OOMKilled",
      "summary": "container app restarted (3 restarts): OOMKilled, exit code 137",
      "hint": "The container hit its memory limit. Check the limit and the app's usage; a restart will not prevent a repeat.",
      "suppressed": 2
    }
  ],
  "dropped": 0,
  "text": "[warning] default Pod apps/web-7d9f8-abcde: container app restarted (3 restarts): OOMKilled, exit code 137 (+2 similar)\n  hint: The container hit its memory limit. Check the limit and the app's usage; a restart will not prevent a repeat."
}
```

`text`는 그대로 채팅에 전달하면 됩니다. `suppressed`는 이 알림에 합쳐진 반복 횟수입니다. 한 묶음 안에서 같은 알림이 반복됐거나, 쿨다운 동안 같은 경고가 반복된 횟수입니다. 정보 알림은 쿨다운으로 막지 않고, 한 묶음 안에서 반복되면 가장 최근 요약으로 합칩니다. `reason`·`summary`·힌트를 포함한 클러스터의 모든 알림 텍스트는 전송과 중복 식별 전에 마스킹합니다. 제어 문자는 공백으로 바꾸고 텍스트는 유니코드 300자와 생략 표시로 제한합니다. 직접 JSON과 사용자 템플릿에도 같은 정리된 필드를 전달하며, 일반적인 기계 판독용 이유 코드는 바꾸지 않습니다. 그래도 클러스터에서 온 메시지는 신뢰할 수 없는 데이터로 다뤄야 합니다.

출력에 `secret_file`을 설정하면 모든 POST에 다음 헤더가 붙습니다.

```text
X-Opsgate-Signature: sha256=<hex HMAC-SHA256(secret, 원본 본문)>
```

받는 쪽에서는 원본 본문으로 서명을 검증한 뒤, 서명된 `sent_at`이 오래된 묶음은 거부해 재전송 공격을 막으세요. n8n 같은 수신 측은 코드 없이 기본 HMAC 노드로 검증할 수 있습니다. 리다이렉트는 따라가지 않습니다. 네트워크 오류, HTTP 429와 5xx 응답은 1초, 4초 뒤 두 번 재시도하고, 그래도 실패하면 그 묶음을 잃었다고 로그에 남깁니다. 내부 큐(알림 1000개)가 가득 차면 새 알림은 다음 POST의 `dropped`에 셉니다.

## 실행

watcher는 복제본 1개짜리 Deployment로 실행합니다. `deploy/kubernetes/opsgate-watch.yaml`은 `rbac-readonly.yaml`의 `opsgate` ServiceAccount와 클러스터 내부 설정, `opsgate-watch-hmac` Secret을 씁니다. 필요한 권한은 pods, configmaps, deployments, statefulsets, daemonsets, nodes에 대한 `list`와 `watch`뿐입니다. API로 Secret을 읽지 않고 아무것도 변경하지 않습니다. `replicas: 1`과 `Recreate` 전략은 그대로 두세요. 복제본마다 전체 범위를 watch하므로 두 개면 모든 알림이 두 번 갑니다. 같은 이유로 DaemonSet도 아닙니다.

```sh
kubectl apply -f deploy/kubernetes/rbac-readonly.yaml
kubectl -n opsgate create secret generic opsgate-watch-hmac --from-literal=key="$(openssl rand -hex 32)"
kubectl apply -f deploy/kubernetes/opsgate-watch.yaml
kubectl -n opsgate logs deployment/opsgate-watch
```

클러스터 밖에서도 kubeconfig로 클러스터에 접근할 수 있으면 `opsgate watch`를 실행할 수 있습니다.

watch 오류(예: RBAC 권한 부족)는 로그에 남고 client-go가 다시 시도합니다. watcher를 재시작하면 기준점이 다시 잡히므로, 꺼져 있던 동안의 변경은 알리지 않습니다.

## Discord 직접 전송과 커스텀 웹훅

`watch.outputs`로 n8n 없이 Discord나 원하는 JSON 수신 서버에 직접 보낼 수
있습니다. `watch.email`과 함께 쓸 수 있고, 각 대상은 별도 큐,
쿨다운과 재시도를 사용하므로 느린 대상이 다른 전송을 막지 않습니다.
전체 설정은 [설정 문서](../configuration.md#direct-webhook-destinations)를 참고하세요.

```yaml
watch:
  outputs:
    - name: discord
      type: discord
      url_file: /etc/opsgate/discord-webhook-url
    - name: custom
      type: webhook
      url: https://alerts.example.com/ingest
      header_files:
        Authorization: /etc/opsgate/alerts-authorization
      body_template: |
        {"message": {{json .Text}}, "sent_at": {{json .SentAt}}, "alerts": {{json .Alerts}}}
```

Discord 채널에서 만든 웹훅 URL을 `url_file`에 저장하면 됩니다. 인증 헤더
파일에는 `Bearer TOKEN`처럼 전체 값을 넣으세요. 파일은 시작할 때 읽으므로
변경 후 watch를 재시작해야 합니다. URL과 헤더를 파일로 지정하면 설정 출력에
비밀 값이 나타나지 않습니다. 직접 적은 URL과 헤더는 설정 출력에 그대로 보입니다.

`type` 기본값은 `webhook`이며 원본 JSON을 보냅니다. `body_template`을 지정하면
Go 템플릿에 배치 필드 `.Source`, `.SentAt`, `.Alerts`, `.Dropped`, `.Text`, `.Group`(그룹이 없으면 빈 문자열)이 전달됩니다.
`{{json 값}}`을 따옴표로 감싸지 않고 사용하면 안전하게 JSON으로 인코딩합니다.
알림 텍스트 자체는 템플릿으로 실행하지 않습니다. 템플릿은 최대 64 KiB, 결과는
유효한 JSON으로 최대 1 MiB입니다. 문법 오류는 시작 시 거부하고, 실행 오류나
유효하지 않은 JSON은 전송하지 않고 해당 배치 실패로 기록합니다.

Discord 기본 형식은 발생·전송 시각(UTC), 클러스터, 심각도, 리소스 종류와
namespace/이름, 오류 이유·요약, 반복 횟수, 힌트와 재시작 제안 등을 표시합니다.
자동 멘션은 끕니다. 설명이 Discord의 4096 UTF-16 단위 제한을 넘으면 잘림을
표시하므로 `watch.max_alerts`를 줄이거나 원본 JSON 대상에도 함께 보내세요.
`type: webhook`과 템플릿으로 Discord 형식을 직접 만들 수도 있지만, 이 경우
길이 제한과 멘션 설정도 직접 처리해야 합니다.

각 대상의 `secret_file`은 변환 후 실제 HTTP 본문을 HMAC 서명합니다. 대상별로
`headers`, `header_files`, `allow_private_http`, `problems_only`를 지정할 수 있고,
공통 `watch.batch`, `watch.cooldown`, `watch.max_alerts`가 적용됩니다. 전역
`watch.problems_only`가 켜져 있으면 모든 대상에서 정보 알림을 제외합니다.

리다이렉트는 거부하고 요청 제한 시간은 10초입니다. 네트워크 오류, HTTP 429와
5xx는 1초·4초 뒤 두 번 재시도합니다. 별도의 서버 지정 속도 제한 스케줄러는
없습니다. 영구 큐와 정확히 한 번 전송 보장은 없으며, 재시도로 중복될 수 있습니다.

## 승인함으로 받는 휴대폰 알림

`opsgate watch`는 HTTP 서버와 별개 프로세스이며, 다른 파드에서 실행할 수 있습니다.
양쪽에 전용 HMAC 키를 설정하세요. 공백을 제거한 뒤 16–4096바이트여야 하며,
다른 웹훅 키와 구분한 무작위 키를 쓰세요.

```yaml
# watch 프로세스의 기존 설정에 합칩니다.
watch:
  approval_url: https://ops.example.com
  inbox:
    url: https://ops.example.com
    secret_file: /etc/opsgate/inbox/key
    allow_private_http: false
```

```yaml
# builtin 인증을 쓰는 HTTP 서버의 기존 설정에 합칩니다.
auth:
  builtin:
    users:
      - subject: passkey:owner
        scopes: [ops:read, ops:write]
approval:
  web: true
  inbox_notify:
    secret_file: /etc/opsgate/inbox/key
    subjects: [passkey:owner]
  push:
    enabled: true
    subject: mailto:owner@example.com
    state_file: /var/lib/opsgate/push.json
```

`watch.inbox.url`은 경로·쿼리·자격 증명이 없는 origin입니다. HTTPS를 기본으로 쓰며,
루프백 HTTP와 `allow_private_http: true`인 사설 HTTP는 웹훅 출력과 같은 URL 및
연결 시 주소 검사를 거칩니다. 클러스터 내부 예시는 `http://opsgate.opsgate.svc:8787`입니다.
메신저 링크용 `approval_url`은 공개 HTTPS origin으로 유지하세요. 생략하면 승인함
origin으로 선택지 링크를 만듭니다. 리버스 프록시는 `POST /approvals/notify`를
전달해야 합니다. 활성화된 서명 POST 경로는 사설 서비스 Host도 받으며, 브라우저와
MCP 경로는 기존 Host 허용 정책을 유지합니다.

이 독립 출력은 다른 출력을 막지 않는 전용 큐, **1초** 묶음 주기(그룹 설정 시 그룹 batch로 대체), 기존 cooldown과
웹훅 재시도 정책을 씁니다. 네트워크 오류·429·5xx는 1초, 4초를 기다려 최대 세 번
시도합니다. **조치 선택** 링크가 있는 파드 경고만 보내며 정보 알림은 보내지 않습니다.
묶음은 최대 50개이며 `watch.max_alerts` 제한도 적용합니다. 다른 출력의 큐와 주기는
각자 유지되고 승인함 출력만 설정한 watch도 지원합니다.

서명한 JSON은 `sent_at`과 `notices`만 담습니다. 각 항목의 필드는 `cluster`,
`namespace`, `pod`, `workload_kind`, `workload_name`, `reason`, `severity`,
`occurred_at`입니다. 요약·메시지·힌트·전달받은 URL 등 클러스터 자유 텍스트는 보내지
않습니다. 식별자는 watch에서 이미 마스킹했으며 유효하지 않거나 마스킹된 이동 대상은
건너뜁니다. 서명은 기존 `X-Opsgate-Signature: sha256=<원문 본문의 HMAC-SHA256 hex>`
형식입니다. 이 알림은 승인 요청을 만들거나 조치를 실행하지 않습니다.

휴대폰의 일반 브라우저에서 `/approvals`를 열어 알림을 한 번 켜세요
([설정 방법](approval.md#승인함과-알림)). iPhone은 먼저 홈 화면에 추가해야 합니다.
푸시를 누르면 승인함의 **Alerts waiting for a decision**에서 현재 선택지로 이동합니다.
Telegram/Discord 내장 브라우저 밖에서 조치를 고르고 패스키로 계획을 검토·승인하세요.
푸시 없이도 승인함에서 알림을 볼 수 있습니다. [제한과 보안](approval.md#서명된-watch-알림)을 참고하세요.
