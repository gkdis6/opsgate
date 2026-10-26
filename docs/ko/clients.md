# 클라이언트 연결

[English](../clients.md)

opsgate는 stdio와 `<public_url>/mcp`의 인증된 Streamable HTTP를 지원합니다. 문서의 기능은 첫 공개 릴리스용으로 구현되어 있으며, 제3자의 보안 검토는 아직 받지 않았습니다. TLS, 인증, upstream 콜백 설정은 [배포](deploy.md)를 참고합니다.

stdio에서는 클라이언트가 바이너리를 실행하고 stdin/stdout으로 MCP 메시지를 교환합니다. 실행 파일과 설정 파일에는 절대 경로를 사용합니다. stdout에 배너를 출력하지 않습니다. 진단 메시지와 기본 감사 로그는 stderr로 출력됩니다.

프로세스는 실행한 OS 사용자의 권한과 설정된 `stdio.scopes`로 동작합니다. `stdio.user`는 감사 로그용 식별 레이블입니다. 설정 파일을 보호하고 일반 사용자 계정을 사용합니다. 필요한 provider와 허용 목록의 unit/네임스페이스만 활성화합니다. Kubernetes kubeconfig의 `exec` 및 `auth-provider` 자격 증명 플러그인은 거부됩니다. 수명이 짧은 ServiceAccount 토큰 같은 자격 증명을 사용합니다. 네임스페이스 정책은 Nodes나 PersistentVolumes를 제한하지 않습니다. [RBAC 매니페스트](../../deploy/kubernetes/rbac-readonly.yaml)와 [보안 정책](security.md)을 검토합니다.

## Claude Code

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

## Claude Desktop

다른 서버 설정을 유지하면서 `claude_desktop_config.json`에 병합합니다.

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

Claude Desktop의 개발자 설정에서 설정 파일 위치를 확인하고, 편집 후 앱을 다시 시작합니다. macOS와 Windows에서는 로컬 호스트 provider를 활성화하는 대신 SSH로 Linux 호스트를 조회합니다.

## Cursor

프로젝트의 `.cursor/mcp.json`이나 사용자 수준 Cursor `mcp.json`에 병합합니다.

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

검토한 프로젝트 MCP 설정만 신뢰합니다. 이 설정은 사용자 권한으로 프로그램을 실행할 수 있습니다.

## SSH를 통한 연결

Linux 호스트에 opsgate와 설정 파일을 설치합니다. stdio 명령은 다음과 같습니다.

```sh
ssh host /usr/local/bin/opsgate -config /etc/opsgate.yaml
```

Claude Code에서는 다음 명령을 사용합니다.

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

먼저 호스트 키 검증과 비대화형 SSH 인증을 설정합니다. 호스트 키 검사를 끄지 않습니다. 전용 일반 원격 계정을 사용하고 가상 터미널을 할당하지 않습니다. 원격 시작 파일은 stdout에 출력하면 안 됩니다. 원격 명령 경로는 고정된 신뢰할 수 있는 경로로 유지합니다. 도구 입력을 SSH 명령에 삽입하지 않습니다. SSH는 전송 보안을 제공합니다. 이 구성도 HTTP 엔드포인트가 아닌 stdio MCP 서버입니다.

## HTTP를 통한 claude.ai 및 ChatGPT 연결

opsgate는 ChatGPT가 사용하는 세션 없는 MCP 프로토콜 `2026-07-28`을 무상태로 제공합니다.

클라이언트의 커넥터/통합 설정에서 사용자 지정 커넥터를 추가하고 OAuth를 선택합니다. `https://mcp.example.com/mcp`처럼 전체 MCP URL을 입력합니다. 클라이언트는 Protected Resource Metadata와 인가 서버 검색을 사용합니다. builtin 모드는 OAuth 클라이언트 등록(DCR)을 지원하므로 MCP 클라이언트 ID나 시크릿을 수동으로 발급할 필요가 없습니다.

설정된 리다이렉트 호스트 정책에 따라 클라이언트가 거부되거나 `/admin/clients`에서 검토를 기다릴 수 있습니다. `notify_url`로 관리자에게 알릴 수 있습니다. 호스트 목록이 비어 있으면 다른 유효성 조건을 만족하는 모든 리다이렉트 호스트를 허용합니다. upstream Google/GitHub 애플리케이션 자격 증명은 서버에만 둡니다. 허용 목록의 사용자로 upstream에 로그인합니다. 접근을 허용하기 전에 동의 화면의 클라이언트, 리다이렉트 목적지, 스코프를 확인합니다. 커넥터 제공 여부와 설정 이름은 클라이언트/계정에 따라 다릅니다.

upstream identity provider에는 MCP URL이 아닌 `<public_url>/oauth/callback`을 등록합니다. 프록시는 `/mcp`, `/.well-known/*`, `/oauth/*`, `/passkey/*`, `/passkey.js`, `/approve/*`, `/admin/clients`를 전달하고 스트리밍을 지원해야 합니다. 명시적 포트를 포함한 공개 Host를 보존합니다. 다른 호스트는 421 응답을 받습니다. 단, 루프백 리스너의 로컬 루프백 요청은 예외입니다. 이 검증은 SDK의 localhost 검사를 대체합니다. external 모드는 외부 인가 서버의 클라이언트 등록 지원에 의존합니다. static 모드는 OAuth/DCR을 제공하지 않습니다.

## HTTP를 통한 Claude Code 연결

```sh
claude mcp add --transport http opsgate https://host/mcp
```

클라이언트의 인증 절차로 로그인합니다. `host`를 공개 HTTPS origin으로 바꿉니다. 로컬이나 SSH 접근을 원하면 위의 stdio 설정을 유지합니다.

## 로컬 HTTP 클라이언트용 정적 토큰

비공개 터미널에서 `opsgate -gen-token`을 실행합니다. 원본 토큰과 SHA-256 다이제스트가 출력됩니다. 서버 설정에는 다이제스트만 넣습니다.

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

`opsgate -transport http -config /path/opsgate.yaml`로 시작합니다. 로컬 HTTP 클라이언트의 시크릿/헤더 기능으로 `http://127.0.0.1:8787/mcp`에 `Authorization: Bearer <raw-token>`을 보내도록 설정합니다. 토큰을 URL, 커밋한 설정, 공유 로그에 넣지 않습니다. 정적 토큰에는 OAuth 갱신 절차가 없습니다. 폐기하거나 교체하려면 다이제스트를 삭제하거나 교체한 뒤 다시 시작합니다. 리스너는 루프백에 유지하고, 원격 클라이언트에는 HTTPS를 사용합니다.

## 쓰기 승인

호스트/Kubernetes 쓰기는 해당 provider와 작업 허용 목록이 설정된 경우에만 등록됩니다. 노트 쓰기에는 볼트 provider와 `allow_write`가 필요합니다. 쓰기에는 `ops:write`, OS/RBAC 권한, 사람의 승인도 필요합니다. HTTP 사용자는 `ops:read`도 필요합니다. MCP 클라이언트 내 확인(elicitation)을 지원하는 클라이언트는 계획을 클라이언트 내부 프롬프트에 표시합니다. 프로토콜 버전에 걸쳐 여러 왕복의 입력 요청을 사용합니다.

그렇지 않으면 `approval.web: true`가 설정된 builtin 인증이 웹 승인 링크와 `approval_id`를 반환합니다. 링크를 열고 같은 upstream 사용자로 로그인한 뒤 정확한 요약을 읽습니다. 승인 후 클라이언트는 같은 도구와 인자에 `approval_id`를 추가해 다시 호출합니다. 승인은 만료되며, 일회용이고 정확한 작업에 바인딩됩니다. 클라이언트 내 확인이 없는 static/external 클라이언트는 쓸 수 없습니다. stdio도 클라이언트 내 확인이 필요합니다. 침해된 UI는 클라이언트 내 확인을 자동 승인할 수 있고, 프롬프트 인젝션은 쓰기를 요청할 수 있습니다. 사람이 요약을 읽어야 합니다.

dev 도구에는 대신 `dev:run`이 필요합니다. 작업 시작은 승인이 필요하며, 자신이 소유한 작업의 취소에는 승인이 필요하지 않습니다. runner 관리자는 모든 작업을 취소할 수 있습니다. HTTP에는 여전히 `ops:read`가 필요합니다. 승인 시간 창은 기본적으로 꺼져 있습니다. 활성화한 뒤 사람이 선택하면, 승인 권한은 클라이언트와 대상에 관계없이 사용자/도구 쌍에 적용됩니다. `dev_task_start`에는 승인 시간 창을 활성화하지 않습니다. [dev 설정](dev.md)과 [신뢰 모델](security.md)을 참고합니다.
