# opsgate 배포

[English](../deploy.md)

Kubernetes 점검에는 컨테이너를 사용하세요. Darwin 빌드는 kubeconfig로 클러스터에 접근하는 로컬 클라이언트용입니다. Windows 빌드는 없습니다.

Streamable HTTP, OAuth, 인증, 사람의 승인, 쓰기 도구는 모두 구현했습니다.
현재 소스는 첫 공개 릴리스를 준비하는 단계이며, 외부 보안 검토는 아직 받지 않았습니다.
note 및 dev provider는 선택 사항이고 아직 실험 단계입니다.
외부에 공개하기 전에 인증, 승인, 프록시 동작을 검증하세요.
업스트림 로그인 콜백은 정확히 `<public_url>/oauth/callback`입니다.

## 설치 전 릴리스 검증

공식 배포 경로에서 cosign과 GitHub CLI를 설치하세요.
실제로 존재하는 릴리스 태그를 지정하고, 운영체제와 아키텍처에 맞는 아카이브를 선택하세요.

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

각 명령은 앞선 명령이 성공한 뒤에만 실행하세요.
더 엄격하게 신원을 검증하려면 정규식 플래그를 `--certificate-identity`로 바꾸고,
`@refs/tags/$TAG`로 끝나는 전체 워크플로 URL을 지정하세요.
macOS에서는 `shasum -a 256`으로 선택한 아카이브의 다이제스트를 구한 뒤, 검증된 체크섬 파일에 기록된 값과 비교하세요.
서명, 체크섬, 증명(attestation)을 모두 검증한 뒤에만 아카이브를 풀고 실행하세요.
릴리스 자산에는 Syft가 생성한 아카이브 SBOM도 포함됩니다.

컨테이너를 배포할 때는 릴리스나 레지스트리에서 매니페스트 다이제스트를 가져오세요.
다이제스트는 변하지 않는 이미지를 가리키므로, 이 참조를 검증한 뒤 배포 설정에 사용하세요.

```sh
IMAGE=ghcr.io/gkdis6/opsgate@sha256:REPLACE_WITH_MANIFEST_DIGEST
cosign verify \
  --certificate-identity-regexp '^https://github\.com/gkdis6/opsgate/\.github/workflows/release\.yml@refs/tags/v[^/]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com "$IMAGE"
```

GoReleaser는 Sigstore 번들을 사용하는 키리스 `sign-blob`으로 체크섬에 서명하고,
`docker_signs`로 컨테이너 다이제스트에 서명합니다.
[서명 문서](https://goreleaser.com/customization/sign/sign/)를 참고하세요.

## TLS 리버스 프록시와 Google 로그인

`http.public_url`을 `https://mcp.example.com`과 같은 외부 **HTTPS 오리진**으로 설정하세요.
`/mcp`나 다른 경로를 덧붙이지 마세요.
클라이언트는 `https://mcp.example.com/mcp`에 연결합니다. 이 URL은 OAuth 리소스 식별자이기도 합니다.
Google은 사용자를 인증하고, opsgate는 MCP 액세스 토큰을 발급합니다.

Google OIDC 웹 애플리케이션을 만들고 issuer로 `https://accounts.google.com`을 사용하세요.
identity provider에는 업스트림 콜백 URL을 정확히 **`<public_url>/oauth/callback`**로 등록하세요.
예를 들어 `https://mcp.example.com/oauth/callback`입니다.
클라이언트 ID를 설정하고 허용 목록을 명시하세요.
허용 목록에는 검증된 이메일 subject(`email:operator@example.com`)나 정확한 `oidc:<sub>` subject를 지정하세요.
Google 계정의 이메일을 신원으로 사용하려면 Gmail/Googlemail 계정이거나, 이메일 도메인과 일치하는 Workspace `hd` 클레임이 있어야 합니다.
그 밖의 Google 계정은 `oidc:<sub>`를 사용해야 합니다.
클라이언트 비밀값은 파일이나 Kubernetes 시크릿에 저장하세요.
YAML이나 명령 인자에 직접 넣거나 소스 관리에 포함하지 마세요.
`ops:write`는 필요한 운영자에게만 주세요.
각 쓰기 작업에는 허용된 대상, OS/RBAC 권한, 사람의 승인도 필요합니다.

Service `opsgate:8787` 앞의 ingress나 리버스 프록시에서 TLS를 종료하세요.
`/mcp`, `/.well-known/*`, `/oauth/*`, `/passkey/*`, `/passkey.js`, `/approve/*`, `/admin/clients`를 프록시하도록 설정하세요.
경로, 쿼리, Host, Authorization, Origin, MCP 헤더는 변경하지 말고,
응답 헤더, 쿠키, 리다이렉트도 그대로 전달하세요.
GET, POST, DELETE와 스트리밍 응답을 지원하도록 설정하고,
버퍼링과 캐싱을 끈 상태에서 장시간 유지되는 MCP 연결을 허용하세요.
인증 헤더, 쿠키, OAuth 쿼리 문자열, 요청 본문, 승인 URL은 로그에 남기지 마세요.
신뢰할 수 없는 네트워크에서는 백엔드 포트에 접근할 수 없도록 막으세요.

요청 폭주 시 진행 중인 로그인은 만료 전까지 유지되지만 공유 로그인 제한기가 회복될 때까지 새 웹 승인 및 관리자 로그인이 지연될 수 있으므로(`Retry-After`가 포함된 HTTP 429), 리버스 프록시에서도 요청 빈도를 제한하세요.

opsgate는 MCP SDK의 localhost 검사 대신 자체 Host 검증을 사용합니다.
공개 요청은 `http.public_url`의 호스트를 사용해야 하며, 명시된 포트가 있으면 포트도 포함해야 합니다.
대소문자를 구분하지 않고 비교하며, 일치하지 않으면 HTTP 421을 반환합니다.
따라서 공개 Host를 보존하는 프록시 뒤에서 `127.0.0.1`에 바인딩해도 동작합니다.
루프백 리스너는 로컬 프로브를 위해 루프백 Host 값도 허용합니다.
와일드카드 리스너에서는 연결이 루프백으로 들어와도 루프백 Host 값에 이 예외를 적용하지 않습니다.
Forwarded-Host 헤더는 Host를 대체하지 않습니다.

`allowed_redirect_hosts`로 builtin 등록을 제한하세요.
목록이 비어 있으면 그 밖의 유효성 조건을 충족하는 모든 리다이렉트 호스트를 허용합니다.
목록이 비어 있지 않을 때 `unknown_redirect_hosts: deny`는 다른 호스트를 거부합니다.
`approve`는 그 호스트를 검토 대기 상태로 두며, `admins`에 속한 subject가 `/admin/clients`에서 검토합니다.
이 subject는 `users`에도 있어야 합니다.
`notify_url`로 대기 중인 클라이언트의 JSON 알림을 보낼 수 있지만, 알림 전달을 보장하지는 않습니다.
클라이언트의 목적지를 검토하고, 클라이언트가 스스로 제시한 이름을 검증된 정보로 취급하지 마세요.

예를 들어 `opsgate` 네임스페이스에서 실행하는 Caddy는 다음 설정으로 TLS 인증서를 발급받고 응답을 스트리밍합니다.

```caddyfile
mcp.example.com {
    @opsgate path /mcp /.well-known/* /oauth/* /passkey/* /passkey.js /approve/* /admin/clients
    reverse_proxy @opsgate opsgate:8787 {
        flush_interval -1
    }
    respond 404
}
```

이 예제는 접근 로그를 활성화하지 않습니다.
`/healthz`는 내부 프로브용이며 공개 프록시에 노출할 필요가 없습니다.
클러스터 네트워크 정책으로 ingress 컨트롤러와 모니터링에서만 Kubernetes Service에 접근할 수 있도록 제한하세요.
DNS, Google OIDC, Kubernetes API에 필요한 아웃바운드 통신은 허용하세요.

## Kubernetes에 설치

배포 템플릿은 바이너리 아카이브에는 없고, 체크아웃한 소스에 들어 있습니다.

네임스페이스 허용 목록은 RBAC을 보완합니다.
제공된 `rbac-readonly.yaml`의 ClusterRoleBinding은 ServiceAccount가 Kubernetes API 수준에서 허용된 리소스 종류를 모든 네임스페이스에서 읽을 수 있게 합니다. opsgate 정책이 거부한 네임스페이스도 포함됩니다.
watcher도 이 역할을 공유합니다. 이 바인딩에서는 애플리케이션 정책이 네임스페이스 접근 경계입니다.
더 엄격하게 제한하려면 ClusterRoleBinding을 네임스페이스별 RoleBinding으로 교체하세요.
쓰기는 선택 사항이며, 이 권한만으로는 허용하지 않습니다.
쓰기가 필요하면 필요한 네임스페이스 스코프 권한만 추가하세요.

`deploy/kubernetes/opsgate-deployment.yaml`과 기존 `deploy/kubernetes/rbac-readonly.yaml`을 사용하세요.
이 배포는 RBAC 매니페스트에 정의된 ServiceAccount `opsgate`를 사용합니다.
읽기만 활성화하며, OAuth 상태를 저장할 PVC를 마운트합니다.
복제본 수는 하나로, 전략은 `Recreate`로 유지하세요.
여러 프로세스가 상태 파일을 동시에 공유해서는 안 됩니다.
기본 StorageClass가 필요하며, 기본값이 없으면 PVC에 직접 지정하세요.
PVC와 백업은 자격 증명으로 취급해 보호하세요.

매니페스트의 Google 클라이언트 ID, HTTPS 오리진, 신원 및 네임스페이스 허용 목록을 수정하세요.
예시 이미지 태그를 검증된 이미지 다이제스트로 바꾸고,
모든 HTTP 프로브의 Host 헤더를 공개 호스트에 맞추세요.
네임스페이스를 만들고, 보호된 로컬 파일로 시크릿을 생성하세요.

```sh
kubectl create namespace opsgate --dry-run=client -o yaml | kubectl apply -f -
kubectl -n opsgate create secret generic opsgate-upstream \
  --from-file=client-secret=/secure/path/google-client-secret
kubectl apply -f deploy/kubernetes/rbac-readonly.yaml
kubectl apply -f deploy/kubernetes/opsgate-deployment.yaml
kubectl -n opsgate rollout status deployment/opsgate
```

시크릿은 필수이며 읽기 전용으로 마운트합니다. 매니페스트에는 클라이언트 비밀값이 들어 있지 않습니다.
시크릿의 RBAC 권한을 제한하고 저장 시 암호화를 활성화하세요.
로그는 클러스터 로그 수집기가 수집할 수 있도록 stderr로 출력합니다. 클러스터 로그 수집기에서 감사 로그 보존과 수집 접근 제한을 설정하고 이벤트 누락을 방지하세요.
TLS ingress/리버스 프록시가 Service `opsgate:8787`로 연결되도록 설정하고,
앞서 설명한 경로와 스트리밍 동작을 유지하세요.

프로브는 내부 `/healthz` 엔드포인트를 사용합니다.
매니페스트가 `0.0.0.0`에 바인딩하므로 제공된 각 HTTP 프로브의 `httpGet` 아래에
`httpHeaders: [{name: Host, value: mcp.example.com}]`가 포함되어 있습니다.
Host 값은 `http.public_url`의 호스트와 일치해야 하며, 명시된 포트가 있으면 포함해야 합니다.
이 헤더가 없으면 kubelet은 파드 IP를 Host로 사용하므로 421을 받습니다.
Service/파드 IP에 직접 보내는 상태 확인 요청에도 같은 요구사항이 적용됩니다.

일회용 개발 환경에서만 `state` 볼륨의 `persistentVolumeClaim`을 `emptyDir: {}`로 바꾸고 PVC 문서를 생략하세요.
이 경우 파드가 교체되면 등록 정보와 토큰이 사라져 클라이언트가 다시 연결해야 합니다.
루트 파일시스템은 읽기 전용이며, 그 밖에 쓰기 가능한 마운트는 필요하지 않습니다.

쓰기 작업은 비활성화 상태입니다.
활성화하려면 허용할 작업을 정확히 명시한 허용 목록을 추가하고, 선택한 사용자에게 `ops:write`를 주세요.
대상 네임스페이스에는 그 작업에 필요한 리소스와 동사만 허용하는 Role/RoleBinding을 추가하세요.
클러스터 전체에 쓰기 권한을 주지 마세요.
제공된 `rbac-operator.yaml`의 Role은 네임스페이스 범위이지만, 워크로드 `patch` 권한은 재시작 도구가 바꾸는 필드보다 더 많은 필드를 변경할 수 있게 합니다.
RBAC은 이 권한을 재시작 annotation으로만 제한하지 않습니다.
subPath 마운트는 변경 사항을 자동으로 다시 로드하지 않으므로, ConfigMap을 변경한 뒤에는 재시작하세요.

```sh
kubectl -n opsgate rollout restart deployment/opsgate
```

공개 오리진에서 검색(discovery)이 동작하는지 확인하세요.
인증되지 않은 `/mcp` 요청을 거부하는지, 목록에 없는 신원으로 로그인할 수 없는지도 확인하세요.
허용된 일회용 대상에 읽기 작업을 하나 실행하고,
쓰기를 활성화했다면 승인된 쓰기 작업도 하나 실행하세요.

## 업그레이드와 로컬 빌드

릴리스 노트를 읽고 새 아티팩트를 먼저 검증하세요. Kubernetes 배포를 중지한 동안 OAuth 상태, 설정, 감사 로그를 안전하게 백업하고, 검증된 이미지 다이제스트로 바꾼 뒤 적용하세요. `Recreate`는 의도적으로 짧은 중단을 만듭니다. 트래픽을 다시 받기 전에 프로브, 검색, 로그인, 정책 검사를 확인하세요. 롤백을 위해 이전의 검증된 이미지와 호환되는 상태 백업을 보관하세요. 롤백에는 이전 버전과 호환되는 상태 복원이 필요할 수 있습니다. 구버전과 새 버전 프로세스를 같은 상태 파일에 동시에 연결하지 마세요.

`make docker VERSION=dev`는 Go 1.26을 사용해 로컬에서 빌드하고 root가 아닌 사용자로 실행되는 distroless 이미지를 만듭니다.
`make snapshot`은 GoReleaser v2(릴리스 워크플로는 v2.19.0으로 고정), Syft, Buildx가 설치된 Docker를 사용합니다.
릴리스 서명에는 cosign이 필요하고, 게시에는 레지스트리/GitHub 자격 증명이 필요합니다.
스냅샷은 게시와 서명을 건너뜁니다.
`v*` 태그 릴리스는 race 테스트와 취약점 검사를 마친 뒤 아카이브, SBOM, 다중 아키텍처 이미지를 빌드합니다.
이어서 체크섬과 이미지에 서명하고 GitHub 빌드 출처 증명을 첨부합니다.
