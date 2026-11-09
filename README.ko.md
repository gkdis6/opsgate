<p align="center"><img src="docs/images/logo.png" alt="opsgate 마스코트: 승인 체크 패를 든 해태" width="240"></p>

# opsgate

[English](README.md)

**서버가 고장 나면 먼저 알고, 어디서든 고칩니다. AI에게 셸을 내주지 않고도.**

opsgate는 혼자 서버를 운영하는 사람을 위해 Go로 작성한 오픈 소스 운영 관문입니다.
Kubernetes를 지켜보다가 문제가 생기면 알리고, 폰이나 컴퓨터에서 패스키로 조치를
승인하게 합니다. 서비스를 되살린 뒤에는 Claude, ChatGPT, Claude Code, Cursor 같은
AI 클라이언트가 opsgate MCP 서버로 원인을 조사합니다. MCP 서버는 정해진 도구만
제공하며, 정책을 명시하고 변경에는 사람의 승인을 받고 감사 로그를 남깁니다.

> **상태:** 초기 릴리스이며, 노트, 호스트 provider, watch, 승인함, analyzer는 실험적 기능입니다. 변경 작업에는 사용자 권한 검사,
> 허용 대상 제한, 사람의 승인이 적용됩니다. 조회 범위 제한과 출력 마스킹으로 민감 정보
> 노출을 줄입니다. 외부 보안 검토는 아직 받지 않았으며, 보호 범위와 남아 있는 한계는
> [보안 정책](docs/ko/security.md)에 설명합니다.



## 해결하려는 문제

혼자 서버를 운영하면 보통 두 가지 빈틈이 생깁니다.

- **문제를 늦게 압니다.** 알림을 제대로 갖춰 두지 않으면 사용자의 연락을 받거나
  우연히 발견하고 나서야 장애를 압니다.
- **고치는 방법은 알지만 서버에 접근할 수 없습니다.** 폰용 SSH 앱이 있어도, 장애
  중에 폰으로 명령을 입력하는 일은 느리고 실수하기 쉽습니다.

AI 어시스턴트에게 셸을 주면 두 번째 빈틈은 메워지지만, 잘못되거나 되돌릴 수 없는
명령 하나라는 더 큰 위험이 생깁니다. opsgate는 두 빈틈을 모두 메우면서 모든 변경을
사람 앞에 둡니다.



## 장애를 처리하는 흐름

1. **먼저 압니다.** `opsgate watch`가 Kubernetes API를 지켜보다가 문제가 생기면 바로
   Discord, 웹훅, 이메일로 알립니다. 알림을 승인함에 올리고 휴대폰으로 푸시를 보낼
   수도 있습니다. [Watch 알림](#watch-알림)을 참고하세요.
2. **어디서든 먼저 살립니다.** 푸시를 누르거나 알림의 **조치 선택** 링크를 엽니다.
   패스키로 로그인하면 마지막으로 정상 동작한 이미지 복원, 워크로드 재시작처럼 현재
   상태에 맞는 조치가 보입니다. 하나를 골라 새 실행 계획을 확인하고 승인하면 바로
   실행됩니다. 그 뒤 opsgate가 워크로드를 지켜보고, 변경이 적용됐다는 것뿐 아니라
   실제로 **복구됐는지** 알려 줍니다. 되돌릴 수 있는 조치라면 **되돌리기** 버튼이
   나타납니다. 폰 브라우저만 있으면 됩니다.
3. **그다음 원인을 찾습니다.** 서비스가 돌아오면 AI 클라이언트에 opsgate 조회 도구로
   조사하도록 요청하거나, 선택 기능인 [analyzer](docs/ko/analyzer.md)가 먼저 살펴보게
   합니다.
4. **기록해 둡니다.** 선택 기능인 note provider가 런북과 지난 장애 기록을 일반
   Markdown으로 보관하고, AI에 과거 기록을 먼저 참고하고 결과를 기록할지 제안하도록
   안내합니다. [노트](docs/ko/notes.md)를 참고하세요.



## 설계 원칙


### 임의 명령을 실행하지 않습니다

목적이 정해진 도구만 있습니다. 셸 도구도,
Kubernetes Secret 조회 도구도 없습니다.

### 모든 기능이 꺼진 상태로 시작합니다

provider, 쓰기 작업, 허용 목록은 설정에서
직접 켜기 전까지 동작하지 않습니다.

### 변경에는 사람의 승인이 필요하고, 승인하면 실행됩니다

쓰기 도구는 먼저 바꿀 내용을 정확히 계산합니다. 사람이 승인하면(기본값은 로그인 후
웹 페이지에서 승인) 서버가 실행 계획을 다시 확인하고 한 번 실행합니다. 승인했다고 채팅에
다시 알릴 필요가 없습니다. 선택 기능인 승인 시간 창은 아래에서 설명합니다.

### 조치는 카탈로그에서만 나옵니다

알림, 선택지 페이지, AI 제안이 내밀 수 있는 조치는 정해진 카탈로그에 있는 것뿐입니다.
재시작, 파드 수 조정, 이미지 복원, 컨트롤러가 다시 만드는 파드의 삭제입니다. 규칙이나
AI가 제안할 수 있고 버튼에 누가 제안했는지 표시하며, 실행 여부는 허용 목록과 사람이
정합니다.

### 데이터를 영구 삭제하지 않습니다

노트를 지우면 `.trash`로 옮깁니다. 삭제 작업은 파드 삭제 하나뿐이며, 별도 허용 목록과
승인이 필요합니다. 컨트롤러가 다시 만들지 않는 파드라면 실행 계획에서 경고하고, 링크로는
삭제할 수 없습니다.

### 원격 접속은 인증을 거칩니다

내장 OAuth 2.1 서버가 claude.ai와 ChatGPT
커넥터를 지원합니다. 패스키, GitHub, OIDC(Google 포함)로 로그인하거나 외부 JWT를
검증하도록 설정할 수 있습니다.

### 조회 결과는 분석 자료로 취급합니다

AI 클라이언트에는 로그나 조회 결과 안의 지시문을
따르지 않도록 안내합니다. 반환 결과에는 민감 정보 마스킹과 길이 제한을 적용합니다.
누가 어떤 도구를 호출했고 어떻게 처리됐는지는 한 줄에 JSON 하나씩 감사 로그에 기록합니다.



## 앞으로의 방향

현재 [로드맵](docs/ko/roadmap.md)의 모든 단계는 v0.4.0으로 릴리스했습니다.
PostgreSQL provider, 설정 다시 불러오기 같은 남은 아이디어는 약속 없이 로드맵에 적어 두었습니다.



## 할 수 있는 일

각 기능 묶음을 provider라고 부르며, 필요한 것만 설정에서 켤 수 있습니다.

| 기능(provider) | 조회 도구 | 변경 도구(승인 필요) |
| --- | --- | --- |
| Kubernetes | 클러스터, 개요, 목록, 상세, 환경 변수, 서비스 점검, 이벤트, 파드 로그, 조치 옵션 | 워크로드 재시작, 실행할 파드 수 조정, 파드 삭제, 이전 이미지 복원 |
| 호스트: Kubernetes 없는 서버의 systemd 유닛과 Docker/Compose 컨테이너 *(실험적, 기본값 꺼짐)* | 유닛, 유닛 로그, 컨테이너, 컨테이너 로그 | 유닛 재시작, 컨테이너 재시작 |
| 노트: 런북과 메모리, Markdown 폴더 또는 Obsidian 볼트 *(실험적)* | 검색, 읽기, 목록, 최근 노트, 링크, 태그 | 작성, 내용 추가, 내용 수정, 휴지통으로 이동 |

스코프는 사용자가 opsgate를 통해 수행할 수 있는 작업의 종류입니다. `ops:read`는 조회, `ops:write`는
운영 대상과 노트 변경에 사용합니다. 허용 목록은 접근할
도구나 변경할 Kubernetes 대상 등을 설정으로 제한하는 장치입니다. 스코프가 있어도
설정에서 허용하지 않은 작업은 실행할 수 없습니다.

노트 작성은 설정한 노트 폴더 안에서만 가능합니다. 일반 파일을 서버의 임의 경로에
만들거나 Kubernetes의 CPU·메모리 requests·limits를 변경하는 도구는 없습니다.
requests·limits 설정값은 Kubernetes 상세 조회로 확인할 수 있습니다.
전체 인자와 제한은 [도구 문서](docs/ko/tools.md)를 참고하세요.

### Kubernetes 변경

Kubernetes 변경은 허용 목록에 대상을 적기 전까지 모두 꺼져 있고, 실행은 언제나 사람이
승인합니다. 모든 변경은 MCP 클라이언트에서 요청하거나 링크로 열 수 있습니다(링크를 쓰려면
클러스터에 `action_approval_url`을 설정합니다).

| 변경 | 켜는 설정 | 선택지 페이지가 제안하는 경우 | 되돌리기 |
| --- | --- | --- | --- |
| 워크로드 재시작 | `rollout_restart` | 컨테이너가 계속 재시작하거나(설정 오류, Job 제외) 메모리 제한에 계속 걸릴 때 | 없음 |
| 이전 이미지 복원 | `restore_image` | 이미지를 받을 수 없거나, 이전 ReplicaSet이 준비된 상태로 남아 있는데 새 배포가 계속 실패하거나 멈췄을 때 | 복원 전 이미지로 |
| 파드 삭제 | `delete_pod` | 컨트롤러 소유 파드가 종료 중에 멈췄거나 연결할 수 없는 노드에 있을 때 | 없음 |
| 파드 수 조정 | `scale` (`max_replicas`로 상한) | 규칙으로는 제안하지 않음. MCP 클라이언트나 AI 제안으로만 | 이전 파드 수로 |

링크를 열면 브라우저 로그인 후 새 실행 계획이 나오고, 승인 버튼을 누르기 전에는 아무것도
바뀌지 않습니다. 승인 시간 창은 링크에 적용되지 않습니다. 분석기는 조회 전용
`action_link`로만 해결 조치를 제안하고 영향이 가장 작은 조치를 우선하며, 그 버튼에는 AI
제안이라고 표시합니다. [카탈로그 조치 링크](docs/ko/approval.md#카탈로그-조치-링크)와
[장애별 현재 선택지](docs/ko/approval.md#장애별-현재-선택지)를 참고하세요.



## 동작 방식

<img src="docs/images/ko/architecture.svg" alt="클라이언트 연결, 인증과 도구 처리, provider 및 OS 계정·RBAC 신뢰 경계를 보여 주는 opsgate 구조" width="830">

AI 클라이언트가 조회 결과를 분석하고 다음 작업을 선택합니다. opsgate MCP 서버는 AI 모델을
실행하거나 호출하지 않으며, 요청받은 도구의 권한과 대상을 검사하고 실행합니다.
선택 기능인 별도 프로세스 `opsgate-analyzer`만 직접 지정한 모델을 호출합니다.

Kubernetes 도구는 kubeconfig의 접속 정보나 클러스터 내부 인증으로 Kubernetes API에
접속합니다. 따라서 로컬 PC에서 실행해도 네트워크 연결과 접근 권한이 있으면 원격
클러스터를 조회할 수 있습니다.

클라이언트는 로컬 stdio(클라이언트가 opsgate를 실행하고 표준 입출력으로 통신하는 방식)
또는 TLS 리버스 프록시를 거치는 HTTPS로 연결합니다.
opsgate는 신원과 정책을 확인하고, 쓰기 작업을 계획한 뒤 승인을 받아 실행합니다.
이후 출력을 처리하고 감사 로그를 남깁니다.
실제 접근 범위는 OS 계정과 Kubernetes RBAC가 제한합니다.

자세한 내용: [구조](docs/ko/architecture.md) · [승인](docs/ko/approval.md) ·
[인증](docs/ko/authentication.md)

### 승인 흐름

변경 도구는 먼저 대상과 변경 내용을 확인해 실행 계획을 만듭니다. 웹 승인을 사용할 수
있으면 기본적으로 사용자가 웹 승인 페이지에서 내용을 확인하고 승인해야 실행합니다.
`approval.require_web: false`를 명시하면 클라이언트 내 확인을 허용하며, 침해된 클라이언트가
자기 쓰기를 스스로 승인할 수 있습니다. external/static 인증과 stdio는 웹 채널이 없어
클라이언트 내 확인을 유지합니다. 모드 파일도 `require_web`을 생략하면 `-transport stdio`에서
클라이언트 내 확인을 허용하지만, 명시적 `true`는 stdio 쓰기를 거부합니다.
일회성 승인은 해당 인자와 계획에만 유효합니다.

브라우저에서 승인하면 실행 계획을 다시 확인하고 즉시 한 번 실행합니다. MCP 호출은
`approval.wait` 동안(웹 승인 시 기본 25s, 최대 55s) 기다리다가 그 안에 승인되면 결과를
돌려줍니다. 그렇지 않으면 AI가 나중에 `approval_status`로 결과를 조회합니다. 같은 호출을
`approval_id`와 함께 다시 보내도 저장된 결과만 돌려주고 다시 실행하지 않습니다.

### 어디서든 승인하기

- **승인함.** `/approvals`에서 대기 중이거나 최근 처리한 승인, 조치를 기다리는 알림과
  그 결과를 봅니다. 휴대폰이나 컴퓨터의 일반 브라우저에서 여세요. 메신저 내장 브라우저에서는
  패스키가 자주 실패합니다.
- **푸시.** `approval.push`를 켜면 채팅에서 요청한 변경이, `watch.inbox`까지 켜면 새 알림이
  기기로 Web Push를 보냅니다. 누르면 가장 최근의 대기 중인 승인 페이지가 바로 열립니다. 푸시에는 내용이 담기지 않습니다.
  iPhone은 먼저 opsgate를 홈 화면에 추가하세요.
- **되돌리기.** 파드 수 조정이나 이미지 복원을 실행하면 결과에 이전 상태로 돌아가는
  되돌리기 링크가 붙습니다. 이 링크도 새 실행 계획과 승인을 거치는 별도 조치입니다.

[승인함과 알림](docs/ko/approval.md#승인함과-알림)과
[되돌리기](docs/ko/approval.md#되돌리기)를 참고하세요.

선택적으로 승인 시간 창을 설정하면, 사용자가 특정 도구를 일정 시간 동안 승인할 수
있습니다. 그동안 같은 사용자의 해당 도구 호출은 승인한 대상에서 인자가 달라도 추가 승인 요청을
생략할 수 있습니다. 권한과 허용 대상 검사는 매번 수행하며, 기본값은 꺼짐입니다.
`k8s_restore_image`에는 승인 시간 창이 적용되지 않습니다.

<img src="docs/images/ko/approval-flow.svg" alt="쓰기 호출의 계획, 승인 시간 창·클라이언트·웹 승인, 실행 전 검증과 실행 및 거부 경로" width="830">

### 노트: 런북과 메모리

note provider를 활성화하면 AI에 조치 전 과거 기록을 검색하고, 처리 후 기록을 남길지
제안하도록 안내합니다. 실제 검색과 제안 여부는 AI 클라이언트가 결정하며, 자동 실행을
보장하지는 않습니다. 노트 변경에도 승인이 적용됩니다. 저장한 기록은 다음 조사에서
다시 검색해 참고할 수 있습니다.
[노트](docs/ko/notes.md)를 참고하세요.

<img src="docs/images/ko/note-memory.svg" alt="upstream timeout 검색으로 지난 조치를 참고하고, 사람의 승인 후 실행과 노트 기록을 거쳐 다음 조사에 활용하는 흐름" width="830">



## 빠른 시작: 로컬(stdio)

[릴리스](https://github.com/gkdis6/opsgate/releases)에서 압축 파일을 받아
[검증](docs/ko/deploy.md#설치-전-릴리스-검증)한 뒤 쓰거나, Go 1.26 이상으로 빌드하세요.

```sh
go install github.com/gkdis6/opsgate/cmd/opsgate@latest   # 또는: make build
```

이 `go install`은 `opsgate`만 빌드합니다. 선택 기능인 analyzer는 별도 명령
`./cmd/opsgate-analyzer`입니다.

현재 kubeconfig를 사용하는 설정 파일을 저장하세요. 아래 예제에서는 `/path/opsgate.yaml`을 사용합니다.

```yaml
stdio:
  scopes: [ops:read]
providers:
  kubernetes:
    enabled: true
    namespaces: [default]
```

Claude Code에 등록하세요. 경로는 절대 경로로 쓰세요.

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

stdio는 실행한 계정의 권한으로 동작하므로 관리자 권한이 없는 일반 계정으로 실행하세요. 다른
클라이언트는 [클라이언트 설정](docs/ko/clients.md)을 참고하세요.



## 빠른 시작: 원격(HTTP + 패스키)

패스키는 별도의 ID 제공자가 필요 없습니다. Face ID, Touch ID, Windows Hello,
휴대폰, 보안 키로 로그인할 수 있습니다.

`opsgate init`이 공개 URL, 로그인할 사람, 네임스페이스를 묻고 짧은 설정 파일을 만듭니다.

```sh
opsgate init            # opsgate.yaml을 만듭니다. 플래그로 미리 답할 수 있습니다(-h 참고)
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

나머지는 모드가 채웁니다. HTTP는 `0.0.0.0:8787`에서
받고, 내장 패스키 로그인을 쓰며, 상태를 `/var/lib/opsgate`에 두고, 브라우저 승인
링크와 파드의 ServiceAccount를 사용합니다. 적용되는 값 전체는
`opsgate config show -config opsgate.yaml`로 볼 수 있습니다.

설정 파일을 `deploy/kubernetes/opsgate-deployment.yaml`의 `opsgate` ConfigMap에 넣고 적용하세요. 매니페스트의 기본 예제는 Google 로그인이며, 패스키에는 선택 항목인 `opsgate-upstream` Secret이 필요 없습니다. Secret 준비, 이미지 검증, RBAC과 probe 설정은 [배포](docs/ko/deploy.md)를 따르세요.

```sh
kubectl apply -f deploy/kubernetes/opsgate-deployment.yaml
```

1. 처음 시작하면 패스키가 없는 사용자마다 일회용 등록 링크(24시간 유효)가 로그에
   남습니다. 링크를 열고 **Create passkey**를 누르세요. 링크를 새로 만들려면
   `kubectl -n opsgate exec deploy/opsgate -- /opsgate passkey invite -config /etc/opsgate/opsgate.yaml -subject passkey:alice`를 실행하세요.
2. 앞단에 `Host` 헤더를 그대로 전달하는 TLS ingress나 리버스 프록시를 두세요. 공개 호스트는
   `public_url`과 같아야 하며, 다른 호스트로 온 요청은 421로 거부됩니다.
3. 클라이언트를 `https://mcp.example.com/mcp`에 연결하세요. 예:
   `claude mcp add --transport http opsgate https://mcp.example.com/mcp`.
   claude.ai나 ChatGPT에서는 커넥터로 추가하세요.

생성된 파일은 클라이언트 리다이렉트 호스트(`allowed_redirect_hosts`)를 이미
제한합니다. 외부에 공개하기 전에 확인하세요. GitHub, Google 등 다른 로그인 방식은
[인증 문서](docs/ko/authentication.md)에, 프록시 경로·릴리스 검증은
[배포 문서](docs/ko/deploy.md)에 있습니다. 명령과 플래그 목록은 `opsgate -h`로 볼 수 있습니다.



## Watch 알림

MCP 서버와 함께 `opsgate watch`를 실행하면 Kubernetes의 문제와 변경 알림을
받을 수 있습니다. Kubernetes API를 감시하고 Discord, 커스텀 JSON 웹훅,
SMTP 이메일로 직접 전송하므로 n8n은 선택 사항입니다.

- `watch.approval_url`을 설정하면 파드 경고에 선택지 페이지로 가는 **조치 선택** 링크가
  붙습니다.
- `watch.inbox`를 설정하면 경고가 승인함에도 올라가고 휴대폰으로 푸시가 갑니다
  ([서명된 watch 알림](docs/ko/approval.md#서명된-watch-알림)).
- [namespace 그룹](docs/ko/watch.md#그룹)으로 namespace 묶음마다 출력, 경고 전용 필터,
  묶음 주기를 정합니다. 처음 일치한 그룹을 사용합니다.

### 전송 설정

Kubernetes provider가 켜진 설정에 다음 내용을 추가하세요.

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

필요한 전송 대상만 남기면 됩니다. Discord 채널에서 만든 수신 웹훅 URL을
`url_file`에 저장하고, 인증 헤더 파일에는 `Bearer TOKEN`처럼 전체 값을 넣으세요.
파일은 시작할 때 읽으므로 변경 후 watch를 재시작해야 합니다. Kubernetes에서는
Secret을 파일로 마운트해 사용하세요.

```sh
opsgate watch -config opsgate.yaml
```

`watch.problems_only: true`는 재시작, `OOMKilled`, 이미지 받기 실패, 롤아웃 정체 같은
경고만 남깁니다. 이미지, replica, ConfigMap 키 변경, 오브젝트 생성·삭제, 노드 복구 같은
변경 알림은 모든 전송 대상에서 빠집니다. 출력 하나에 설정하면 그 대상에만 적용됩니다.
알림 이유를 하나씩 골라 끌 수는 없습니다.

각 대상은 별도 큐·쿨다운·재시도를 사용합니다. Discord 메시지에는 발생 시각(UTC),
클러스터, namespace/리소스, 오류 이유와 요약이 포함되며, 긴 배치는 Discord 제한에
맞춰 잘림을 표시합니다. 커스텀 템플릿은 `json` 함수로 값을 안전하게 인코딩하고
유효한 JSON을 만들어야 합니다. 전송 대상은 `watch.outputs`와 `watch.email`로
설정합니다. 전송 제한, HMAC 서명과 전체 설정은
[watch 알림 문서](docs/ko/watch.md)를 참고하세요.

### 알림 조사하기

받은 알림을 MCP 클라이언트에 붙여넣고 다음처럼 요청할 수 있습니다.

> 이 알림을 opsgate MCP로 조사해줘. 관련 운영 기록, 리소스 상태와 이벤트,
> 현재·직전 컨테이너 로그를 확인하고, 확인된 원인과 추정 원인,
> 추가 확인 사항을 구분해서 알려줘.

조사는 클라이언트의 AI가 조회 도구로 수행합니다. 확인할 수 있는 근거는 활성화된 provider와
접근 정책에 따라 달라지며, Pod 삭제나 로그·이벤트 만료로 원인을 확정하지 못할 수 있습니다.
변경은 일반 승인 절차를 따릅니다.

### 자동 분석 (선택)

실험 기능인 별도 프로세스 `opsgate-analyzer`는 서명된 watch 알림을 받아, 모델이 허용 목록의
조회 전용 MCP 도구로 조사하게 한 뒤 Discord에 보고서를 보냅니다. 알림을 먼저 보내고
보고서는 별도 메시지로 보내므로 분석 때문에 알림이 늦어지지 않습니다. 반복 알림을 묶고
조사 한도를 적용하며, 쓰기 도구를 호출하거나 자신의 제안을 실행하지 않습니다.
`model.backend`로 OpenAI 호환 API나 설치된 Claude Code·Codex CLI를 선택합니다. 일반 watch와
MCP 사용에는 모델이 필요하지 않습니다.

**데이터가 클러스터 밖으로 나갑니다.** 알림 내용과 도구 결과는 analyzer가 읽을 수 있는 로그와
노트를 포함해 선택한 모델 제공자로, 보고서는 Discord와 선택적으로 설정한 전달 수신자로 전송됩니다. 모든 모델 backend에서 알림 문자열을 JSON으로 인코딩하기 전에 마스킹하고, 두 알림 전송 경로에 보내기 전에 보고서를 마스킹합니다. 모든 비밀 값을 찾아낼 수는 없습니다. 이 서비스들에 보내도 되는 데이터만 접근을 허용하세요.

보고서 언어와 조치 제안은 `report.language`와 `report.suggest_actions`로 설정합니다.
[자동 분석 문서](docs/ko/analyzer.md)를 참고하세요.

### 실패한 이미지 복원 (선택)

Kubernetes 클러스터에 `restore_image`(`namespace/deployment` 허용 목록)와
`action_approval_url`을 설정하면 켜집니다. 파드가 `ImagePullBackOff`, `ErrImagePull`,
`InvalidImageName`으로 실패할 때, 같은 Deployment의 삭제 중이 아닌 ReplicaSet이 소유한 준비된 파드에서 대상 컨테이너가 준비되어 제안 이미지를 실행 중이면 `k8s_remediation_options`가 승인 링크를 돌려줍니다. 준비 상태는 애플리케이션 정상 동작을 보장하지 않으며 레지스트리의 이미지 태그는 바뀔 수 있습니다. 같은 규칙이 계속 실패하거나 멈춘 새 배포에도 적용되고, 선택지 페이지에 나타나며, analyzer가 보고서에 붙일 수도 있습니다.

링크를 열려면 로그인해야 하고, 그때 새로 계산한 계획이 표시됩니다. 사람이 승인하기 전에는
아무것도 바뀌지 않으며, 패치 직전에 계획을 다시 확인합니다. 해당 컨테이너의 이미지만 바뀌고
설정은 되돌리지 않습니다. 결과에는 이전 이미지로 돌아가는 되돌리기 링크가 붙습니다.



## 보안 모델 요약

- 인스턴스 하나가 신뢰 그룹 하나입니다. `ops:read`는 켜진 provider 전체에 적용됩니다.
  운영 서버와 개인 노트처럼 신뢰 수준이 다른 대상은 인스턴스와 OS
  계정을 나누세요.
- 스코프, 서버 도구 정책, 사용자별 도구 목록, provider 허용 목록을 차례로 검사하고,
  마지막으로 OS 권한과 Kubernetes RBAC가 접근을 제한합니다.
- HTTP에서는 `approval.web`이 켜져 있으면 기본적으로 브라우저 승인을 요구합니다.
  `approval.require_web: false`를 명시하면 클라이언트 내 확인을 허용하며, 침해된 클라이언트가
  자기 쓰기를 스스로 승인할 수 있습니다. HTTP 시작 시 경고를 남깁니다.
- 선택 기능인 analyzer는 알림과 조사한 근거를 선택한 모델 제공자로 보냅니다. 마스킹으로
  노출을 줄이지만 완전히 막는 것은 아닙니다.

자세한 내용: [보안 정책](docs/ko/security.md)



## 문서

| 문서 | 내용 |
| --- | --- |
| [구조](docs/ko/architecture.md) | 요청 흐름, 패키지, 설계 원칙 |
| [인증](docs/ko/authentication.md) | 패스키, GitHub, OIDC, 외부 JWT, 고정 토큰, stdio |
| [승인](docs/ko/approval.md) | 계획과 실행, 승인 경로와 시간 창, 조치 링크, 장애별 선택지, 승인함과 푸시, 되돌리기 |
| [클라이언트 설정](docs/ko/clients.md) | Claude Code, Claude Desktop, claude.ai, ChatGPT, Cursor |
| [설정](docs/ko/configuration.md) | 모든 설정 키와 기본값, 감사 로그 형식 |
| [도구](docs/ko/tools.md) | 도구 인자, 제한, 마스킹 |
| [노트](docs/ko/notes.md) | Markdown 폴더에 런북과 장애 기록 남기기 |
| [호스트 provider](docs/ko/host.md) | Kubernetes 없이 systemd 유닛과 Docker/Compose 컨테이너 다루기, polkit과 소켓 프록시 설정 |
| [배포](docs/ko/deploy.md) | 릴리스 검증, Kubernetes, 리버스 프록시 |
| [Watch 알림](docs/ko/watch.md) | 별도 `opsgate watch` 프로세스로 Kubernetes 알림을 Discord·커스텀 JSON 웹훅·이메일·승인함으로 전송, namespace 그룹 |
| [자동 분석](docs/ko/analyzer.md) | 선택적 모델 worker, 조회 전용 MCP 인증과 조사 한도 |
| [로드맵](docs/ko/roadmap.md) | 완료한 기능과 앞으로의 계획 |



## 기여와 라이선스

[CONTRIBUTING.md](CONTRIBUTING.md), [행동 강령](CODE_OF_CONDUCT.md),
[변경 이력](CHANGELOG.md)을 참고하세요. 라이선스는 [Apache-2.0](LICENSE)이며,
저작권은 2026 The opsgate Authors에 있습니다.
