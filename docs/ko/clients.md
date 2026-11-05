# 클라이언트 연결

[English](../clients.md)

opsgate는 stdio와 `<public_url>/mcp`에서 인증이 필요한 Streamable HTTP를 지원합니다. 첫 공개 릴리스에 필요한 기능은 모두 구현했지만, 외부 보안 검토는 아직 받지 않았습니다. TLS, 인증, upstream 콜백 설정은 [배포](deploy.md)를 참고하세요.

stdio에서는 클라이언트가 바이너리를 실행하고 stdin/stdout으로 MCP 메시지를 주고받습니다. 실행 파일과 설정 파일에는 절대 경로를 사용하고, stdout에는 배너를 출력하지 마세요. 진단 메시지는 stderr로 출력하며, 감사 로그도 기본적으로 stderr에 남깁니다.

프로세스는 실행한 OS 사용자의 권한과 설정된 `stdio.scopes`로 동작하며, `stdio.user`는 감사 로그용 레이블입니다. 설정 파일을 보호하고, 관리자 권한이 없는 일반 계정으로 실행하세요. 필요한 provider와 허용 목록의 네임스페이스만 활성화하세요. Kubernetes kubeconfig의 `exec` 및 `auth-provider` 자격 증명 플러그인은 허용하지 않으므로, 수명이 짧은 ServiceAccount 토큰 같은 자격 증명을 사용하세요. 네임스페이스 정책은 Nodes나 PersistentVolumes를 제한하지 않습니다. [RBAC 매니페스트](../../deploy/kubernetes/rbac-readonly.yaml)와 [보안 정책](security.md)을 검토하세요.

## Claude Code

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

## Claude Desktop

다른 서버 설정은 유지하고, 아래 내용을 `claude_desktop_config.json`에 추가하세요.

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "/path/to/opsgate",
      "args": ["-config", "/path/opsgate.yaml"]
    }
  }
}
```

Claude Desktop의 개발자 설정에서 설정 파일 위치를 확인하고, 수정한 뒤 앱을 다시 시작하세요. macOS에서는 Darwin 빌드와 로컬 kubeconfig를 사용할 수 있습니다. Windows 빌드는 없으므로, kubeconfig로 클러스터에 접근할 수 있는 Linux 머신에 SSH로 연결하세요.

## Cursor

아래 내용을 프로젝트의 `.cursor/mcp.json`이나 Cursor의 사용자 설정 파일인 `mcp.json`에 추가하세요.

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "/path/to/opsgate",
      "args": ["-config", "/path/opsgate.yaml"]
    }
  }
}
```

프로젝트의 MCP 설정은 사용자 권한으로 프로그램을 실행할 수 있으므로, 직접 검토한 설정만 신뢰하세요.

## SSH를 통한 연결

kubeconfig로 클러스터에 접근할 수 있는 Linux 머신에 opsgate와 모드 없는 stdio 설정 파일을 설치하세요. stdio로 연결하는 명령은 다음과 같습니다.

```sh
ssh host /usr/local/bin/opsgate -config /etc/opsgate.yaml
```

Claude Code에서는 다음 명령을 사용하세요.

```sh
claude mcp add opsgate -- ssh -T host /usr/local/bin/opsgate -config /etc/opsgate.yaml
```

Desktop과 Cursor에서는 다음 서버 항목을 사용할 수 있습니다.

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "ssh",
      "args": ["-T", "host", "/usr/local/bin/opsgate", "-config", "/etc/opsgate.yaml"]
    }
  }
}
```

먼저 호스트 키 검증과 비대화형 SSH 인증을 설정하세요. 호스트 키 검사는 끄지 마세요. 원격 호스트에는 관리자 권한이 없는 전용 계정을 사용하고, 가상 터미널은 할당하지 마세요. 원격 시작 파일에서도 stdout에 출력하지 않도록 하세요. 원격 명령에는 신뢰할 수 있는 고정 경로를 사용하고, 도구 입력을 SSH 명령에 삽입하지 마세요. SSH가 전송 보안을 담당하며, 이 구성에서도 서버는 HTTP 엔드포인트가 아닌 stdio MCP 서버로 동작합니다.

## HTTP를 통한 claude.ai 및 ChatGPT 연결

opsgate는 ChatGPT가 사용하는 세션 없는 MCP 프로토콜 `2026-07-28`을 서버에 상태를 유지하지 않는 방식으로 제공합니다.

클라이언트의 커넥터/통합 설정에서 사용자 지정 커넥터를 추가하고 OAuth를 선택하세요. `https://mcp.example.com/mcp`처럼 전체 MCP URL을 입력하세요. 클라이언트는 Protected Resource Metadata와 인가 서버 검색을 사용합니다. builtin 모드는 OAuth 클라이언트 등록(DCR)을 지원하므로 MCP 클라이언트 ID나 비밀값을 직접 발급할 필요가 없습니다.

리다이렉트 호스트 정책을 설정했다면 클라이언트를 거부하거나 `/admin/clients`에서 검토 대기 상태로 둘 수 있으며, `notify_url`로 관리자에게 알릴 수 있습니다. 호스트 목록이 비어 있으면 다른 유효성 조건을 만족하는 모든 리다이렉트 호스트를 허용합니다. upstream Google/GitHub 애플리케이션 자격 증명은 서버에만 저장하세요. 허용 목록에 있는 사용자로 upstream에 로그인하고, 접근을 허용하기 전에 동의 화면의 클라이언트, 리다이렉트 목적지, 스코프를 확인하세요. 커넥터 제공 여부와 설정 이름은 클라이언트와 계정에 따라 다릅니다.

upstream 인증 provider에는 MCP URL이 아닌 `<public_url>/oauth/callback`을 등록하세요. 프록시는 `/mcp`, `/.well-known/*`, `/oauth/*`, `/passkey/*`, `/passkey.js`, `/approve/*`, `/admin/clients`를 전달하고 스트리밍을 지원해야 합니다. 공개 Host는 명시한 포트까지 그대로 전달하세요. 다른 호스트로 온 요청에는 421로 응답하지만, 루프백 리스너로 들어오는 로컬 루프백 요청은 예외입니다. 이 검증은 SDK의 localhost 검사를 대체합니다. external 모드의 클라이언트 등록은 외부 인가 서버의 지원 여부에 따라 달라지며, static 모드는 OAuth/DCR을 제공하지 않습니다.

## HTTP를 통한 Claude Code 연결

```sh
claude mcp add --transport http opsgate https://host/mcp
```

클라이언트의 인증 절차에 따라 로그인하세요. `host`를 공개 HTTPS origin으로 바꾸세요. 로컬이나 SSH로 접속하려면 위의 stdio 설정을 유지하세요.

## 로컬 HTTP 클라이언트용 정적 토큰

다른 사람이 볼 수 없는 터미널에서 `opsgate -gen-token`을 실행하세요. 원본 토큰과 SHA-256 다이제스트를 출력합니다. 서버 설정에는 다이제스트만 넣으세요.

```yaml
http:
  listen: 127.0.0.1:8787
  public_url: http://127.0.0.1:8787
auth:
  mode: static
  static:
    - name: local-client
      sha256: REPLACE_WITH_GENERATED_HEX_DIGEST
      scopes: [ops:read]
```

`opsgate -transport http -config /path/opsgate.yaml`로 시작하세요. 로컬 HTTP 클라이언트의 비밀값/헤더 설정 기능에서 `http://127.0.0.1:8787/mcp`에 `Authorization: Bearer <raw-token>`을 보내도록 설정하세요. 토큰을 URL, 커밋하는 설정 파일, 공유 로그에 넣지 마세요. 정적 토큰에는 OAuth 갱신 절차가 없으므로, 폐기하거나 교체하려면 다이제스트를 삭제하거나 교체한 뒤 다시 시작하세요. 리스너는 루프백에 유지하고, 원격 클라이언트에는 HTTPS를 사용하세요.

## 쓰기 승인

opsgate는 provider와 작업 허용 목록을 설정한 경우에만 Kubernetes 쓰기 도구를 등록합니다. 노트 쓰기에는 볼트 provider와 `allow_write`가 필요합니다. 쓰기에는 `ops:write`, OS/RBAC 권한, 사람의 승인도 필요하며, HTTP 사용자는 `ops:read`도 있어야 합니다. MCP 클라이언트 내 확인(elicitation)을 지원하는 클라이언트는 확인 창에 계획(Plan)을 표시합니다. 여러 프로토콜 버전에서 입력 요청을 여러 차례 주고받는 방식을 사용합니다.

클라이언트 내 확인을 지원하지 않더라도 builtin 인증에서 `approval.web: true`를 설정하면 웹 승인 링크와 `approval_id`를 반환합니다. 링크를 열고 같은 upstream 사용자로 로그인한 뒤, 요약에 표시된 작업 내용을 꼼꼼히 읽으세요. 승인 후 클라이언트는 같은 도구와 인자에 `approval_id`를 추가해 다시 호출합니다. 승인은 유효 기간이 있는 일회용이며, 승인한 작업에만 유효합니다. 클라이언트 내 확인을 지원하지 않는 static/external 클라이언트는 쓰기 작업을 할 수 없으며, stdio도 클라이언트 내 확인이 필요합니다. 공격자가 장악한 UI는 클라이언트 내 확인을 자동 승인할 수 있고, 프롬프트 인젝션으로 쓰기 요청이 들어올 수도 있으므로 사람이 직접 요약을 읽어야 합니다.

dev 도구에는 대신 `dev:run`이 필요합니다. 작업을 시작할 때는 승인이 필요하지만 자신이 소유한 작업을 취소할 때는 필요하지 않으며, runner 관리자는 모든 작업을 취소할 수 있습니다. HTTP에는 여전히 `ops:read`가 필요합니다. 승인 시간 창은 기본적으로 꺼져 있습니다. 활성화한 뒤 사람이 선택하면, 그 승인은 특정 사용자, 도구, 대상에 적용되며 클라이언트가 달라도 유효합니다. `dev_task_start`에는 승인 시간 창을 활성화하지 마세요. [dev 설정](dev.md)과 [신뢰 모델](security.md)을 참고하세요.
