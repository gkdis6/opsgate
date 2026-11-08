package action

import (
	"net/url"
	"strings"
	"testing"
)

func TestCatalogRoundTrip(t *testing.T) {
	for _, name := range []string{"k8s_rollout_restart", "k8s_restore_image", "k8s_scale", "k8s_delete_pod"} {
		e, ok := Lookup(name)
		if !ok {
			t.Fatal(name)
		}
		args := map[string]any{}
		for _, key := range e.Arguments {
			args[key] = "a/b c?&=한글"
		}
		if name == "k8s_scale" {
			args["replicas"] = 3
		}
		link, err := URL("https://ops.example/", name, args, "ai")
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(link)
		entry, got, source, err := Parse(u)
		if err != nil || entry.Tool != name || source != "ai" || len(got) != len(args) || got["name"] != args["name"] {
			t.Fatalf("%s: %v %v", link, got, err)
		}
		if _, ok := got["source"]; ok {
			t.Fatal("source became an argument")
		}
		if entry.Label("en") == "" || entry.Label("ko") == "" {
			t.Fatal("missing labels")
		}
		if entry.Reversible != (name == "k8s_scale" || name == "k8s_restore_image") {
			t.Fatal("wrong undo metadata")
		}
	}
	if _, ok := Lookup("note_trash"); ok {
		t.Fatal("note in catalog")
	}
}

func TestLinkAttackQueries(t *testing.T) {
	base := "/approve/action/k8s_scale?kind=deployment&namespace=apps&name=web&replicas=2"
	for _, tail := range []string{"&approval_id=forged", "&source=ai&source=rule", "&name=other", "&extra=value", "&replicas=3", "&source=other", "&image=secret", "&bad=%zz", "&bad=x;y"} {
		u, _ := url.Parse(base + tail)
		if _, _, _, err := Parse(u); err == nil {
			t.Errorf("accepted %s", tail)
		}
	}
	for _, value := range []string{"2.5", "null", "2147483648", "no"} {
		u, _ := url.Parse(strings.Replace(base, "replicas=2", "replicas="+value, 1))
		if _, _, _, err := Parse(u); err == nil {
			t.Errorf("accepted replicas=%s", value)
		}
	}
	for _, secret := range []string{"[REDACTED]", "ghp_12345678901234567890", "PASSWORD=hidden", "Bearer abcdefg"} {
		if _, err := URL("https://ops.example", "k8s_delete_pod", map[string]any{"namespace": "apps", "name": secret}, "rule"); err == nil {
			t.Errorf("linked masked value %q", secret)
		}
	}
	for _, name := range []any{nil, true, 42, []any{"web"}} {
		if err := Validate("k8s_delete_pod", map[string]any{"namespace": "apps", "name": name}); err == nil {
			t.Errorf("accepted wrong type %v", name)
		}
	}
}

func TestLegacyAliasesShareValidation(t *testing.T) {
	for _, path := range []string{"/approve/restart/main/deployment/apps/web", "/approve/restore-image/main/apps/web?container=app&expected_image=bad&image=good"} {
		u, _ := url.Parse(path)
		_, _, source, err := Parse(u)
		if err != nil || source != "rule" {
			t.Fatalf("alias: %v", err)
		}
		q := u.Query()
		q.Set("approval_id", "forged")
		u.RawQuery = q.Encode()
		if _, _, _, err := Parse(u); err == nil {
			t.Fatal("alias smuggled approval_id")
		}
		q.Del("approval_id")
		q.Set("name", "override")
		u.RawQuery = q.Encode()
		if _, _, _, err := Parse(u); err == nil {
			t.Fatal("alias query overrode path")
		}
	}
}

func TestOptionsURL(t *testing.T) {
	got, err := OptionsURL("https://ops.example/", "main", "apps", "web-abc", "deployment/web")
	if err != nil || got != "https://ops.example/approve/options/main/apps/web-abc?workload=deployment%2Fweb" {
		t.Fatalf("%q %v", got, err)
	}
	for _, tc := range []struct{ base, cluster, ns, pod, hint string }{
		{"http://ops.example", "main", "apps", "web", ""},
		{"https://ops.example/redirect", "main", "apps", "web", ""},
		{"https://user@ops.example", "main", "apps", "web", ""},
		{"", "../main", "apps", "web", ""}, {"", "main", "apps", "a/b", ""},
		{"", "main", "apps", "web", "job/web"}, {"", "main", "apps", "web", "deployment/a/b"},
		{"", "main", "apps", "web", "deployment/web\nLocation:evil"},
	} {
		if link, err := OptionsURL(tc.base, tc.cluster, tc.ns, tc.pod, tc.hint); err == nil {
			t.Fatalf("unsafe link %s", link)
		}
	}
}
