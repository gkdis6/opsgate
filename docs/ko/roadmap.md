# 로드맵

[English](../roadmap.md)

아래 기능은 v0.2.0까지 구현했지만, 외부 보안 검토는 아직 받지 않았습니다. 향후 작업 일정은 보장하지 않습니다.

## 완료

- 내장 인증에서 패스키 로그인을 지원합니다. upstream provider는 `passkey`이며, `opsgate passkey invite`로 일회용 등록 초대를 발급합니다.
- ChatGPT에 필요한 세션 없는 MCP 프로토콜 `2026-07-28`을 서버에 상태를 유지하지 않는 방식으로 제공합니다.
- stdio로 Kubernetes를 조회할 수 있습니다. 허용 목록, 네임스페이스 거부 목록, 스코프 검사, 출력 제한, 마스킹을 적용하고 JSONL 감사 로그를 저장합니다.
- `/mcp`에서 인증이 필수인 Streamable HTTP를 제공하며, Protected Resource Metadata를 지원합니다.
- 내장 OAuth 2.1에서 PKCE S256을 사용하는 인가 코드, 클라이언트 등록(DCR), 동의, upstream GitHub/OIDC 로그인(Google 포함)을 지원합니다. 불투명 토큰은 해시로 저장하고, 리프레시 토큰 교체와 재사용 탐지를 지원하며, 필요하면 상태를 저장해 재시작 후에도 유지할 수 있습니다.
- issuer 검색/JWKS를 사용하는 외부 JWT 인증과 다이제스트로 설정하는 정적 Bearer 토큰을 지원하며, `-gen-token` 보조 명령을 제공합니다.
- Kubernetes 워크로드 롤아웃 재시작, 복제본 수 변경, 파드 삭제를 지원합니다. 각 작업에는 별도 허용 목록과 `ops:write`, 사람의 승인이 필요합니다.
- 여러 MCP 프로토콜 버전에서 클라이언트 내 확인(elicitation)을 지원하며, 같은 사용자로 upstream에 로그인하는 브라우저 승인을 제공합니다. 승인은 유효 기간이 있는 일회용이며, 승인한 인자에만 유효합니다.
- GoReleaser 릴리스, cosign 키 없는 서명, SBOM, 출처 증명, 컨테이너 이미지, Kubernetes 매니페스트를 제공합니다.
- 도구 등록 정책과 사용자별·토큰별 도구 제한, 리다이렉트 호스트 등록 정책을 지원합니다. 관리자는 등록 대기 중인 클라이언트를 검토할 수 있고, 필요하면 알림을 받을 수 있습니다.
- 선택 기능인 승인 시간 창은 특정 사용자, 도구, 대상에만 유효하고, 상태는 메모리에만 저장합니다. 기본적으로는 꺼져 있습니다.
- 실험적 note provider는 범위가 제한된 Markdown 폴더(Obsidian 호환)에서 읽기·검색·링크·태그 조회를 지원합니다. 노트 작성, 내용 추가, 내용 수정, 휴지통으로 이동은 승인을 받아 실행합니다.
- 실험적 dev provider와 별도 runner는 설정된 프로젝트와 에이전트 명령으로 승인된 작업을 시작합니다. 요청자의 작업 소유권과 관리자의 접근 권한, 로그·diff 조회, 취소, 보존 관리를 지원합니다.
- 배포 모드(`mode: kubernetes`)가 기본값을 채우고, `opsgate init`이 짧은 설정 파일을 만들며, `opsgate config show`가 최종 설정을 보여 줍니다.
- 실험적 `opsgate watch`는 별도 프로세스로 파드, 워크로드, 노드, ConfigMap 키를 watch하고, 알림을 묶고 중복을 줄여 webhook으로 보냅니다. 필요하면 HMAC으로 서명합니다.
- 실험적 `opsgate-analyzer`는 서명된 watch 알림을 받아 제한된 읽기 전용 MCP 조사를 수행합니다. OpenAI 호환 API, Claude Code CLI, Codex CLI 중 하나를 선택하며, Discord 보고서와 선택 사항인 서명 webhook 전달, 한국어/영어 보고서와 실행하지 않는 작업 제안을 지원합니다.
- `k8s_restore_image`는 허용 목록과 사람의 승인을 거쳐 Deployment 컨테이너 이미지를 현재 준비된 소유 ReplicaSet의 이미지로 복원합니다. 읽기 전용 `k8s_remediation_options`는 검증한 복원 후보의 승인 링크를 제공합니다.
- watch는 독립된 Discord 및 사용자 지정 JSON webhook 출력, 본문 템플릿, 헤더, 자격 증명 파일, HTTP 429 재시도를 지원합니다. SMTP 이메일 출력은 검증된 TLS, 별도 배치와 재시도를 사용합니다.
- watch는 한국어/영어 표시와 원본 오류 코드·메시지 보존, 출력별 `problems_only` 경고 필터링을 지원합니다.
- HTTP에서는 사용 가능한 웹 승인을 기본적으로 요구합니다. `approval.require_web: false`로 클라이언트 내 확인을 허용할 수 있고, stdio는 `require_web: true`를 명시하지 않으면 클라이언트 내 확인을 유지합니다.

## 남은 아이디어

아래 항목은 아이디어이며 구현을 약속한 것은 아닙니다. 범위는 보안 검토에 따라 달라질 수 있습니다.

- 최소 권한과 입력 제한을 적용한 Docker 및 PostgreSQL provider.
- 설정 다시 불러오기.
- Client ID Metadata Documents(CIMD).
- 도구별 요청 빈도 제한.
