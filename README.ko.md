<p align="center"><img src="docs/images/logo.png" alt="opsgate 마스코트: 승인 체크 패를 든 해태" width="240"></p>

# opsgate

[English](README.md) · [웹사이트](https://gkdis6.github.io/opsgate/ko/)

**AI는 제안만 하고, 실행은 내 패스키로.**

서버를 혼자 운영하다 보면 AI에게 맡기고 싶은 일이 많습니다. 그렇다고 셸이나 kubectl
권한까지 넘기기는 불안하죠. opsgate는 그 사이를 지키는 MCP 서버입니다. Kubernetes용이고,
직접 설치해서 쓰는 오픈 소스이며, Go로 만들었습니다.

Claude, ChatGPT, Claude Code, Cursor 같은 AI나 알림 규칙이 서버를 바꾸자고 하면, opsgate는
그 제안을 **승인 카드**로 만듭니다. 카드에는 무엇을 어떻게 바꾸는지, 누가 제안했는지,
되돌릴 수 있는지가 적혀 있습니다. 폰이나 PC에서 패스키로 승인해야만 실행되고, 실행한
뒤에는 서비스가 정말 살아났는지까지 알려 줍니다.

**약속하는 세 가지**

1. **승인한 그대로만 실행합니다.** 실행 직전에 계획을 다시 확인하고, 그사이 상황이
   바뀌었으면 멈춥니다.
2. **데이터를 지우는 조치는 없습니다.** 할 수 있는 건 재시작, 파드 수 조정, 이전 이미지로
   되돌리기, 컨트롤러가 다시 띄우는 파드 교체뿐입니다.
3. **알림부터 조치까지 몇 번만 누르면 됩니다.** 알림을 누르고, 조치를 고르고, 승인하면
   끝입니다.

평소 AI가 노트를 쓸 때도 같은 카드로 승인합니다. 그래서 정작 장애가 났을 때도 손에 익은
방식 그대로 처리할 수 있습니다.

opsgate가 하지 않는 일도 분명히 해 둡니다. 셸이나 범용 에이전트가 아니고, 모니터링
대시보드나 팀 온콜 도구도 아닙니다. 사람이 승인하지 않으면 아무것도 고치지 않습니다.

> **현재 상태:** 아직 1.0 전입니다. 핵심 흐름(알림, 승인 카드, 패스키 승인, 복구 확인)은
> [안정성 문서](docs/ko/stability.md)에 적은 대로 호환성을 유지합니다. 노트, 호스트, analyzer는 실험 기능입니다.
> 외부 보안 검토는 받지 않았습니다. 무엇을 막고 무엇을 못 막는지는
> [보안 정책](docs/ko/security.md)에 정리해 두었습니다.



## 왜 만들었나

혼자 서버를 운영하면 늘 두 가지가 아쉽습니다.

- **장애를 늦게 압니다.** 알림을 제대로 갖춰 두지 않으면 사용자가 연락하거나 우연히
  보고 나서야 압니다.
- **고칠 줄 알아도 손이 닿지 않습니다.** 밖에서 폰 SSH 앱으로 명령을 치는 건 느리고
  실수하기 쉽습니다.

AI에게 셸을 주면 두 번째 문제는 풀립니다. 대신 명령 하나만 잘못돼도 되돌릴 수 없다는 더
큰 걱정이 생깁니다. opsgate는 두 문제를 다 풀면서도 모든 변경을 사람이 확인하게 합니다.



## 장애가 나면 이렇게 처리합니다

1. **먼저 알려 줍니다.** `opsgate watch`가 Kubernetes를 지켜보다가 문제가 생기면 바로
   Discord, 웹훅, 이메일로 알립니다. 승인함에 올리고 폰으로 푸시를 보낼 수도 있습니다.
   [Watch 알림](#watch-알림)을 참고하세요.
2. **밖에서도 바로 살립니다.** 푸시나 알림의 **조치 선택** 링크를 누르고 패스키로
   로그인하면, 지금 상태에 맞는 조치가 나옵니다. 마지막으로 잘 돌던 이미지로 되돌리기나
   재시작 같은 것들입니다. 하나를 골라 계획을 확인하고 승인하면 곧바로 실행됩니다. 그다음
   opsgate가 워크로드를 지켜보다가 정말 **복구됐는지** 알려 줍니다. 되돌릴 수 있는 조치면
   **되돌리기** 버튼도 생깁니다. 폰 브라우저 하나면 됩니다.
3. **원인은 그다음에 찾습니다.** 서비스가 돌아오면 AI에게 opsgate 조회 도구로 원인을 찾아
   달라고 하면 됩니다. 선택 기능인 [analyzer](docs/ko/analyzer.md)가 먼저 살펴보게 할 수도
   있습니다.
4. **기록을 남깁니다.** 노트 기능을 켜면 런북과 지난 장애 기록을 Markdown으로 남겨 두고,
   비슷한 일이 생겼을 때 AI가 먼저 찾아봅니다. [노트](docs/ko/notes.md)를 참고하세요.



## 설계 원칙

- **임의 명령은 없습니다.** 정해진 도구만 있습니다. 셸 도구도, Kubernetes Secret을 읽는
  도구도 없습니다.
- **처음에는 전부 꺼져 있습니다.** 기능, 쓰기, 허용 대상 모두 설정에서 직접 켜야
  동작합니다.
- **승인하면 바로 실행합니다.** 쓰기 도구는 먼저 바꿀 내용을 정확히 계산합니다. 사람이
  승인하면 계획을 한 번 더 확인하고 딱 한 번 실행합니다. 승인했다고 채팅에 다시 알릴 필요는
  없습니다.
- **조치는 정해진 목록에서만 나옵니다.** 규칙이든 AI든 목록에 있는 조치만 제안할 수 있고,
  버튼에는 누가 제안했는지 표시합니다. 실제로 실행할지는 허용 목록과 사람이 정합니다.
- **지우지 않습니다.** 노트를 지우면 `.trash`로 옮깁니다. 삭제는 파드 삭제 하나뿐이고,
  이것도 따로 허용하고 승인해야 합니다. 컨트롤러가 다시 만들지 않는 파드면 계획에서 경고하고,
  링크로는 지울 수 없습니다.
- **원격 접속은 로그인해야 합니다.** 내장 OAuth 2.1 서버가 있어서 claude.ai와 ChatGPT
  커넥터를 바로 붙일 수 있습니다. 패스키, GitHub, OIDC(Google 포함), 외부 JWT로 로그인합니다.
- **조회 결과는 데이터로만 다룹니다.** 로그 안에 지시문이 섞여 있어도 따르지 말라고 AI에게
  안내합니다. 결과에서 민감 정보를 가리고 길이를 제한하며, 누가 어떤 도구를 불렀는지 감사
  로그에 한 줄씩 남깁니다.



## 앞으로의 방향

[로드맵](docs/ko/roadmap.md)에 있던 단계는 모두 릴리스했습니다. PostgreSQL provider, 설정
다시 불러오기 같은 아이디어는 약속 없이 로드맵에 적어 두었습니다.



## 할 수 있는 일

opsgate는 기능을 provider라는 묶음으로 나눕니다. 필요한 것만 켜서 쓰면 됩니다.

| provider | 조회 | 변경 (승인 필요) |
| --- | --- | --- |
| Kubernetes | 클러스터, 개요, 목록, 상세, 환경 변수, 서비스 점검, 이벤트, 파드 로그, 조치 선택지 | 재시작, 파드 수 조정, 파드 삭제, 이전 이미지로 되돌리기 |
| 호스트 *(실험, 기본 꺼짐)*: Kubernetes 없는 서버의 systemd 유닛과 Docker/Compose 컨테이너 | 유닛, 유닛 로그, 컨테이너, 컨테이너 로그 | 유닛 재시작, 컨테이너 재시작 |
| 노트 *(실험)*: 런북과 장애 기록, Markdown 폴더나 Obsidian 볼트 | 검색, 읽기, 목록, 최근 노트, 링크, 태그 | 새로 쓰기, 덧붙이기, 고치기, 휴지통으로 옮기기 |

권한은 두 가지입니다. `ops:read`는 조회, `ops:write`는 변경입니다. 여기에 허용 목록이 한
번 더 걸립니다. 그래서 `ops:write`가 있어도 설정에서 허용하지 않은 도구나 대상은 건드릴 수
없습니다.

노트는 지정한 폴더 안에만 씁니다. 서버의 아무 경로에나 파일을 만드는 도구나, CPU·메모리
requests/limits를 바꾸는 도구는 없습니다. requests/limits 값은 상세 조회로 확인할 수
있습니다. 도구별 인자와 제한은 [도구 문서](docs/ko/tools.md)에 있습니다.

### Kubernetes 변경

Kubernetes 변경은 허용 목록에 대상을 적어야 켜지고, 실행은 언제나 사람이 승인합니다. MCP
클라이언트에서 요청할 수도 있고, 링크로 열 수도 있습니다. 링크를 쓰려면 클러스터 설정에
`action_approval_url`을 넣으세요.

| 변경 | 켜는 설정 | 선택지 페이지가 이 조치를 권하는 때 | 되돌리기 |
| --- | --- | --- | --- |
| 재시작 | `rollout_restart` | 컨테이너가 계속 재시작하거나(설정 오류와 Job은 제외) 메모리 제한에 계속 걸릴 때 | 없음 |
| 이전 이미지로 되돌리기 | `restore_image` | 이미지를 받지 못하거나, 새 배포가 계속 실패하는데 이전 ReplicaSet이 아직 준비 상태일 때 | 되돌리기 전 이미지로 |
| 파드 삭제 | `delete_pod` | 컨트롤러가 관리하는 파드가 종료 중에 멈췄거나 연결이 끊긴 노드에 있을 때 | 없음 |
| 파드 수 조정 | `scale` (상한은 `max_replicas`) | 규칙으로는 권하지 않습니다. MCP 클라이언트나 AI 제안으로만 씁니다 | 이전 파드 수로 |

링크를 열면 로그인한 뒤 새로 계산한 계획이 나옵니다. 승인 버튼을 누르기 전에는 아무것도
바뀌지 않고, 링크에는 승인 시간 창도 적용되지 않습니다. analyzer는 조회 전용 `action_link`로만
조치를 제안하고, 영향이 가장 작은 것부터 권하며, 버튼에 AI 제안이라고 표시합니다.
[카탈로그 조치 링크](docs/ko/approval.md#카탈로그-조치-링크)와
[장애별 현재 선택지](docs/ko/approval.md#장애별-현재-선택지)를 참고하세요.



## 동작 방식

<img src="docs/images/ko/architecture.svg" alt="클라이언트 연결, 인증과 도구 처리, provider 및 OS 계정·RBAC 신뢰 경계를 보여 주는 opsgate 구조" width="830">

조회 결과를 분석하고 다음에 뭘 할지 고르는 건 AI 클라이언트입니다. opsgate MCP 서버는 AI
모델을 부르지 않습니다. 요청받은 도구의 권한과 대상을 검사하고 실행할 뿐입니다. 모델을
직접 부르는 건 선택 기능인 별도 프로세스 `opsgate-analyzer`뿐입니다.

Kubernetes 도구는 kubeconfig나 클러스터 안의 ServiceAccount로 Kubernetes API에 붙습니다.
그래서 내 PC에서 실행해도 네트워크와 권한만 있으면 원격 클러스터를 볼 수 있습니다.

클라이언트는 두 방식으로 연결합니다. 하나는 로컬 stdio로, 클라이언트가 opsgate를 직접
실행하고 표준 입출력으로 주고받습니다. 다른 하나는 TLS 리버스 프록시를 거치는 HTTPS입니다.
opsgate는 누가 요청했는지와 정책을 확인하고, 쓰기라면 계획을 세워 승인을 받은 뒤 실행하고,
결과를 다듬어 감사 로그에 남깁니다. 실제로 닿을 수 있는 범위는 OS 계정과 Kubernetes
RBAC가 정합니다.

자세한 내용: [구조](docs/ko/architecture.md) · [승인](docs/ko/approval.md) ·
[인증](docs/ko/authentication.md)

### 승인은 이렇게 진행됩니다

쓰기 도구는 먼저 대상과 바꿀 내용을 확인해 실행 계획을 만듭니다. builtin 로그인을 쓰면
기본으로 웹 승인만 받습니다. 사용자가 로그인한 페이지에서 계획을 읽고 승인해야 실행됩니다.
AI 앱 안에서 바로 승인하는 방식(elicitation)은 `approval.require_web: false`로 직접 켜야
합니다. 켜면 침해된 클라이언트가 스스로 승인할 수 있다는 점은 감수해야 합니다. stdio처럼 웹
승인을 쓸 수 없는 경우는 [승인 문서](docs/ko/approval.md)를 보세요.

승인은 그 인자와 계획에만 한 번 유효합니다. 웹에서 승인하면 계획을 다시 확인하고 바로 한 번
실행합니다. 채팅 쪽 호출은 `approval.wait`(기본 25초, 최대 55초)까지 기다립니다. 그 안에
승인하면 결과가 바로 채팅으로 오고, 더 늦으면 AI가 `approval_status`로 결과를 확인합니다.
같은 요청을 `approval_id`와 함께 다시 보내도 저장된 결과만 돌려주고 두 번 실행하지 않습니다.

### 어디서든 승인하기

- **승인함:** `/approvals`에서 기다리는 승인, 최근에 처리한 승인, 조치를 기다리는 알림과 그
  결과를 한곳에서 봅니다. 폰이나 PC의 일반 브라우저로 여세요. 메신저 안 브라우저에서는
  패스키가 자주 실패합니다.
- **앱으로 설치:** iPhone은 홈 화면에 추가한 앱에만 알림을 보냅니다. Safari에서 공유 →
  **홈 화면에 추가**를 누르세요. 어느 opsgate 페이지에서 추가해도 같은 앱이 되고, 앱을 열면
  승인함이 나옵니다.
- **푸시:** `approval.push`를 켜면 채팅에서 요청한 변경이, `watch.inbox`까지 켜면 새 알림도
  폰으로 옵니다. 알림을 누르면 가장 최근에 기다리는 승인 페이지가 바로 열립니다. 푸시 자체에는
  내용이 없고, 알림 문구에도 대상 이름을 넣지 않습니다. 승인함의 **테스트 알림 보내기**로
  기기에 알림이 오는지 확인할 수 있습니다.
- **폰에서 열기:** PC에서 승인 페이지, 승인함, 조치 선택 페이지를 보고 있다면 **폰에서
  열기**를 누르세요. QR 코드를 폰으로 찍으면 같은 페이지가 열리고, 폰의 패스키로 승인하면
  됩니다.
- **되돌리기:** 파드 수 조정이나 이미지 되돌리기를 실행하면 결과에 이전 상태로 돌아가는
  링크가 붙습니다. 이 링크도 새 계획과 승인을 거치는 별개의 조치입니다.

[승인함과 알림](docs/ko/approval.md#승인함과-알림), [복구 확인](docs/ko/approval.md#복구-확인),
[되돌리기](docs/ko/approval.md#되돌리기)를 참고하세요.

같은 승인을 자꾸 하기 번거롭다면 승인 시간 창을 켤 수 있습니다. 정해 둔 도구를 일정 시간
동안 한 번에 승인해 두는 기능입니다. 그동안 같은 사용자가 같은 대상에 그 도구를 쓰면, 인자가
달라도 승인을 다시 묻지 않습니다. 권한과 허용 대상 검사는 매번 하고, 기본값은 꺼짐입니다.
`k8s_restore_image`에는 적용되지 않습니다.

<img src="docs/images/ko/approval-flow.svg" alt="쓰기 호출의 계획, 승인 시간 창·클라이언트·웹 승인, 실행 전 검증과 실행 및 거부 경로" width="830">

### 노트: 런북과 장애 기록

노트 기능을 켜면, 조치하기 전에 지난 기록을 찾아보고 끝난 뒤에는 기록을 남길지 물어보라고
AI에게 안내합니다. 실제로 찾아보고 물어볼지는 AI 클라이언트가 정하므로 늘 그렇게 된다고
장담할 수는 없습니다. 노트를 바꿀 때도 승인을 받습니다. 남긴 기록은 다음 조사 때 다시
찾아 쓸 수 있습니다. [노트](docs/ko/notes.md)를 참고하세요.

<img src="docs/images/ko/note-memory.svg" alt="upstream timeout 검색으로 지난 조치를 참고하고, 사람의 승인 후 실행과 노트 기록을 거쳐 다음 조사에 활용하는 흐름" width="830">



## 빠른 시작: 내 PC에서 (stdio)

[릴리스](https://github.com/gkdis6/opsgate/releases)에서 압축 파일을 받아
[검증](docs/ko/deploy.md#설치-전-릴리스-검증)한 뒤 쓰거나, Go 1.26 이상으로 직접 빌드하세요.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest   # 또는: make build
```

이렇게 하면 `opsgate`만 빌드됩니다. 선택 기능인 analyzer는 `./cmd/opsgate-analyzer`로 따로
빌드합니다.

지금 쓰는 kubeconfig로 접속하는 설정 파일을 하나 만드세요. 아래 예에서는
`/path/opsgate.yaml`로 저장했다고 가정합니다.

```yaml
stdio:
  scopes: [ops:read]
providers:
  kubernetes:
    enabled: true
    namespaces: [default]
```

Claude Code에 등록합니다. 경로는 절대 경로로 쓰세요.

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

stdio는 실행한 계정의 권한 그대로 움직이므로, 관리자가 아닌 일반 계정으로 실행하세요. 다른
클라이언트는 [클라이언트 설정](docs/ko/clients.md)을 보세요.



## 빠른 시작: 서버에 올려서 (HTTPS + 패스키)

패스키를 쓰면 별도 로그인 서비스가 필요 없습니다. Face ID, Touch ID, Windows Hello, 폰,
보안 키로 로그인합니다.

`opsgate init`을 실행하면 공개 주소, 로그인할 사람, namespace를 묻고 짧은 설정 파일을
만들어 줍니다.

```sh
opsgate init            # opsgate.yaml을 만듭니다. 플래그로 미리 답할 수도 있습니다(-h 참고)
```

```yaml
mode: kubernetes
http:
  public_url: "https://mcp.example.com"
auth:
  builtin:
    users:
      - subject: "passkey:alice"
        scopes: [ops:read]
    admins: ["passkey:alice"]
    allowed_redirect_hosts: [claude.ai, chatgpt.com, localhost]
    unknown_redirect_hosts: approve
providers:
  kubernetes:
    namespaces: ["*"]
    deny_namespaces: ["kube-system", "kube-public", "kube-node-lease", "opsgate"]
    allow_logs: true
```

나머지는 `mode`가 알아서 채웁니다. `0.0.0.0:8787`에서 HTTP를 받고, 내장 패스키 로그인과 웹
승인을 쓰고, 상태는 `/var/lib/opsgate`에 두고, 파드의 ServiceAccount로 클러스터에 붙습니다.
실제로 적용되는 값 전체는 `opsgate config show -config opsgate.yaml`로 볼 수 있습니다.

이 파일을 `deploy/kubernetes/opsgate-deployment.yaml`의 `opsgate` ConfigMap에 넣고 적용하세요.
매니페스트 예제는 Google 로그인 기준이지만, 패스키를 쓰면 `opsgate-upstream` Secret은 필요
없습니다. Secret 준비, 이미지 검증, RBAC, probe 설정은 [배포 문서](docs/ko/deploy.md)를
따르세요.

```sh
kubectl apply -f deploy/kubernetes/opsgate-deployment.yaml
```

1. 처음 시작하면 아직 패스키가 없는 사용자마다 일회용 등록 링크(24시간 유효)가 로그에
   찍힙니다. 링크를 열고 **Create passkey**를 누르세요. 링크를 새로 받으려면
   `kubectl -n opsgate exec -it deploy/opsgate -- /opsgate passkey invite -config /etc/opsgate/opsgate.yaml -subject passkey:alice`를
   실행하세요. 폰으로 찍을 수 있는 QR 코드도 함께 나옵니다.
2. 앞단에 TLS ingress나 리버스 프록시를 두고, `Host` 헤더를 그대로 넘기게 하세요. 공개
   호스트가 `public_url`과 다르면 421로 거절합니다.
3. 클라이언트를 `https://mcp.example.com/mcp`에 연결하세요. 예를 들면
   `claude mcp add --transport http opsgate https://mcp.example.com/mcp`입니다. claude.ai나
   ChatGPT에서는 커넥터로 추가합니다.

만들어진 설정은 클라이언트가 돌아갈 수 있는 주소(`allowed_redirect_hosts`)를 이미 좁혀
둡니다. 외부에 열기 전에 한 번 확인하세요. GitHub, Google 같은 다른 로그인 방식은
[인증 문서](docs/ko/authentication.md)에, 프록시 경로와 릴리스 검증은
[배포 문서](docs/ko/deploy.md)에 있습니다. 명령과 플래그는 `opsgate -h`로 볼 수 있습니다.



## 빠른 시작: 폰으로 복구하기

`opsgate init` 한 번이면 알림부터 복구 확인까지 전체 흐름이 준비됩니다. 폰에서 재시작하거나
이전 이미지로 되돌릴 워크로드를 지정하세요.

```sh
opsgate init -recover "apps/*" -language ko
```

`opsgate.yaml`과 함께 watch용 `opsgate-watch.yaml`도 만듭니다. 이 설정은 사용자에게
`ops:write`를 주고, 지정한 워크로드를 재시작과 이미지 되돌리기 대상에 넣고, Web Push를 켜고,
서명 키로 watch를 승인함에 연결합니다. 그다음 남은 명령을 순서대로 보여 줍니다. RBAC, 서명
키 Secret, 매니페스트와 설정 파일 적용, 패스키 등록, 폰에서 알림 켜기까지입니다. 마지막에는
승인함 QR 코드가 나오니 폰으로 찍어서 열면 됩니다.

매니페스트를 다시 적용하면 ConfigMap이 예제 내용으로 돌아갑니다. 설정 파일은 꼭 매니페스트를
적용한 다음에 넣으세요.



## Watch 알림

MCP 서버 옆에 `opsgate watch`를 띄우면 Kubernetes에서 생기는 문제와 변경을 알림으로 받을 수
있습니다. Kubernetes API를 직접 지켜보다가 Discord, JSON 웹훅, 이메일로 바로 보내므로 n8n
같은 도구는 없어도 됩니다.

- `watch.approval_url`을 설정하면 파드 경고에 **조치 선택** 링크가 붙습니다.
- `watch.inbox`를 설정하면 경고가 승인함에도 올라가고 폰으로 푸시가 갑니다
  ([서명된 watch 알림](docs/ko/approval.md#서명된-watch-알림)).
- [namespace 그룹](docs/ko/watch.md#그룹)으로 namespace마다 받을 곳, 경고만 받을지,
  얼마나 모아서 보낼지를 따로 정할 수 있습니다. 위에서부터 처음 맞는 그룹을 씁니다.

### 보낼 곳 설정

Kubernetes provider를 켠 설정에 다음을 추가하세요.

```yaml
watch:
  problems_only: true # 선택: 경고만 받기
  outputs:
    - name: discord
      type: discord
      language: ko # ko 또는 en
      url_file: /etc/opsgate/discord-webhook-url
    - name: custom
      type: webhook
      url: https://alerts.example.com/ingest
      header_files:
        Authorization: /etc/opsgate/alerts-authorization
      body_template: |
        {"message": {{json .Text}}, "sent_at": {{json .SentAt}}, "alerts": {{json .Alerts}}}
```

필요한 곳만 남기면 됩니다. Discord 채널에서 만든 웹훅 URL은 `url_file`에 저장하고, 인증 헤더
파일에는 `Bearer TOKEN`처럼 값 전체를 넣으세요. 파일은 시작할 때 한 번 읽으므로 바꾸면 watch를
다시 시작해야 합니다. Kubernetes에서는 Secret을 파일로 마운트해서 쓰세요.

```sh
opsgate watch -config opsgate.yaml
```

`watch.problems_only: true`로 두면 재시작, `OOMKilled`, 이미지 받기 실패, 배포 정체 같은
경고만 받습니다. 이미지나 replica, ConfigMap 키 변경, 리소스 생성과 삭제, 노드 복구 같은
변경 알림은 모두 빠집니다. 출력 하나에만 걸면 그 출력에만 적용됩니다. 알림 종류를 하나씩
골라 끄는 기능은 없습니다.

보낼 곳마다 큐, 쿨다운, 재시도가 따로 돕니다. Discord 메시지에는 발생 시각(UTC), 클러스터,
namespace와 리소스, 이유와 요약이 들어가고, 너무 길면 Discord 제한에 맞춰 자른 뒤 잘렸다고
표시합니다. 커스텀 템플릿은 `json` 함수로 값을 감싸서 올바른 JSON이 나오게 해야 합니다. 전송
제한, HMAC 서명, 전체 설정은 [watch 알림 문서](docs/ko/watch.md)에 있습니다.

### 알림 받은 뒤 조사하기

받은 알림을 MCP 클라이언트에 붙여 넣고 이렇게 부탁해 보세요.

> 이 알림을 opsgate MCP로 조사해줘. 관련 운영 기록, 리소스 상태와 이벤트,
> 현재·직전 컨테이너 로그를 확인하고, 확인된 원인과 추정 원인,
> 추가로 확인할 것을 나눠서 알려줘.

조사는 클라이언트 쪽 AI가 조회 도구로 합니다. 무엇을 볼 수 있는지는 켜 둔 provider와 접근
정책에 따라 다릅니다. 파드가 이미 지워졌거나 로그와 이벤트가 만료됐으면 원인을 못 찾을 수도
있습니다. 변경은 평소처럼 승인을 거칩니다.

### 자동 분석 (선택)

실험 기능인 `opsgate-analyzer`는 별도 프로세스입니다. 서명된 watch 알림을 받으면 모델이 허용된
조회 도구로만 조사하게 하고, 그 보고서를 Discord로 보냅니다. 알림은 먼저 보내고 보고서는
나중에 따로 보내므로, 분석 때문에 알림이 늦어지지 않습니다. 반복되는 알림은 묶고 조사량에
한도를 두며, 쓰기 도구는 부르지 않고 자기 제안을 직접 실행하지도 않습니다. 모델은
`model.backend`로 OpenAI 호환 API나 설치된 Claude Code, Codex CLI 중에서 고릅니다. 일반 watch와
MCP 서버는 모델 없이 동작합니다.

**데이터가 클러스터 밖으로 나갑니다.** 알림 내용과, 로그와 노트를 포함한 조사 결과는 고른 모델
제공자로 갑니다. 보고서는 Discord와, 설정했다면 전달 대상으로 갑니다. 보내기 전에 알림 문자열과
보고서에서 민감 정보를 가리지만 모든 비밀 값을 찾아낼 수는 없습니다. 이 서비스들에 보내도 되는
데이터만 볼 수 있게 허용하세요.

보고서 언어와 조치 제안 여부는 `report.language`, `report.suggest_actions`로 정합니다.
[자동 분석 문서](docs/ko/analyzer.md)를 참고하세요.

### 실패한 이미지 되돌리기 (선택)

클러스터 설정에 `restore_image`(`namespace/deployment` 허용 목록)와 `action_approval_url`을
넣으면 켜집니다. 파드가 `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`으로 실패하면
`k8s_remediation_options`가 이전 이미지로 되돌리는 승인 링크를 줍니다. 조건은 같은 Deployment의
이전 ReplicaSet에 준비된 파드가 있고, 그 파드에서 대상 컨테이너가 그 이미지로 잘 돌고 있는
것입니다. 다만 준비 상태라고 애플리케이션이 정상이라는 보장은 없고, 레지스트리의 태그는 바뀔
수 있습니다. 새 배포가 계속 실패하거나 멈췄을 때도 같은 규칙이 적용되고, 선택지 페이지에도
나오며, analyzer가 보고서에 붙이기도 합니다.

링크를 열면 로그인한 뒤 새로 계산한 계획이 나옵니다. 사람이 승인하기 전에는 아무것도 바뀌지
않고, 패치 직전에 계획을 다시 확인합니다. 바뀌는 건 그 컨테이너의 이미지뿐이고 설정은
그대로입니다. 결과에는 이전 이미지로 돌아가는 되돌리기 링크가 붙습니다.



## 다른 도구와 무엇이 다른가

| 원하는 것 | 흔히 쓰는 것 | opsgate는 |
| --- | --- | --- |
| AI에게 Kubernetes를 폭넓게 맡기기 | [kubernetes-mcp-server](https://github.com/containers/kubernetes-mcp-server), [mcp-server-kubernetes](https://github.com/Flux159/mcp-server-kubernetes) 같은 범용 Kubernetes MCP 서버 | 되돌릴 수 있는 조치만 정해진 목록으로 줍니다. 조치마다 계획을 세우고, AI 앱 밖에서 패스키로 승인받고, 실행 직전에 다시 확인합니다 |
| AI가 제안한 변경을 승인하기 | [Kubernetes MCP Guard](https://github.com/mirusser/Kubernetes-MCP-Guard) 같은 승인형 MCP 서버 | 같은 승인 흐름을 알림에도 씁니다. 파드가 실패하면 지금 상태에 맞는 선택지와 함께 폰으로 알림이 오고, 조치 뒤에는 복구됐는지까지 알려 줍니다 |
| 조치 버튼이 달린 알림 | [Robusta](https://github.com/robusta-dev/robusta)처럼 채팅에 조치 버튼을 붙이는 알림 도구 | 채팅 버튼이 아니라 어느 브라우저에서든 패스키로 승인합니다. 같은 서버가 그대로 원인 분석용 MCP 서버가 됩니다 |
| 폰으로 서버에 접속하기 | SSH 앱 | 명령을 칠 필요가 없습니다. 조치를 고르고, 계획을 읽고, 승인하면 됩니다 |

opsgate는 혼자 또는 작은 팀이 직접 운영하는 클러스터나 서버에 맞춰져 있습니다. 팀용 온콜
도구, 대시보드, 범용 자동화 도구를 대신하지는 않습니다.



## 자주 묻는 질문

**Claude나 ChatGPT에게 Kubernetes 재시작을 맡겨도 괜찮을까요?**
정해 둔 범위 안에서라면 괜찮습니다. opsgate를 커넥터나 MCP 서버로 연결하고 대상 워크로드를
`rollout_restart`에 넣으세요. AI가 요청하는 재시작은 모두 승인 카드가 되고, 패스키로 승인해야
실행됩니다.

**AI가 스스로 승인해 버릴 수는 없나요?**
기본 설정에서는 못 합니다. builtin 로그인을 쓰면 승인은 AI 앱 안이 아니라, 패스키(또는
GitHub/OIDC)로 로그인한 웹 페이지에서만 합니다. 서버는 실행 직전에 계획을 다시 계산하고,
승인한 내용과 다르면 실행하지 않습니다.

**AI에게 셸이나 kubectl, Secret을 넘기나요?**
아니요. 셸 도구도, 아무 kubectl 명령이나 실행하는 도구도, Secret을 읽는 도구도 없습니다.
결과는 민감 정보를 가리고 길이를 제한하며, 모든 호출을 감사 로그에 남깁니다.

**폰으로는 어떻게 승인하나요?**
폰에서 `/approvals`를 열고 알림을 켜세요. iPhone은 먼저 홈 화면에 추가해야 합니다. 알림이
오면 눌러서 승인하면 됩니다. `opsgate init -recover`가 이 설정을 대신 해 줍니다.
[폰으로 복구하기](#빠른-시작-폰으로-복구하기)를 참고하세요.

**조치를 했는데도 안 고쳐지면요?**
opsgate가 조치 뒤에 워크로드를 지켜보고, 복구됨 또는 아직 복구되지 않음을 알려 줍니다. 안
됐을 때는 `CrashLoopBackOff` 같은 이유도 함께 보여 줍니다. 이미지 되돌리기와 파드 수 조정에는
되돌리기 버튼이 있습니다.

**Kubernetes가 꼭 있어야 하나요?**
주로 Kubernetes를 위한 도구입니다. 선택 기능인 호스트 provider로 서버 한 대의 systemd 유닛과
Docker 컨테이너도 다룰 수 있습니다. [호스트 provider](docs/ko/host.md)를 참고하세요.

**어떤 AI 앱에서 쓸 수 있나요?**
MCP를 지원하면 다 됩니다. claude.ai와 ChatGPT는 커넥터(OAuth를 쓰는 HTTP)로, Claude Code나
Cursor는 HTTP나 stdio로 연결합니다.

**돈이 드나요?**
안 듭니다. Apache-2.0 라이선스이고, 내가 운영하는 인프라에서 돌아갑니다.



## 보안 모델 요약

- 인스턴스 하나가 신뢰 범위 하나입니다. `ops:read`는 켜 둔 provider 전체에 적용됩니다. 운영
  서버와 개인 노트처럼 믿을 수 있는 정도가 다른 대상은 인스턴스와 OS 계정을 나누세요.
- 스코프, 서버 도구 정책, 사용자별 도구 목록, provider 허용 목록을 차례로 검사합니다. 그 위에서
  OS 권한과 Kubernetes RBAC가 마지막으로 범위를 막습니다.
- builtin 로그인에서는 기본으로 브라우저 승인만 받습니다. `approval.require_web: false`로 AI 앱
  안 승인을 켜면 침해된 클라이언트가 스스로 승인할 수 있고, 이때 서버가 시작하면서 경고를
  남깁니다.
- 선택 기능인 analyzer는 알림과 조사 결과를 고른 모델 제공자에게 보냅니다. 민감 정보를 가리긴
  하지만 완전히 막지는 못합니다.

자세한 내용: [보안 정책](docs/ko/security.md)



## 문서

| 문서 | 내용 |
| --- | --- |
| [구조](docs/ko/architecture.md) | 요청 흐름, 패키지, 설계 원칙 |
| [인증](docs/ko/authentication.md) | 패스키, GitHub, OIDC, 외부 JWT, 고정 토큰, stdio |
| [승인](docs/ko/approval.md) | 계획과 실행, 승인 방식과 시간 창, 조치 링크, 장애별 선택지, 승인함과 푸시, 복구 확인, 되돌리기 |
| [클라이언트 설정](docs/ko/clients.md) | Claude Code, Claude Desktop, claude.ai, ChatGPT, Cursor |
| [설정](docs/ko/configuration.md) | 설정 키와 기본값, `opsgate init`, 감사 로그 형식 |
| [도구](docs/ko/tools.md) | 도구 인자, 제한, 민감 정보 가리기 |
| [노트](docs/ko/notes.md) | Markdown 폴더에 런북과 장애 기록 남기기 |
| [호스트 provider](docs/ko/host.md) | Kubernetes 없이 systemd 유닛과 Docker/Compose 컨테이너 다루기, polkit과 소켓 프록시 설정 |
| [배포](docs/ko/deploy.md) | 릴리스 검증, Kubernetes, 리버스 프록시 |
| [Watch 알림](docs/ko/watch.md) | `opsgate watch`로 Discord, 웹훅, 이메일, 승인함에 알림 보내기, namespace 그룹 |
| [자동 분석](docs/ko/analyzer.md) | 선택 기능인 모델 기반 조사, 조회 전용 MCP 인증과 조사 한도 |
| [안정성](docs/ko/stability.md) | 유지하는 부분, 실험 기능, 바꿀 때 알리는 방법 |
| [로드맵](docs/ko/roadmap.md) | 지금까지 한 것과 앞으로의 계획 |



## 기여와 라이선스

[CONTRIBUTING.md](CONTRIBUTING.md), [행동 강령](CODE_OF_CONDUCT.md),
[변경 기록](CHANGELOG.md)을 참고하세요. 라이선스는 [Apache-2.0](LICENSE)이고, 저작권은
2026 The opsgate Authors에 있습니다.
