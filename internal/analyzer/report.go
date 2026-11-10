package analyzer

import (
	"fmt"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/alert"
)

// The report is read on a phone in a chat channel: the cause first, then what
// to do, each command alone in a code block so it can be copied in one tap.
const (
	formatKO = `Report in Korean, in plain everyday Korean, not translated English. Keep exact error codes, resource names and commands unchanged. The reader is on a phone and wants to know the cause and what to do. Use exactly this layout, at most 15 lines outside code blocks, no other sections, no preamble and no closing remarks:

**원인** (확정 | 추정 | 증거 부족)
One or two sentences: what failed and why. Name the resource. If evidence is missing, say 증거 부족 and what is missing.

**지금 할 일**
1. One short line per step, most important first, at most 3 steps. Start each with the action, not the background.
%s

**근거**
- At most 3 bullets. Each is one short quoted log line, event or status with its evidence ID such as [E2].

**더 확인할 것**
- Only when something still needs checking; at most 2 bullets. Omit this section otherwise.

Do not repeat the alert, do not list tools you called, do not describe impact, risk or rollback at length: one clause per step is enough.`
	formatEN = `Report in English. Preserve exact error codes, resource names and commands. The reader is on a phone and wants to know the cause and what to do. Use exactly this layout, at most 15 lines outside code blocks, no other sections, no preamble and no closing remarks:

**Cause** (confirmed | likely | insufficient evidence)
One or two sentences: what failed and why. Name the resource. If evidence is missing, say so and what is missing.

**Do now**
1. One short line per step, most important first, at most 3 steps. Start each with the action, not the background.
%s

**Evidence**
- At most 3 bullets. Each is one short quoted log line, event or status with its evidence ID such as [E2].

**Still to check**
- Only when something still needs checking; at most 2 bullets. Omit this section otherwise.

Do not repeat the alert, do not list tools you called, do not describe impact, risk or rollback at length: one clause per step is enough.`
	stepsChecks  = `Steps are checks for the operator only. Do not add an action proposal section and do not propose changes.`
	stepsActions = `A step is either an approval link or a command. For a fix in the action catalog, call action_link (or use a URL returned by k8s_remediation_options) and write the step as a Markdown link: [what it does](approval URL). You may propose a fix only by calling action_link with verified catalog arguments; do not invent approval links. Every action requires a person to sign in and approve it, so write steps as proposals, NEVER tool calls or actions you have taken. Prefer the least invasive action. For anything else the operator must run, put the complete command alone in its own fenced code block right under the step, one command per block, nothing else inside the block, so it can be copied as is. Use only verified values; if a value such as a replacement image tag is unknown, write it as an UPPER_CASE placeholder requiring validation and say so in the step. An MCP cluster name is not a verified kubectl context: leave --context out unless the real context is known. Do not recommend a restart as a fix for a nonexistent image, missing configuration or a memory limit issue.`
)

func reportInstructions(c Config) string {
	format, steps := formatKO, stepsChecks
	if c.Report.Language == "en" {
		format = formatEN
	}
	if c.Report.SuggestActions {
		steps = stepsActions
	}
	return instructions + "\n" + fmt.Sprintf(format, steps)
}

// evidenceFooter maps the cited IDs to the reads behind them on one line.
// Arguments stay in the audit log; repeating them here buried the report.
func evidenceFooter(c Config, evidence []string, calls, tokens int, total, model time.Duration) string {
	failed := " (실패)"
	if c.Report.Language == "en" {
		failed = " (failed)"
	}
	names := make([]string, 0, len(evidence))
	for _, e := range evidence {
		fields := strings.Fields(e)
		if len(fields) < 2 {
			names = append(names, e)
			continue
		}
		name := fields[0] + " " + fields[1]
		if len(fields) > 2 && !strings.HasSuffix(e, "(ok)") {
			name += failed
		}
		names = append(names, name)
	}
	if c.Report.Language == "en" {
		return "\n\n" + strings.Join(names, " · ") + fmt.Sprintf("\n%d reads · %d tokens · %s (model %s) · Model analysis; the cause is not guaranteed.", calls, tokens, reportDuration(total, "en"), reportDuration(model, "en"))
	}
	return "\n\n" + strings.Join(names, " · ") + fmt.Sprintf("\n조회 %d회 · 사용 토큰 %d · 소요 %s (모델 %s) · 모델 분석이며 원인 확정을 보장하지 않습니다.", calls, tokens, reportDuration(total, "ko"), reportDuration(model, "ko"))
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
	choices := ""
	if a.Kind == "Pod" && c.Report.ApprovalURL != "" {
		hint := ""
		if a.Workload != nil {
			hint = a.Workload.Kind + "/" + a.Workload.Name
		}
		link, err := action.OptionsURL(c.Report.ApprovalURL, a.Cluster, a.Namespace, a.Name, hint)
		if err != nil && hint != "" {
			link, err = action.OptionsURL(c.Report.ApprovalURL, a.Cluster, a.Namespace, a.Name, "")
		}
		if err == nil {
			label := "모든 선택지 보기"
			if c.Report.Language == "en" {
				label = "All choices"
			}
			choices = "\n\n[" + label + "](" + link + ")"
		}
	}
	if c.Report.Language == "en" {
		if status == "failed" {
			text = "Analysis failed or insufficient evidence (" + code + "): check investigation limits, model/MCP connectivity and read permissions. No cause was confirmed and no changes were made."
			if result.Started {
				text += "\nElapsed: " + reportDuration(result.Total, "en")
			}
		}
		return fmt.Sprintf("**opsgate analysis [%s]** %s %s %s/%s · alert %s\n\n%s", status, a.Cluster, a.Kind, a.Namespace, a.Name, a.Time.UTC().Format("15:04 UTC"), text) + choices
	}
	label := "완료"
	if status == "failed" {
		label = "실패"
		text = "분석 실패 또는 증거 부족 (" + code + "): 조사 한도, 모델/MCP 연결 및 조회 권한을 확인하세요. 원인은 확정되지 않았으며 변경은 수행하지 않았습니다."
		if result.Started {
			text += "\n소요 " + reportDuration(result.Total, "ko")
		}
	}
	return fmt.Sprintf("**opsgate 분석 [%s]** %s %s %s/%s · 알림 %s\n\n%s", label, a.Cluster, a.Kind, a.Namespace, a.Name, a.Time.UTC().Format("15:04 UTC"), text) + choices
}
