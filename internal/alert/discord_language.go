package alert

import (
	"fmt"
	"strings"
	"time"
)

type reasonText struct{ title, meaning, check string }

// These are explanations of observed states, not a diagnosis of their cause.
// Original codes and cluster messages remain visible as source evidence.
var koreanReasons = map[string]reasonText{
	"ImagePullBackOff":           {"이미지 다운로드 재시도 대기", "이미지 다운로드에 실패해 다음 재시도를 기다리고 있습니다.", "이미지 경로·태그, 레지스트리 인증과 접근 가능 여부를 확인하세요. 재시작만으로는 해결되지 않습니다."},
	"ErrImagePull":               {"이미지 다운로드 실패", "컨테이너 이미지를 내려받지 못했습니다.", "이미지 경로·태그와 레지스트리 접근 권한을 확인하세요. 재시작만으로는 해결되지 않습니다."},
	"InvalidImageName":           {"잘못된 이미지 이름", "설정된 이미지 이름이나 참조 형식을 사용할 수 없습니다.", "이미지 이름과 태그 형식을 수정하세요."},
	"CrashLoopBackOff":           {"컨테이너 반복 종료", "컨테이너가 반복해서 종료되어 재시작 간격이 늘어난 상태입니다.", "직전 컨테이너 로그와 종료 코드를 확인하세요."},
	"OOMKilled":                  {"메모리 부족으로 종료", "컨테이너가 메모리 부족 상태에서 강제 종료되었습니다.", "메모리 제한·실제 사용량과 노드 상태를 확인하세요. 재시작만으로 재발을 막을 수 없습니다."},
	"CreateContainerConfigError": {"컨테이너 설정 오류", "컨테이너 생성에 필요한 설정을 적용하지 못했습니다.", "참조하는 Secret·ConfigMap과 Pod 이벤트를 확인하세요."},
	"CreateContainerError":       {"컨테이너 생성 실패", "컨테이너를 생성하지 못했습니다.", "실행 명령, 볼륨 마운트, 보안 설정과 이벤트를 확인하세요."},
	"RunContainerError":          {"컨테이너 실행 실패", "생성된 컨테이너를 실행하지 못했습니다.", "실행 명령, 볼륨 마운트, 보안 설정과 이벤트를 확인하세요."},
	"Unschedulable":              {"Pod 배치 불가", "Pod를 실행할 노드를 배정하지 못했습니다.", "자원 요청, 노드 여유 자원, taint와 노드 선택 조건을 확인하세요."},
	"Evicted":                    {"노드에서 Pod 퇴거", "노드가 Pod를 퇴거시켰습니다.", "노드의 메모리·디스크 등 자원 압박과 이벤트를 확인하세요."},
	"Restarted":                  {"컨테이너 재시작", "컨테이너 재시작 횟수가 증가했습니다.", "직전 컨테이너 로그와 종료 이유를 확인하세요."},
	"Error":                      {"오류로 종료", "컨테이너가 오류 상태로 종료되었습니다.", "종료 코드와 직전 컨테이너 로그를 확인하세요."},
	"Unknown":                    {"종료 상태 불명", "컨테이너 종료 상태를 확인할 수 없습니다.", "노드·컨테이너 런타임 상태와 같은 시각에 재시작한 Pod를 확인하세요."},
	"ContainerStatusUnknown":     {"컨테이너 상태 불명", "컨테이너 상태를 확인할 수 없습니다.", "노드와 컨테이너 런타임 상태를 확인하세요."},
	"Completed":                  {"프로세스 정상 종료", "컨테이너 프로세스가 정상 종료된 상태입니다.", "계속 실행되어야 하는 서비스인지 일회성 작업인지 확인하세요."},
	"Failed":                     {"Pod 실행 실패", "Pod가 실패 상태로 변경되었습니다.", "Pod 이벤트와 컨테이너 로그를 확인하세요."},
	"ProgressDeadlineExceeded":   {"배포 진행 시간 초과", "새 Pod가 준비되지 않아 배포 진행 제한 시간을 초과했습니다.", "새 Pod의 이미지·이벤트·로그를 확인하고 배포 변경 내용을 검토하세요."},
	"NotReady":                   {"노드 준비 안 됨", "노드가 준비 상태가 아니어서 Pod 실행에 영향을 줄 수 있습니다.", "노드 상태와 kubelet을 확인하세요."},
	"Ready":                      {"노드 준비 완료", "노드가 준비 상태로 변경되었습니다.", "관련 Pod의 복구 상태를 확인하세요."},
	"MemoryPressure":             {"노드 메모리 압박", "노드가 메모리 압박 상태를 보고했습니다.", "노드의 메모리 사용량과 Pod 자원 요청을 확인하세요."},
	"DiskPressure":               {"노드 디스크 압박", "노드가 디스크 자원 압박을 보고했습니다.", "디스크 여유 공간과 inode 사용량을 확인하세요."},
	"PIDPressure":                {"노드 프로세스 자원 압박", "노드가 사용 가능한 프로세스 수의 압박을 보고했습니다.", "프로세스 수와 노드 제한을 확인하세요."},
	"NetworkUnavailable":         {"노드 네트워크 사용 불가", "노드가 네트워크 사용 불가 상태를 보고했습니다.", "노드 네트워크와 CNI 상태를 확인하세요."},
	"Created":                    {"리소스 생성", "새 리소스가 감지되었습니다.", "아래 원문에서 이미지나 설정 키 등 변경 내용을 확인하세요."},
	"Deleted":                    {"리소스 삭제", "리소스 삭제가 감지되었습니다.", "의도한 삭제인지 확인하세요."},
	"ImageChanged":               {"이미지 변경", "워크로드의 컨테이너 이미지가 변경되었습니다.", "변경한 이미지와 배포 준비 상태를 확인하세요."},
	"Scaled":                     {"복제본 수 변경", "워크로드의 복제본 수가 변경되었습니다.", "원하는 복제본 수와 준비된 Pod 수를 확인하세요."},
	"DataChanged":                {"설정 데이터 변경", "ConfigMap 데이터 변경이 감지되었습니다.", "변경한 설정 키와 관련 워크로드를 확인하세요."},
	"ConfigMapChanged":           {"설정 변경", "ConfigMap 변경이 감지되었습니다.", "변경한 설정과 관련 워크로드를 확인하세요."},
	"Changed":                    {"설정 변경", "설정 변경이 감지되었습니다.", "변경한 설정 키와 관련 워크로드를 확인하세요."},
	"Cordoned":                   {"노드 신규 배치 차단", "노드에 새 Pod를 배치하지 않도록 설정되었습니다.", "의도한 유지보수 상태인지 확인하세요."},
	"Uncordoned":                 {"노드 신규 배치 허용", "노드에 새 Pod를 다시 배치할 수 있게 되었습니다.", "관련 Pod의 배치 상태를 확인하세요."},
}

func discordDescription(b *Batch, language string) string {
	if language != "ko" {
		return discordEnglish(b)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "전송 시각: %s\n", b.SentAt.UTC().Format(time.RFC3339))
	for _, a := range b.Alerts {
		severity := "정보"
		if a.Severity == Warning {
			severity = "경고"
		}
		object := a.Name
		if a.Namespace != "" {
			object = a.Namespace + "/" + a.Name
		}
		explanation, known := koreanReasons[a.Reason]
		if !known {
			explanation = reasonText{"리소스 상태 변경", "상태 변경이 감지되었습니다. 원문과 MCP 조회로 구체적인 원인을 확인하세요.", "Pod·워크로드 상태, 이벤트와 로그를 확인하세요."}
		}
		fmt.Fprintf(&text, "\n발생 시각: %s [%s]\n대상: %s %s %s\n상태: %s (%s)\n설명: %s\n", a.Time.UTC().Format(time.RFC3339), severity, a.Cluster, a.Kind, object, explanation.title, a.Reason, explanation.meaning)
		// Only specialize a registry error for image-pull states; never infer it from unrelated logs.
		if a.Reason == "ImagePullBackOff" || a.Reason == "ErrImagePull" {
			lower := strings.ToLower(a.Summary)
			if strings.Contains(lower, "not found") || strings.Contains(lower, "manifest unknown") {
				text.WriteString("오류 내용: 레지스트리에서 지정한 이미지를 찾지 못했습니다. 이미지 경로와 태그를 확인하세요.\n")
			}
			if strings.Contains(lower, "unauthorized") || strings.Contains(lower, "authentication required") {
				text.WriteString("오류 내용: 이미지 레지스트리 인증이 필요하거나 거부되었습니다.\n")
			}
		}
		fmt.Fprintf(&text, "확인할 사항: %s\n원문: %s\n", explanation.check, a.Summary)
		if a.Suppressed > 0 {
			fmt.Fprintf(&text, "유사 알림 %d회 추가\n", a.Suppressed)
		}
		if a.Restart != nil {
			fmt.Fprintf(&text, "필요 시 재시작 승인: %s %s/%s %s\n", a.Restart.Kind, a.Restart.Namespace, a.Restart.Name, a.Restart.URL)
		}
	}
	if b.Dropped > 0 {
		fmt.Fprintf(&text, "\n알림 %d건 누락\n", b.Dropped)
	}
	return text.String()
}

func discordEnglish(b *Batch) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Sent: %s\n", b.SentAt.UTC().Format(time.RFC3339))
	for _, a := range b.Alerts {
		fmt.Fprintf(&text, "\n%s [%s] %s %s %s/%s: %s\n%s\n", a.Time.UTC().Format(time.RFC3339), a.Severity, a.Cluster, a.Kind, a.Namespace, a.Name, a.Reason, a.Summary)
		if a.Suppressed > 0 {
			fmt.Fprintf(&text, "(+%d similar)\n", a.Suppressed)
		}
		if a.Hint != "" {
			fmt.Fprintf(&text, "Hint: %s\n", a.Hint)
		}
		if a.Restart != nil {
			fmt.Fprintf(&text, "Restart suggestion: %s %s/%s %s\n", a.Restart.Kind, a.Restart.Namespace, a.Restart.Name, a.Restart.URL)
		}
	}
	if b.Dropped > 0 {
		fmt.Fprintf(&text, "\n%d alerts dropped\n", b.Dropped)
	}
	return text.String()
}

// Each output gets a copy; one destination cannot rewrite another's evidence.
func localizedBatch(source *Batch, language string) *Batch {
	if language == "" {
		return source
	}
	b := *source
	b.Language = language
	b.Alerts = append([]Alert(nil), source.Alerts...)
	for i := range b.Alerts {
		a := &b.Alerts[i]
		display := Display{Reason: a.Reason, Summary: a.Summary, Hint: a.Hint}
		if language == "ko" {
			value, known := koreanReasons[a.Reason]
			if !known {
				value = reasonText{"리소스 상태 변경", "상태 변경이 감지되었습니다.", "MCP로 리소스 상태·이벤트·로그를 확인하세요."}
			}
			display = Display{Reason: value.title + " (" + a.Reason + ")", Summary: value.meaning, Hint: value.check}
			if (a.Reason == "ImagePullBackOff" || a.Reason == "ErrImagePull") && (strings.Contains(strings.ToLower(a.Summary), "not found") || strings.Contains(strings.ToLower(a.Summary), "manifest unknown")) {
				display.Summary += " 레지스트리에서 지정한 이미지를 찾지 못했습니다."
			}
		}
		a.Display = &display
	}
	if language == "ko" {
		b.Text = discordDescription(&b, "ko")
	} else {
		b.Text = render(&b)
	}
	return &b
}
