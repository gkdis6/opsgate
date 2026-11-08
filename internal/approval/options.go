package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/gkdis6/opsgate/internal/ui"
)

// SetOptions connects the browser to the same read-only rule engine as MCP.
func (m *Manager) SetOptions(evaluate func(context.Context, tool.Caller, string, string, string, string) (action.Options, error)) {
	m.options = evaluate
}

func (m *Manager) optionsRequest(w http.ResponseWriter, r *http.Request, subject string) {
	if len(r.URL.RawQuery) > 4096 {
		ui.Message(w, http.StatusBadRequest, "Invalid choices link", "Query is too long.")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 1 || (len(q) == 1 && len(q["workload"]) != 1) {
		ui.Message(w, http.StatusBadRequest, "Invalid choices link", "Invalid workload hint.")
		return
	}
	cluster, ns, pod, workload := r.PathValue("cluster"), r.PathValue("namespace"), r.PathValue("pod"), q.Get("workload")
	if _, err := action.OptionsURL("", cluster, ns, pod, workload); err != nil {
		ui.Message(w, http.StatusBadRequest, "Invalid choices link", "Invalid target.")
		return
	}
	access, ok := m.browser.(browserAccess)
	if !ok || m.options == nil || m.direct == nil {
		unavailablePage(w)
		return
	}
	scopes, tools, known := access.Access(subject)
	caller := tool.Caller{User: subject, Transport: "web", Scopes: scopes, Tools: tools, Source: "rule"}
	if subject == "" || !known || !caller.Has(config.ScopeRead) || !caller.Has(config.ScopeWrite) || !caller.AllowsTool("k8s_remediation_options") {
		ui.Message(w, http.StatusForbidden, "Choices unavailable", "This account cannot read and approve these actions.")
		return
	}
	options, err := m.options(r.Context(), caller, cluster, ns, pod, workload)
	if err != nil {
		ui.Message(w, http.StatusForbidden, "Choices unavailable", "The target is unavailable or not allowed.")
		return
	}
	// Rebuild local catalog URLs and check this person's write access. The
	// provider result cannot inject an external navigation target or bypass Plan.
	choices := []action.Choice{}
	for _, choice := range options.Actions {
		u, err := url.Parse(choice.URL)
		if err != nil {
			continue
		}
		entry, args, source, err := action.Parse(u)
		if err != nil || source != "rule" || args["cluster"] != cluster || args["namespace"] != ns {
			continue
		}
		d := m.direct(entry.Tool)
		if d == nil {
			continue
		}
		raw, _ := json.Marshal(args)
		if _, _, err := d.Plan(r.Context(), caller, raw); err != nil {
			continue
		}
		choice.URL, err = action.URL("", entry.Tool, args, "rule")
		if err != nil {
			continue
		}
		choice.Label, choice.LabelKO, choice.Source = entry.Label("en"), entry.Label("ko"), "rule"
		choices = append(choices, choice)
	}
	options.Actions = choices
	ui.Render(w, http.StatusOK, optionsPage, options)
}

var optionsPage = ui.NewTemplate(`{{define "title"}}Choose an action / 조치 선택{{end}}
{{define "content"}}<p><a href="/approvals">Approval inbox</a></p>
<h1>Choose an action / 조치 선택</h1>
<p>Current state is checked each time. Choose an action, then review and approve its plan.<br>현재 상태를 다시 확인합니다. 조치를 선택한 뒤 계획을 검토하고 승인하세요.</p>
{{if .PodGone}}<p>The pod is gone. Any choices below are for the verified current workload.<br>파드가 사라졌습니다. 아래 선택지는 확인된 현재 워크로드에 대한 것입니다.</p>{{end}}
{{range .Actions}}<div class="target"><a class="button primary" href="{{.URL}}">{{.Label}} / {{.LabelKO}}</a><p>{{.Reason}}<br>{{.ReasonKO}}</p><p>Source / 출처: rule</p></div>{{else}}<p>No applicable actions / 적용 가능한 조치가 없습니다.</p>{{end}}
{{if .Hint}}<p>{{.Hint}}</p>{{end}}{{end}}`)

// SetHostOptions connects the browser to the host provider's rule engine.
func (m *Manager) SetHostOptions(evaluate func(context.Context, tool.Caller, string, string) (action.Options, error)) {
	m.hostOptions = evaluate
}

func (m *Manager) hostOptionsRequest(w http.ResponseWriter, r *http.Request, subject string) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	if r.URL.RawQuery != "" {
		ui.Message(w, http.StatusBadRequest, "Invalid choices link", "This link takes no query.")
		return
	}
	if _, err := action.HostOptionsURL("", kind, name); err != nil {
		ui.Message(w, http.StatusBadRequest, "Invalid choices link", "Invalid target.")
		return
	}
	access, ok := m.browser.(browserAccess)
	if !ok || m.hostOptions == nil || m.direct == nil {
		unavailablePage(w)
		return
	}
	scopes, tools, known := access.Access(subject)
	caller := tool.Caller{User: subject, Transport: "web", Scopes: scopes, Tools: tools, Source: "rule"}
	if subject == "" || !known || !caller.Has(config.ScopeRead) || !caller.Has(config.ScopeWrite) {
		ui.Message(w, http.StatusForbidden, "Choices unavailable", "This account cannot read and approve these actions.")
		return
	}
	options, err := m.hostOptions(r.Context(), caller, kind, name)
	if err != nil {
		ui.Message(w, http.StatusForbidden, "Choices unavailable", "The target is unavailable or not allowed.")
		return
	}
	// As for Kubernetes: rebuild local catalog links for this exact target and
	// keep only those this person could plan.
	choices := []action.Choice{}
	for _, choice := range options.Actions {
		u, err := url.Parse(choice.URL)
		if err != nil {
			continue
		}
		entry, args, source, err := action.Parse(u)
		if err != nil || source != "rule" || len(args) != 1 || args[kind] != name {
			continue
		}
		d := m.direct(entry.Tool)
		if d == nil {
			continue
		}
		raw, _ := json.Marshal(args)
		if _, _, err := d.Plan(r.Context(), caller, raw); err != nil {
			continue
		}
		if choice.URL, err = action.URL("", entry.Tool, args, "rule"); err != nil {
			continue
		}
		choice.Label, choice.LabelKO, choice.Source = entry.Label("en"), entry.Label("ko"), "rule"
		choices = append(choices, choice)
	}
	options.Actions = choices
	ui.Render(w, http.StatusOK, optionsPage, options)
}
