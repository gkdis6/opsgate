# opsgate 배포

[English](../deploy.md)

호스트 점검에는 Linux 네이티브 바이너리를 사용하고, Kubernetes 점검에는 컨테이너를 사용합니다.
distroless 이미지에는 셸, sudo, systemctl, journalctl이 없습니다.
pod 안에서 실행하는 host provider는 노드의 상태를 나타내지 않습니다.
Darwin 빌드는 로컬 클라이언트용으로 제공합니다. systemd 배포에는 Linux가 필요합니다.

Streamable HTTP, OAuth, 인증, 사람의 승인, 쓰기 도구가 구현되어 있습니다.
이 checkout은 첫 공개 릴리스를 준비하고 있으며, 제3자 보안 검토는 아직 진행되지 않았습니다.
선택 사항인 note 및 dev provider는 실험적입니다.
외부에 노출하기 전에 인증, 승인, 프록시 동작을 검증합니다.
업스트림 로그인 콜백은 정확히 `<public_url>/oauth/callback`입니다.

## 설치 전 릴리스 검증

공식 배포 경로에서 cosign과 GitHub CLI를 설치합니다.
실제 릴리스 태그와 운영체제 및 아키텍처에 맞는 아카이브를 선택합니다.

```sh
TAG=v0.2.0 # Replace with the release you intend to install.
ARCH=amd64 # Or arm64.
gh release download "$TAG" --repo gkdis6/opsgate \
  --pattern "opsgate_${TAG#v}_linux_${ARCH}.tar.gz" \
  --pattern checksums.txt --pattern checksums.txt.sigstore.json
cosign verify-blob \
  --certificate-identity-regexp '^https://github\.com/gkdis6/opsgate/\.github/workflows/release\.yml@refs/tags/v[^/]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --check --ignore-missing checksums.txt
gh attestation verify "opsgate_${TAG#v}_linux_${ARCH}.tar.gz" --repo gkdis6/opsgate
gh attestation verify checksums.txt --repo gkdis6/opsgate
```

이전 명령이 성공한 뒤에만 다음 명령을 실행합니다.
더 엄격하게 신원을 검증하려면 정규식 플래그를 `--certificate-identity`로 바꾸고,
`@refs/tags/$TAG`로 끝나는 전체 워크플로 URL을 지정합니다.
macOS에서는 `shasum -a 256`을 사용해 선택한 아카이브의 다이제스트를 검증된 체크섬 파일의 해당 항목과 비교합니다.
서명, 체크섬, 증명(attestation)을 모두 검증한 뒤에만 아카이브를 추출하고 실행합니다.
릴리스 자산에는 Syft가 생성한 아카이브 SBOM도 포함됩니다.

컨테이너는 릴리스 또는 레지스트리에서 매니페스트 다이제스트를 가져옵니다.
배포에 사용하기 전에 이 불변 참조를 검증합니다.

```sh
IMAGE=ghcr.io/gkdis6/opsgate@sha256:REPLACE_WITH_MANIFEST_DIGEST
cosign verify \
  --certificate-identity-regexp '^https://github\.com/gkdis6/opsgate/\.github/workflows/release\.yml@refs/tags/v[^/]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com "$IMAGE"
```

GoReleaser는 체크섬에 Sigstore 번들을 사용하는 키리스 `sign-blob`을 적용하고,
`docker_signs`로 컨테이너 다이제스트에 서명합니다.
[서명 문서](https://goreleaser.com/customization/sign/sign/)를 참고합니다.

## TLS 리버스 프록시와 Google 로그인

`http.public_url`을 외부 **HTTPS 오리진**으로 설정합니다. 예를 들어 `https://mcp.example.com`입니다.
`/mcp`나 다른 경로를 덧붙이지 않습니다.
클라이언트는 `https://mcp.example.com/mcp`에 연결합니다. 이 URL은 OAuth 리소스 식별자이기도 합니다.
Google은 사용자를 인증하고, opsgate는 MCP 액세스 토큰을 발급합니다.

Google OIDC 웹 애플리케이션을 생성하고 issuer로 `https://accounts.google.com`을 사용합니다.
정확한 업스트림 콜백 URL **`<public_url>/oauth/callback`**을 identity provider에 등록합니다.
예를 들어 `https://mcp.example.com/oauth/callback`입니다.
클라이언트 ID와 명시적인 허용 목록을 설정합니다.
허용 목록에는 검증된 이메일 subject(`email:operator@example.com`) 또는 정확한 `oidc:<sub>` subject를 지정합니다.
Google 이메일 신원은 Gmail/Googlemail 계정이거나, 이메일 도메인과 일치하는 Workspace `hd` 클레임이 있어야 합니다.
그 밖의 Google 계정은 `oidc:<sub>`를 사용해야 합니다.
클라이언트 시크릿은 파일이나 Kubernetes Secret에 저장합니다.
YAML, 명령 인자, 소스 관리에 직접 넣지 않습니다.
`ops:write`는 필요한 운영자에게만 부여합니다.
각 쓰기 작업에는 허용된 대상, OS/RBAC 권한, 사람의 승인도 필요합니다.

호스트에서는 opsgate를 `127.0.0.1:8787`에 바인딩하고 로컬 리버스 프록시에서 TLS를 종료합니다.
`/mcp`, `/.well-known/*`, `/oauth/*`, `/passkey/*`, `/passkey.js`, `/approve/*`, `/admin/clients`를 프록시합니다.
경로, 쿼리, Host, Authorization, Origin, MCP 헤더를 변경하지 않습니다.
응답 헤더, 쿠키, 리다이렉트를 보존합니다.
GET, POST, DELETE 및 스트리밍 응답을 지원하도록 설정합니다.
버퍼링과 캐싱을 비활성화하고 장시간 유지되는 MCP 연결을 허용합니다.
인증 헤더, 쿠키, OAuth 쿼리 문자열, 요청 본문, 승인 URL을 로그에 남기지 않습니다.
신뢰할 수 없는 네트워크에서 백엔드 포트에 접근하지 못하도록 합니다.

opsgate는 MCP SDK의 localhost 검사를 자체 Host 검증으로 대체합니다.
공개 요청은 `http.public_url`의 호스트를 사용해야 하며, 명시된 포트가 있으면 포트도 포함해야 합니다.
비교는 대소문자를 구분하지 않습니다. 일치하지 않으면 HTTP 421을 반환합니다.
따라서 공개 Host를 보존하는 프록시 뒤에서 `127.0.0.1`에 바인딩해도 동작합니다.
루프백 리스너는 로컬 프로브를 위해 루프백 Host 값도 허용합니다.
와일드카드 리스너에서는 연결이 루프백으로 들어와도 루프백 Host 값에 이 예외를 적용하지 않습니다.
Forwarded-Host 헤더는 Host를 대체하지 않습니다.

`allowed_redirect_hosts`로 builtin 등록을 제한합니다.
목록이 비어 있으면 그 밖의 유효성 조건을 충족하는 모든 리다이렉트 호스트를 허용합니다.
목록이 비어 있지 않을 때 `unknown_redirect_hosts: deny`는 다른 호스트를 거부합니다.
`approve`는 해당 호스트를 검토 대기 상태로 두며, `admins`에 속한 subject가 `/admin/clients`에서 검토합니다.
이 subject는 `users`에도 있어야 합니다.
`notify_url`은 대기 중인 클라이언트에 대해 최선형(best-effort) JSON 알림을 보낼 수 있습니다.
클라이언트의 목적지를 검토하고, 클라이언트가 스스로 제시한 이름을 검증된 정보로 취급하지 않습니다.

예를 들어 다음 Caddy 설정은 TLS 인증서를 발급받고 응답을 스트리밍합니다.

```caddyfile
mcp.example.com {
    @opsgate path /mcp /.well-known/* /oauth/* /passkey/* /passkey.js /approve/* /admin/clients
    reverse_proxy @opsgate 127.0.0.1:8787 {
        flush_interval -1
    }
    respond 404
}
```

이 예제는 접근 로그를 활성화하지 않습니다.
`/healthz`는 내부 프로브용이며 공개 프록시에 노출할 필요가 없습니다.
클러스터의 네트워크 정책을 사용해 Kubernetes Service 접근을 ingress 컨트롤러와 모니터링으로 제한합니다.
DNS, Google OIDC, Kubernetes API에 필요한 아웃바운드 통신을 허용합니다.

## systemd로 설치

Linux 아카이브를 검증하고 추출한 뒤 관리자 권한으로 실행합니다.

```sh
sudo useradd --system --user-group --home-dir /var/lib/opsgate --shell /usr/sbin/nologin opsgate
sudo install -o root -g root -m 0755 opsgate /usr/local/bin/opsgate
sudo install -d -o root -g opsgate -m 0750 /etc/opsgate
sudo install -o root -g opsgate -m 0640 deploy/systemd/opsgate.http.example.yaml /etc/opsgate/opsgate.yaml
sudo install -o root -g root -m 0644 deploy/systemd/opsgate.service /etc/systemd/system/opsgate.service
```

배포 템플릿은 바이너리 아카이브가 아닌 소스 checkout에 있습니다.
시크릿 프로비저닝 시스템을 사용해 Google 클라이언트 시크릿을 `/etc/opsgate/google-client-secret`에 배치합니다.
소유자는 `root:opsgate`, 모드는 `0640`으로 설정합니다.
시작하기 전에 설정의 오리진, 클라이언트 ID, 사용자, 허용 목록을 수정합니다.
유닛은 OAuth 상태와 감사 기록을 위해 비공개 디렉터리 `/var/lib/opsgate`와 `/var/log/opsgate`를 생성합니다.
감사 로그의 보존 정책과 접근이 제한된 수집 방식을 설정합니다.
로그 로테이션 중 이벤트가 누락되지 않도록 합니다.

호스트 예제는 Kubernetes도 활성화합니다.
컨텍스트가 `opsgate`이고 자격 증명의 권한이 적절히 제한된 `/etc/opsgate/kubeconfig`를 준비합니다.
모드는 `0640`, 소유자는 `root:opsgate`로 설정합니다.
서비스는 홈 디렉터리를 숨기므로 `~/.kube`에 의존하지 않습니다.
Kubernetes를 사용하지 않으면 `providers.kubernetes.enabled: false`로 설정합니다.
네임스페이스 허용 목록은 RBAC을 보완합니다.
제공된 `rbac-readonly.yaml`은 클러스터 전체에 읽기 권한을 부여합니다. 가능한 범위에서 바인딩을 좁힙니다.
이 권한은 선택 사항인 쓰기 작업을 허용하지 않습니다.
쓰기가 필요하면 필요한 네임스페이스 범위의 권한만 추가합니다.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now opsgate
sudo systemctl status opsgate
curl --fail http://127.0.0.1:8787/healthz
```

공개 오리진을 통한 검색(discovery)이 동작하는지 확인합니다.
인증되지 않은 `/mcp` 요청이 거부되는지, 목록에 없는 Google 신원으로 로그인할 수 없는지 확인합니다.
허용된 일회용 대상을 상대로 읽기 작업 하나를 실행합니다.
쓰기가 활성화되어 있으면 승인된 쓰기 작업도 하나 실행합니다.

### 호스트 서비스 재시작 옵션

기본 유닛은 `NoNewPrivileges=yes`와 빈 capability bounding set을 사용합니다.
선택 설정인 `restart_units: [myapp.service]`와 `command_prefix: [/usr/bin/sudo, -n]`이 동작하려면
명시적인 권한 변경이 필요합니다.
재시작이 필요하지 않으면 `restart_units: []`를 유지합니다.

`sudo visudo -f /etc/sudoers.d/opsgate`를 사용해 다음 규칙을 정확히 설치합니다(모드 0440).

```sudoers
opsgate ALL=(root) NOPASSWD: /usr/bin/systemctl restart -- myapp.service
```

실행되는 argv는 정확히 `sudo -n /usr/bin/systemctl restart -- <unit>`입니다.
설치된 `systemctl`의 절대 경로와 정확한 유닛 이름을 맞춥니다.
임의의 systemctl 명령, 와일드카드 유닛, 셸, 포괄적인 비밀번호 없는 sudo 권한을 부여하지 않습니다.

`sudo systemctl edit opsgate`를 사용해 다음 내용을 추가합니다.

```ini
[Service]
NoNewPrivileges=no
CapabilityBoundingSet=
CapabilityBoundingSet=CAP_SETUID CAP_SETGID CAP_AUDIT_WRITE
```

제한된 capability는 sudo가 자격 증명을 변경하고 감사 기능과 연동할 수 있게 합니다.
sudo/PAM 요구사항은 배포판마다 다릅니다.
설치된 정책으로 테스트하고 필요한 capability만 유지합니다.
보안상 절충점을 검토합니다. 이제 서비스는 정확히 지정된 권한 있는 재시작을 실행할 수 있습니다.
설정, 실행 파일, 유닛, sudoers 파일의 소유자는 관리자로 유지합니다.
systemd를 다시 로드하고 서비스를 재시작합니다.

## Kubernetes에 설치

`deploy/kubernetes/opsgate-deployment.yaml`과 기존 `deploy/kubernetes/rbac-readonly.yaml`을 사용합니다.
배포는 후자의 ServiceAccount `opsgate`를 사용합니다.
host provider를 비활성화하고 읽기만 활성화하며, OAuth 상태를 저장할 PVC를 마운트합니다.
복제본 수는 하나로, 전략은 `Recreate`로 유지합니다.
여러 프로세스가 상태 파일을 동시에 공유해서는 안 됩니다.
기본 StorageClass가 필요합니다. 기본값이 없으면 PVC에 명시적으로 지정합니다.
PVC와 백업을 자격 증명으로 취급해 보호합니다.

매니페스트의 Google 클라이언트 ID, HTTPS 오리진, 신원 및 네임스페이스 허용 목록을 수정합니다.
예시 이미지 태그를 검증된 이미지 다이제스트로 교체합니다.
모든 HTTP 프로브에 제공된 Host 헤더를 공개 호스트와 일치하도록 수정합니다.
네임스페이스를 생성하고 보호된 로컬 파일에서 Secret을 준비합니다.

```sh
kubectl create namespace opsgate --dry-run=client -o yaml | kubectl apply -f -
kubectl -n opsgate create secret generic opsgate-upstream \
  --from-file=client-secret=/secure/path/google-client-secret
kubectl apply -f deploy/kubernetes/rbac-readonly.yaml
kubectl apply -f deploy/kubernetes/opsgate-deployment.yaml
kubectl -n opsgate rollout status deployment/opsgate
```

Secret은 필수이며 읽기 전용으로 마운트됩니다. 매니페스트에 클라이언트 시크릿을 직접 넣지 않습니다.
Secret RBAC을 제한하고 저장 시 암호화를 활성화합니다.
로그는 클러스터 로그 수집기가 수집하도록 stderr로 출력됩니다.
TLS ingress/리버스 프록시가 Service `opsgate:8787`로 연결되도록 설정합니다.
앞서 설명한 경로와 스트리밍 동작을 보존합니다.

프로브는 내부 `/healthz` 엔드포인트를 사용합니다.
매니페스트가 `0.0.0.0`에 바인딩하므로 제공된 각 HTTP 프로브의 `httpGet` 아래에
`httpHeaders: [{name: Host, value: mcp.example.com}]`가 포함되어 있습니다.
Host 값은 `http.public_url`의 호스트와 일치해야 하며, 명시된 포트가 있으면 포함해야 합니다.
이 헤더가 없으면 kubelet은 Pod IP를 Host로 사용해 421을 받습니다.
Service/Pod-IP에 직접 보내는 상태 확인 요청에도 같은 요구사항이 적용됩니다.

일회용 개발 환경에서만 `state` 볼륨의 `persistentVolumeClaim`을 `emptyDir: {}`로 바꾸고 PVC 문서를 생략합니다.
이 경우 pod가 교체되면 등록 정보와 토큰이 사라져 클라이언트가 다시 연결해야 합니다.
읽기 전용 루트 파일시스템에는 다른 쓰기 가능한 마운트가 필요하지 않습니다.

쓰기 작업은 비활성화된 상태로 유지됩니다.
활성화하려면 정확한 작업 허용 목록과 선택한 사용자의 `ops:write`를 추가합니다.
또한 대상 네임스페이스에서 해당 작업의 리소스와 동사에 한정된 Role/RoleBinding을 추가합니다.
클러스터 전체에 쓰기 권한을 부여하지 않습니다.
ConfigMap을 변경한 뒤에는 재시작합니다(subPath 마운트는 변경 사항을 자동으로 다시 로드하지 않습니다).

```sh
kubectl -n opsgate rollout restart deployment/opsgate
```

## 업그레이드와 로컬 빌드

먼저 릴리스 노트를 읽고 새 자산을 검증합니다.
호스트 서비스를 중지하고 OAuth 상태, 설정, 감사 로그를 안전하게 백업합니다.
바이너리를 교체한 뒤 시작하고 상태 확인 및 인증 검사를 다시 수행합니다.
롤백을 위해 이전에 검증한 바이너리와 호환되는 상태 백업을 보관합니다.
이전 프로세스와 새 프로세스를 같은 상태 파일에 대해 동시에 실행하지 않습니다.

Kubernetes에서는 배포를 중지한 상태에서 일관된 상태 백업을 생성합니다.
검증된 이미지 다이제스트로 갱신하고 적용합니다.
`Recreate`는 의도적으로 짧은 중단을 발생시킵니다.
트래픽을 재개하기 전에 프로브, 검색, 로그인, 정책 검사를 확인합니다.
롤백할 때는 이전 버전과 호환되는 상태를 복원해야 할 수 있습니다.

`make docker VERSION=dev`는 Go 1.26을 사용해 로컬에서 빌드하고 root가 아닌 사용자로 실행되는 distroless 이미지를 만듭니다.
`make snapshot`은 GoReleaser v2(릴리스 워크플로는 v2.19.0으로 고정), Syft, Buildx를 갖춘 Docker를 사용합니다.
릴리스 서명에는 cosign이 필요하고, 게시에는 레지스트리/GitHub 자격 증명이 필요합니다.
스냅샷은 게시와 서명을 건너뜁니다.
`v*` 태그 릴리스는 race 테스트와 취약점 검사를 수행한 뒤 아카이브, SBOM, 다중 아키텍처 이미지를 빌드합니다.
이어서 체크섬과 이미지에 서명하고 GitHub 빌드 출처 증명을 첨부합니다.
