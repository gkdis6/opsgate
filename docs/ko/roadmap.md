# 로드맵

[English](../roadmap.md)

아래 기능은 첫 공개 릴리스용으로 구현되어 있습니다. 아직 제3자의 보안 검토를 받지 않았습니다. 향후 작업의 일정은 보장하지 않습니다.

## 완료

- builtin 인증의 패스키 로그인. upstream provider는 `passkey`이며, `opsgate passkey invite`로 일회용 등록 초대를 발급합니다.
- ChatGPT에 필요한 세션 없는 MCP 프로토콜 `2026-07-28`의 무상태 제공.
- stdio를 통한 호스트 및 Kubernetes 조회, 허용 목록, 네임스페이스 거부 목록, 스코프 검사, 출력 제한, 마스킹, JSONL 감사 로그 저장.
- `/mcp`의 Streamable HTTP, 필수 인증, Protected Resource Metadata.
- 내장 OAuth 2.1: PKCE S256을 사용하는 인가 코드, 클라이언트 등록(DCR), 동의, upstream GitHub/OIDC 로그인(Google 포함), 해시로 저장하는 불투명 토큰, 리프레시 토큰 교체 및 재사용 탐지, 선택적 상태 저장.
- issuer 검색/JWKS를 사용하는 외부 JWT 인증, 다이제스트로 설정하는 정적 Bearer 토큰, `-gen-token` 보조 명령.
- 호스트 서비스 재시작과 Kubernetes 워크로드 재시작, 복제본 수 변경, Pod 삭제. 별도 작업 허용 목록, `ops:write`, 사람의 승인으로 제한합니다.
- 프로토콜 버전 간 MCP 클라이언트 내 확인(elicitation), 같은 사용자로 upstream에 로그인하는 웹 승인 대체 경로. 승인은 만료되며, 일회용이고 인자에 바인딩됩니다.
- GoReleaser 릴리스, cosign 키 없는 서명, SBOM, 출처 증명, 컨테이너 이미지, 보안을 강화한 systemd unit, Kubernetes 매니페스트.
- 도구 등록 정책과 사용자별/토큰별 도구 제한. 리다이렉트 호스트 등록 정책, 대기 중인 클라이언트의 관리자 검토, 선택적 알림.
- 메모리에만 저장하고 사용자/도구에 바인딩하는 선택적 승인 시간 창. 기본값은 비활성화입니다.
- 실험적 Obsidian provider: 제한된 볼트 읽기/검색/링크/태그 조회와 승인된 생성/추가/치환/휴지통 이동 작업.
- 실험적 dev provider와 별도 runner: 설정된 프로젝트/에이전트 명령, 승인된 작업 시작, 요청자 소유권/관리자 접근, 로그/diff, 취소, 보존 관리.

## 남은 아이디어

구현을 약속한 항목이 아닌 아이디어입니다. 범위는 보안 검토에 따라 달라질 수 있습니다.

- 최소 권한과 입력 제한을 적용한 Docker 및 PostgreSQL provider.
- 설정 다시 불러오기.
- Client ID Metadata Documents(CIMD).
- 도구별 요청 빈도 제한.
