package action

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/redact"
)

// A link that parses must be one the builder could have made: rebuilding it
// gives the same tool, arguments and source, and every argument passes the
// catalog rules. A query can never smuggle in an unknown field.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"/approve/action/k8s_rollout_restart?source=rule&kind=deployment&namespace=apps&name=web",
		"/approve/action/k8s_scale?kind=deployment&namespace=apps&name=web&replicas=2&source=ai",
		"/approve/restart/default/deployment/apps/web",
		"/approve/restore-image/default/apps/web?container=app&expected_image=a:2&image=a:1&revert=true",
		"/approve/action/k8s_delete_pod?namespace=apps&name=web-1&name=web-2",
		"/approve/restart/a%2Fb/deployment/apps/web?name=x",
		"/approve/action/host_restart_unit?unit=nginx.service",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := url.Parse(raw)
		if err != nil {
			return
		}
		e, args, source, err := Parse(u)
		if err != nil {
			return
		}
		if source != "rule" && source != "ai" {
			t.Fatalf("source %q", source)
		}
		if _, ok := args["source"]; ok {
			t.Fatal("source became a tool argument")
		}
		if err := Validate(e.Tool, args); err != nil {
			t.Fatalf("parsed arguments do not validate: %v", err)
		}
		for k, v := range args {
			if s, ok := v.(string); ok && (strings.Contains(s, redact.Mask) || redact.String(s) != s) {
				t.Fatalf("argument %q carries maskable text %q", k, s)
			}
		}
		link, err := URL("https://ops.example", e.Tool, args, source)
		if err != nil {
			t.Fatalf("rebuild %v: %v", args, err)
		}
		again, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		e2, args2, source2, err := Parse(again)
		if err != nil || e2.Tool != e.Tool || source2 != source || !reflect.DeepEqual(args, args2) {
			t.Fatalf("round trip changed %q -> %q: %v %v %v", raw, link, args, args2, err)
		}
	})
}

// Options links are navigation only, but they still must not carry text that
// escapes their path segments or would be masked on display.
func FuzzOptionsURL(f *testing.F) {
	f.Add("default", "apps", "web-1", "deployment/web")
	f.Add("a", "b/c", "..", "")
	f.Add("default", "apps", "web", "statefulset/db?x=1")
	f.Fuzz(func(t *testing.T, cluster, namespace, pod, workload string) {
		link, err := OptionsURL("https://ops.example", cluster, namespace, pod, workload)
		if err != nil {
			return
		}
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		want := "/approve/options/" + cluster + "/" + namespace + "/" + pod
		if u.Host != "ops.example" || u.Path != want || u.EscapedPath() != want || u.Fragment != "" {
			t.Fatalf("link %q escapes its path", link)
		}
		if q := u.Query(); len(q) > 1 || workload != "" && q.Get("workload") != workload {
			t.Fatalf("link %q query %v", link, q)
		}
	})
}
