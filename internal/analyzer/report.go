package analyzer

import (
	"fmt"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
)

func reportInstructions(c Config) string {
	language := `Report in Korean with sections 확인된 사실, 추정 원인, 추가 확인 사항. Explain causes in Korean, not untranslated English sentences. Keep exact error codes, resource names, commands and quoted source evidence unchanged. If evidence is missing, say 증거 부족.`
	if c.Report.Language == "en" {
		language = `Report in English with sections Confirmed facts, Hypotheses, Outstanding checks. Explain causes in English. Preserve exact error codes, resource names, commands and quoted evidence. Clearly identify insufficient evidence.`
	}
	actions := `Do not add an action proposal section; list outstanding checks only.`
	if c.Report.SuggestActions {
		actions = `Add an actionable Recommended actions section (권장 조치 in Korean). For each recommendation include the exact target cluster/namespace/kind/name/container, the supporting evidence, an approval URL returned by k8s_remediation_options or action_link, expected impact and risk, and verification/rollback steps. You may propose a fix only by calling action_link with verified catalog arguments. Every action requires a person to sign in and approve it. Prefer the least invasive action. These are proposals for the operator, NEVER tool calls or actions you have taken. Use only verified parameter values. If a replacement image tag or another necessary value is unknown, mark it explicitly as a placeholder requiring validation, and do not propose a fix until validated. Do not invent approval links. The host separately validates remediation URLs before attaching buttons. Do not claim those verified URLs are unavailable merely because they came from the remediation tool rather than the initial alert. An MCP cluster name is not a verified kubectl context; use a clearly marked context placeholder unless the actual context is independently known. Inspect the owning workload if needed. Do not recommend a restart as a fix for a nonexistent image, missing configuration or a memory limit issue. Clearly say that proposed changes have not been executed.`
	}
	return instructions + "\n" + language + "\n" + actions
}

func evidenceFooter(c Config, evidence []string, calls, tokens int, total, model time.Duration) string {
	if c.Report.Language == "en" {
		return "\n\nEvidence:\n" + strings.Join(evidence, "\n") + fmt.Sprintf("\n%d reads · %d tokens · %s (model %s) · Model analysis does not guarantee a confirmed root cause.", calls, tokens, reportDuration(total, "en"), reportDuration(model, "en"))
	}
	return "\n\n조회 근거:\n" + strings.Join(evidence, "\n") + fmt.Sprintf("\n조회 %d회 · 사용 토큰 %d · 소요 %s (모델 %s) · 모델 분석 결과이며 원인 확정을 보장하지 않습니다.", calls, tokens, reportDuration(total, "ko"), reportDuration(model, "ko"))
}

func reportDuration(d time.Duration, language string) string {
	seconds := max(int64(1), int64(d.Round(time.Second)/time.Second))
	if language == "en" {
		if seconds < 60 {
			return fmt.Sprintf("%ds", seconds)
		}
		return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
	}
	if seconds < 60 {
		return fmt.Sprintf("%d초", seconds)
	}
	return fmt.Sprintf("%d분 %d초", seconds/60, seconds%60)
}

func reportNotification(c Config, b alert.Batch, result investigation, status, code string) string {
	a := b.Alerts[0]
	text := result.Text
	if c.Report.Language == "en" {
		if status == "failed" {
			text = "Analysis failed or insufficient evidence (" + code + "): check investigation limits, model/MCP connectivity and read permissions. No cause was confirmed and no changes were made."
			if result.Started {
				text += "\nElapsed: " + reportDuration(result.Total, "en")
			}
		}
		return fmt.Sprintf("opsgate automatic analysis [%s]\n%s %s %s/%s\nAlert occurred: %s\n\n%s", status, a.Cluster, a.Kind, a.Namespace, a.Name, a.Time.UTC().Format(time.RFC3339), text)
	}
	label := "완료"
	if status == "failed" {
		label = "실패"
		text = "분석 실패 또는 증거 부족 (" + code + "): 조사 한도, 모델/MCP 연결 및 조회 권한을 확인하세요. 원인은 확정되지 않았으며 변경은 수행하지 않았습니다."
		if result.Started {
			text += "\n소요 " + reportDuration(result.Total, "ko")
		}
	}
	return fmt.Sprintf("opsgate 자동 분석 [%s]\n%s %s %s/%s\n알림 발생: %s\n\n%s", label, a.Cluster, a.Kind, a.Namespace, a.Name, a.Time.UTC().Format(time.RFC3339), text)
}
