# Kubernetes watch 알림

[English](../watch.md)

`opsgate watch`는 MCP 서버와 따로 계속 실행되는 프로세스입니다. Kubernetes를 watch로 지켜보다가 문제나 변경이 생기면 webhook으로 알림을 보냅니다. 예를 들어 n8n 워크플로우로 받아 채팅으로 전달할 수 있습니다. MCP 클라이언트를 받지 않고 도구도 없습니다. 사람이 알림을 보고 나면 AI 클라이언트가 기존 조회 도구로 원인을 살핍니다. MCP 서버와 분리되어 있어 stdio 사용자도 쓸 수 있고, 별도 계정과 kubeconfig로 실행할 수 있습니다.

```sh
opsgate watch -config /etc/opsgate/opsgate.yaml
```

클러스터와 네임스페이스 정책은 `providers.kubernetes`에서, 전송 설정은 `watch`에서 읽습니다.

```yaml
watch:
  webhook_url: https://n8n.example.com/webhook/opsgate-watch
  allow_private_http: false  # true면 사설 네트워크로 가는 http도 허용합니다
  secret_file: /etc/opsgate/watch-secret   # 선택. HMAC 키, 16바이트 이상
  clusters: []          # 기본값: 설정된 모든 클러스터
  problems_only: false  # true면 경고만 보냅니다
  batch: 30s            # 이 시간 동안 모은 알림을 한 번에 보냅니다 (1s–10m)
  cooldown: 15m         # 같은 객체의 같은 경고는 이 시간 동안 다시 보내지 않고 횟수만 셉니다 (0–24h)
  max_alerts: 50        # 한 번에 보내는 최대 알림 수 (1–500). 넘치면 dropped로 셉니다
```

`webhook_url`은 https이거나 루프백 주소로 가는 http여야 하고, 자격 증명이나 프래그먼트를 넣을 수 없습니다. `allow_private_http: true`로 두면 Service 이름(`http://n8n.n8n.svc.cluster.local:5678/...`)이나 사설 IP 주소처럼 사설 네트워크로 가는 http도 허용합니다. 이때 모든 연결은 DNS 조회 후 사설 주소나 루프백 주소에 닿아야 하고, 검사한 주소가 실제 수신 측 주소가 되도록 프록시 설정은 무시합니다. 공인 IP를 직접 적으면 여전히 거부합니다. 알림은 그 네트워크를 암호화 없이 지나갑니다. URL 쿼리에 비밀 키를 넣어도 로그에는 남지 않습니다.

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
      "suppressed": 2
    }
  ],
  "dropped": 0,
  "text": "[warning] default Pod apps/web-7d9f8-abcde: container app restarted (3 restarts): OOMKilled, exit code 137 (+2 similar)"
}
```

`text`는 그대로 채팅에 전달하면 됩니다. `suppressed`는 이 알림에 합쳐진 반복 횟수입니다. 한 묶음 안에서 같은 알림이 반복됐거나, 쿨다운 동안 같은 경고가 반복된 횟수입니다. 정보 알림은 쿨다운으로 막지 않고, 한 묶음 안에서 반복되면 가장 최근 요약으로 합칩니다. 요약은 도구 출력과 같은 방식으로 정리합니다. 제어 문자는 지우고, 알려진 비밀 형식은 마스킹하고, 300자에서 자릅니다. 그래도 클러스터에서 온 메시지는 신뢰할 수 없는 데이터로 다뤄야 합니다.

`secret_file`을 설정하면 모든 POST에 다음 헤더가 붙습니다.

```text
X-Opsgate-Signature: sha256=<hex HMAC-SHA256(secret, 원본 본문)>
```

받는 쪽에서는 원본 본문으로 서명을 검증한 뒤, 서명된 `sent_at`이 오래된 묶음은 거부해 재전송 공격을 막으세요. n8n 같은 수신 측은 코드 없이 기본 HMAC 노드로 검증할 수 있습니다. 리다이렉트는 따라가지 않습니다. 네트워크 오류와 5xx 응답은 1초, 4초 뒤 두 번 재시도하고, 그래도 실패하면 그 묶음을 잃었다고 로그에 남깁니다. 내부 큐(알림 1000개)가 가득 차면 새 알림은 다음 POST의 `dropped`에 셉니다.

## 실행

watcher는 복제본 1개짜리 Deployment로 실행합니다. `deploy/kubernetes/opsgate-watch.yaml`은 `rbac-readonly.yaml`의 `opsgate` ServiceAccount와 클러스터 내부 설정, `opsgate-watch-hmac` Secret을 씁니다. 필요한 권한은 pods, configmaps, deployments, statefulsets, daemonsets, nodes에 대한 `list`와 `watch`뿐입니다. API로 Secret을 읽지 않고 아무것도 변경하지 않습니다. `replicas: 1`과 `Recreate` 전략은 그대로 두세요. 복제본마다 전체 범위를 watch하므로 두 개면 모든 알림이 두 번 갑니다. 같은 이유로 DaemonSet도 아닙니다.

```sh
kubectl apply -f deploy/kubernetes/rbac-readonly.yaml
kubectl -n opsgate create secret generic opsgate-watch-hmac --from-literal=key="$(openssl rand -hex 32)"
kubectl apply -f deploy/kubernetes/opsgate-watch.yaml
kubectl -n opsgate logs deployment/opsgate-watch
```

클러스터 밖에서는 `deploy/systemd/opsgate-watch.service`가 서버와 같은 보안 설정으로 호스트에서 실행합니다. 이때는 kubeconfig를 씁니다.

watch 오류(예: RBAC 권한 부족)는 로그에 남고 client-go가 다시 시도합니다. watcher를 재시작하면 기준점이 다시 잡히므로, 꺼져 있던 동안의 변경은 알리지 않습니다.
