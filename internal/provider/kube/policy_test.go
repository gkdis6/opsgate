package kube

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNamespacePolicy(t *testing.T) {
	tests := []struct {
		name        string
		allow, deny []string
		ns          string
		want        bool
	}{
		{"empty", nil, nil, "prod", false}, {"all", []string{"*"}, nil, "prod", true},
		{"literal", []string{"prod"}, nil, "prod", true}, {"literal miss", []string{"prod"}, nil, "production", false},
		{"glob", []string{"prod-*"}, nil, "prod-api", true}, {"question", []string{"dev-?"}, nil, "dev-a", true},
		{"class", []string{"dev-[ab]"}, nil, "dev-b", true}, {"deny wins", []string{"*"}, []string{"kube-*"}, "kube-system", false},
		{"deny all", []string{"prod"}, []string{"*"}, "prod", false}, {"union", []string{"dev", "prod-*"}, nil, "dev", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (namespacePolicy{tt.allow, tt.deny}).allowed(tt.ns); got != tt.want {
				t.Fatalf("got %v", got)
			}
		})
	}
	for _, p := range []namespacePolicy{{allow: []string{"["}}, {deny: []string{"["}}} {
		if p.validate() == nil {
			t.Fatal("accepted malformed glob")
		}
	}
}

func TestNamespaceQueries(t *testing.T) {
	for _, tt := range []struct {
		name        string
		allow, deny []string
		ns          string
		want        []string
		actions     int
		denied      bool
	}{
		{"empty", nil, nil, "", nil, 0, false}, {"all", []string{"*"}, nil, "", []string{""}, 0, false},
		{"literals", []string{"prod", "dev", "prod"}, []string{"dev"}, "", []string{"prod"}, 0, false},
		{"glob", []string{"prod-*"}, []string{"prod-secret"}, "", []string{"prod-api"}, 1, false},
		{"explicit", []string{"prod"}, nil, "prod", []string{"prod"}, 0, false},
		{"denied", []string{"prod"}, nil, "other", nil, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod-api"}}, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod-secret"}})
			p := &provider{client: client, policy: namespacePolicy{tt.allow, tt.deny}}
			got, err := p.namespaces(context.Background(), tt.ns)
			if errors.Is(err, tool.ErrDenied) != tt.denied || (!tt.denied && err != nil) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("%v != %v", got, tt.want)
			}
			if len(client.Actions()) != tt.actions {
				t.Fatal(client.Actions())
			}
		})
	}
}

func testProvider(objects ...runtime.Object) *provider {
	return &provider{client: fake.NewClientset(objects...), cfg: config.KubernetesCluster{MaxLogLines: 500, AllowLogs: true}, policy: namespacePolicy{[]string{"*"}, []string{"private"}}}
}

func TestListQueryPlan(t *testing.T) {
	for _, tt := range []struct {
		name  string
		allow []string
		want  []string
	}{
		{"all", []string{"*"}, []string{"list:pods:"}},
		{"literal", []string{"public", "private"}, []string{"list:pods:public"}},
		{"glob", []string{"pub*"}, []string{"list:namespaces:", "list:pods:public"}},
		{"none", nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := testProvider(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "public"}}, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "private"}}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "visible", Namespace: "public"}}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "hidden", Namespace: "private"}})
			p.policy.allow = tt.allow
			items, err := p.collect(context.Background(), resource{"pods", "Pod", false}, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if tt.name != "none" && (len(items) != 1 || str(items[0], "metadata", "name") != "visible") {
				t.Fatal(items)
			}
			var got []string
			for _, a := range p.client.(*fake.Clientset).Actions() {
				got = append(got, a.GetVerb()+":"+a.GetResource().Resource+":"+a.GetNamespace())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
