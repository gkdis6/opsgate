package kube

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func serviceCheckFixture() (*corev1.Service, *corev1.Pod, *discoveryv1.EndpointSlice) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "apps"}, Spec: corev1.ServiceSpec{
		Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.5", Selector: map[string]string{"app": "db"},
		Ports: []corev1.ServicePort{{Name: "postgres", Port: 5432, TargetPort: intstr.FromString("postgres"), Protocol: corev1.ProtocolTCP}},
	}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db-0", Namespace: "apps", Labels: map[string]string{"app": "db"}},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Ports: []corev1.ContainerPort{{Name: "postgres", ContainerPort: 5432}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}
	endpoints := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: "db-1", Namespace: "apps", Labels: map[string]string{discoveryv1.LabelServiceName: "db"}},
		AddressType: discoveryv1.AddressTypeIPv4, Ports: []discoveryv1.EndpointPort{{Name: ptr.To("postgres"), Port: ptr.To(int32(5432)), Protocol: ptr.To(corev1.ProtocolTCP)}},
		Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.1.0.1"}, Hostname: ptr.To("db-0"), Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true), Serving: ptr.To(true)}}},
	}
	return svc, pod, endpoints
}

func assertServiceCheckActions(t *testing.T, client *fake.Clientset, policy namespacePolicy) {
	t.Helper()
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "secrets" {
			t.Fatal("read Secrets", action)
		}
		if ns := action.GetNamespace(); ns != "" && !policy.allowed(ns) {
			t.Fatal("read disallowed namespace", action)
		}
		if action.GetVerb() != "get" && action.GetVerb() != "list" {
			t.Fatal("unexpected action", action)
		}
		if action.GetNamespace() == "" && action.GetResource().Resource != "namespaces" {
			t.Fatal("cluster-wide namespaced read", action)
		}
	}
}

func TestServiceCheck(t *testing.T) {
	for _, tc := range []struct {
		name, host, port, pod string
		change                func(*corev1.Service, *corev1.Pod, *discoveryv1.EndpointSlice)
		want, absent          []string
	}{
		{name: "short name", host: "db", want: []string{"db.apps.svc.cluster.local -> Service apps/db", "1/1 endpoints ready on port 5432", "1 matched pods, 1 ready", "ndots: 5"}},
		{name: "namespace", host: "db.apps", want: []string{"db.apps.svc.cluster.local -> Service apps/db"}},
		{name: "svc suffix", host: "db.apps.svc", want: []string{"db.apps.svc.cluster.local -> Service apps/db"}},
		{name: "FQDN", host: "db.apps.svc.cluster.local", want: []string{"db.apps.svc.cluster.local -> Service apps/db"}},
		{name: "custom domain", host: "db.apps.svc.example.internal", want: []string{"db.apps.svc.example.internal -> Service apps/db"}},
		{name: "domain ending in Service suffix", host: "db.apps.svc.other.svc.cluster.local", want: []string{"db.apps.svc.other.svc.cluster.local -> Service apps/db"}},
		{name: "headless pod", host: "db-0.db.apps.svc.cluster.local", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) { s.Spec.ClusterIP = "None" }, want: []string{"headless Service", "1/1 endpoints ready", "restricted to hostname db-0"}},
		{name: "headless short pod", host: "db-0.db", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) { s.Spec.ClusterIP = "None" }, want: []string{"db-0.db.apps.svc.cluster.local -> Service apps/db", "1/1 endpoints ready"}},
		{name: "headless pod namespace", host: "db-0.db.apps", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) { s.Spec.ClusterIP = "None" }, want: []string{"db-0.db.apps.svc.cluster.local -> Service apps/db"}},
		{name: "headless custom domain", host: "db-0.db.apps.svc.example.internal", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) { s.Spec.ClusterIP = "None" }, want: []string{"db-0.db.apps.svc.example.internal -> Service apps/db"}},
		{name: "headless hostname absent", host: "missing.db", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) { s.Spec.ClusterIP = "None" }, want: []string{"0/0 endpoints ready"}},
		{name: "ordinary service is not hostname", host: "db-0.db", want: []string{"no matching headless Service", "namespace db not allowed"}, absent: []string{"restricted to hostname"}},
		{name: "externalname", host: "db", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			s.Spec.Type = corev1.ServiceTypeExternalName
			s.Spec.ExternalName = "database.example.com"
		}, want: []string{"DNS returns a CNAME to database.example.com; opsgate does not follow it"}, absent: []string{"matched pods", "endpoints ready"}},
		{name: "headless", host: "db", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			s.Spec.ClusterIP = "None"
			s.Spec.PublishNotReadyAddresses = true
		}, want: []string{"DNS returns ready pod IPs", "publishNotReadyAddresses=true", "even when pods are not ready"}},
		{name: "no selector matches", host: "db", change: func(_ *corev1.Service, p *corev1.Pod, _ *discoveryv1.EndpointSlice) { p.Labels = nil }, want: []string{"PROBLEM: selector app=db matches no pods in apps"}},
		{name: "none ready", host: "db", change: func(_ *corev1.Service, p *corev1.Pod, e *discoveryv1.EndpointSlice) {
			p.Status.Conditions[0].Status = corev1.ConditionFalse
			p.Status.Conditions[0].Reason = "ContainersNotReady"
			e.Endpoints[0].Conditions.Ready = ptr.To(false)
		}, want: []string{"none ready", "db-0 phase=Running reason=ContainersNotReady", "0/1 endpoints ready"}},
		{name: "missing named target", host: "db", change: func(_ *corev1.Service, p *corev1.Pod, _ *discoveryv1.EndpointSlice) { p.Spec.Containers[0].Ports = nil }, want: []string{"PROBLEM: named targetPort postgres missing on 1/1 matched pods"}},
		{name: "numeric target informational", host: "db", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			s.Spec.Ports[0].TargetPort = intstr.FromInt32(6543)
		}, want: []string{"INFO: no matched pod declares numeric targetPort 6543; undeclared ports still work"}},
		{name: "absent port", host: "db", port: "80", want: []string{"PROBLEM: Service has no port matching 80"}},
		{name: "named port", host: "db", port: "postgres", want: []string{"1/1 endpoints ready on port 5432"}},
		{name: "number port", host: "db", port: "5432", want: []string{"1/1 endpoints ready on port 5432"}},
		{name: "portless headless", host: "db", change: func(s *corev1.Service, _ *corev1.Pod, e *discoveryv1.EndpointSlice) {
			s.Spec.ClusterIP = "None"
			s.Spec.Ports = nil
			e.Ports = nil
		}, want: []string{"1/1 endpoints ready (Service declares no ports)"}},
		{name: "None with cluster search", host: "db", pod: "db-0", change: func(_ *corev1.Service, p *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			p.Spec.DNSPolicy = corev1.DNSNone
			p.Spec.DNSConfig = &corev1.PodDNSConfig{Nameservers: []string{"10.0.0.10"}, Searches: []string{"apps.svc.example.internal"}}
		}, want: []string{"db.apps.svc.example.internal -> Service apps/db", "1/1 endpoints ready"}, absent: []string{"without cluster search domains"}},

		{name: "manual endpoints", host: "db", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) { s.Spec.Selector = nil }, want: []string{"endpoints are managed manually", "1/1 endpoints ready"}, absent: []string{"matched pods"}},
		{name: "endpoint conditions", host: "db", change: func(_ *corev1.Service, _ *corev1.Pod, e *discoveryv1.EndpointSlice) {
			e.Endpoints = append(e.Endpoints, discoveryv1.Endpoint{Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(false), Serving: ptr.To(true), Terminating: ptr.To(true)}}, discoveryv1.Endpoint{})
		}, want: []string{"2/3 ready, 3 serving (1 defaulted), 1 terminating"}},
		{name: "default DNS", host: "db", pod: "db-0", change: func(_ *corev1.Service, p *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			p.Spec.DNSPolicy = corev1.DNSDefault
		}, want: []string{"dnsPolicy Default without cluster search domains", "Node resolver settings are not inspected"}},
		{name: "none DNS", host: "db", pod: "db-0", change: func(_ *corev1.Service, p *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			p.Spec.DNSPolicy = corev1.DNSNone
		}, want: []string{"dnsPolicy None without cluster search domains"}},
		{name: "host network", host: "db", pod: "db-0", change: func(_ *corev1.Service, p *corev1.Pod, _ *discoveryv1.EndpointSlice) { p.Spec.HostNetwork = true }, want: []string{"hostNetwork with ClusterFirst uses node DNS"}},
		{name: "host network cluster DNS", host: "db", pod: "db-0", change: func(_ *corev1.Service, p *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			p.Spec.HostNetwork = true
			p.Spec.DNSPolicy = corev1.DNSClusterFirstWithHostNet
		}, want: []string{"1/1 endpoints ready"}, absent: []string{"uses node DNS"}},
		{name: "dnsConfig", host: "db.apps.svc.cluster.local", pod: "db-0", change: func(_ *corev1.Service, p *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			p.Spec.DNSConfig = &corev1.PodDNSConfig{Nameservers: []string{"10.0.0.10"}, Searches: []string{"example.com"}, Options: []corev1.PodDNSConfigOption{{Name: "ndots", Value: ptr.To("1")}}}
		}, want: []string{"nameservers: 10.0.0.10", "searches: example.com", "ndots=1", "ndots: 1"}},
		{name: "external RDS", host: "db.abc.us-east-1.rds.amazonaws.com", want: []string{"not a cluster Service name; resolved by upstream DNS"}, absent: []string{"endpoints ready"}},
		{name: "redaction", host: "db", change: func(s *corev1.Service, _ *corev1.Pod, _ *discoveryv1.EndpointSlice) {
			s.Spec.Type = corev1.ServiceTypeExternalName
			s.Spec.ExternalName = "ghp_abcdefghijklm"
		}, want: []string{"[REDACTED]"}, absent: []string{"ghp_abcdefghijklm"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, pod, endpoints := serviceCheckFixture()
			if tc.change != nil {
				tc.change(svc, pod, endpoints)
			}
			client := fake.NewClientset(svc, pod, endpoints)
			p := &provider{client: client, policy: namespacePolicy{allow: []string{"apps"}}}
			out, err := p.serviceCheck(context.Background(), tool.Caller{}, serviceCheckInput{Namespace: "apps", Host: tc.host, Port: tc.port, Pod: tc.pod})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(out, "FINDINGS\n") || !strings.Contains(out, "NetworkPolicies are not evaluated") {
				t.Fatal(out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q:\n%s", want, out)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(out, absent) {
					t.Fatalf("unexpected %q:\n%s", absent, out)
				}
			}
			assertServiceCheckActions(t, client, p.policy)
		})
	}
}

func TestServiceCheckNamespacePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, host  string
		allow, deny []string
		want        string
		denied      bool
	}{
		{"suggestion", "db", []string{"apps", "prod"}, nil, "try db.prod", false},
		{"denied suggestion", "db", []string{"apps", "prod"}, []string{"prod"}, "no matching Service found", false},
		{"wildcard suggestions", "db", []string{"*"}, []string{"private"}, "try db.prod", false},
		{"glob suggestions", "db", []string{"apps", "pr*"}, []string{"private"}, "try db.prod", false},
		{"candidate denied", "db.private.svc.cluster.local", []string{"*"}, []string{"private"}, "namespace private not allowed", false},
		{"lookup namespace denied", "db", []string{"prod"}, nil, "namespace is not allowed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := serviceCheckFixture()
			svc.Namespace = "prod"
			hidden := svc.DeepCopy()
			hidden.Namespace = "private"
			client := fake.NewClientset(svc, hidden, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps"}}, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod"}}, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "private"}})
			p := &provider{client: client, policy: namespacePolicy{allow: tc.allow, deny: tc.deny}}
			out, err := p.serviceCheck(context.Background(), tool.Caller{}, serviceCheckInput{Namespace: "apps", Host: tc.host})
			if tc.denied {
				if !errors.Is(err, tool.ErrDenied) || len(client.Actions()) > 0 {
					t.Fatal(out, err, client.Actions())
				}
				return
			}
			if err != nil || !strings.Contains(out, tc.want) {
				t.Fatal(out, err)
			}
			if slices.Contains(tc.deny, "prod") && strings.Contains(out, "try db.prod") {
				t.Fatal(out)
			}
			if strings.Contains(out, "try db.private") {
				t.Fatal(out)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() == "list" && action.GetResource().Resource == "services" && action.(kt.ListAction).GetListRestrictions().Fields.String() != "metadata.name=db" {
					t.Fatal(action)
				}
			}
			assertServiceCheckActions(t, client, p.policy)
		})
	}
}

func TestServiceCheckInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   serviceCheckInput
		want string
	}{
		{"IPv4", serviceCheckInput{Namespace: "apps", Host: "10.0.0.1"}, "IP addresses are not checked"},
		{"IPv6", serviceCheckInput{Namespace: "apps", Host: "::1"}, "IP addresses are not checked"},
		{"empty", serviceCheckInput{Namespace: "apps"}, "host"},
		{"uppercase", serviceCheckInput{Namespace: "apps", Host: "DB"}, "invalid host"},
		{"URL", serviceCheckInput{Namespace: "apps", Host: "https://db"}, "invalid host"},
		{"underscore", serviceCheckInput{Namespace: "apps", Host: "db_name"}, "invalid host"},
		{"empty label", serviceCheckInput{Namespace: "apps", Host: "db..apps"}, "invalid host"},
		{"trailing dot", serviceCheckInput{Namespace: "apps", Host: "db."}, "invalid host"},
		{"long label", serviceCheckInput{Namespace: "apps", Host: strings.Repeat("a", 64)}, "invalid host"},
		{"long name", serviceCheckInput{Namespace: "apps", Host: strings.Repeat(strings.Repeat("a", 62)+".", 4) + "db"}, "253"},
		{"missing namespace", serviceCheckInput{Host: "db"}, "invalid name"},
		{"bad pod", serviceCheckInput{Namespace: "apps", Host: "db", Pod: "../db"}, "invalid name"},
		{"bad port", serviceCheckInput{Namespace: "apps", Host: "db", Port: "65536"}, "port"},
		{"negative port", serviceCheckInput{Namespace: "apps", Host: "db", Port: "-1"}, "port"},
		{"bad port name", serviceCheckInput{Namespace: "apps", Host: "db", Port: "bad_port"}, "port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset()
			p := &provider{client: client, policy: namespacePolicy{allow: []string{"apps"}}}
			_, err := p.serviceCheck(context.Background(), tool.Caller{}, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(client.Actions()) > 0 {
				t.Fatal(err, client.Actions())
			}
		})
	}
}

func TestServiceCheckAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		name, verb, resource string
		apiErr               error
		want                 string
		fails                bool
	}{
		{"EndpointSlice forbidden", "list", "endpointslices", apierrors.NewForbidden(schema.GroupResource{Group: "discovery.k8s.io", Resource: "endpointslices"}, "db", errors.New("denied")), "EndpointSlices not readable; grant discovery.k8s.io endpointslices get/list to see endpoints", false},
		{"EndpointSlice failure", "list", "endpointslices", errors.New("slice failure"), "slice failure", true},
		{"Service failure", "get", "services", errors.New("service failure"), "service failure", true},
		{"Pod failure", "list", "pods", errors.New("pod failure"), "pod failure", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, pod, endpoints := serviceCheckFixture()
			client := fake.NewClientset(svc, pod, endpoints)
			client.PrependReactor(tc.verb, tc.resource, func(kt.Action) (bool, runtime.Object, error) { return true, nil, tc.apiErr })
			p := &provider{client: client, policy: namespacePolicy{allow: []string{"apps"}}}
			out, err := p.serviceCheck(context.Background(), tool.Caller{}, serviceCheckInput{Namespace: "apps", Host: "db"})
			if tc.fails {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatal(out, err)
				}
			} else if err != nil || !strings.Contains(out, tc.want) || !strings.Contains(out, "1 matched pods, 1 ready") {
				t.Fatal(out, err)
			}
			assertServiceCheckActions(t, client, p.policy)
		})
	}
}

func TestServiceCheckPodBound(t *testing.T) {
	svc, pod, endpoints := serviceCheckFixture()
	objects := []runtime.Object{svc, endpoints}
	for i := 0; i < 501; i++ {
		p := pod.DeepCopy()
		p.Name = fmt.Sprintf("db-%03d", i)
		p.Status.Conditions[0].Status = corev1.ConditionFalse
		objects = append(objects, p)
	}
	client := fake.NewClientset(objects...)
	p := &provider{client: client, policy: namespacePolicy{allow: []string{"apps"}}}
	out, err := p.serviceCheck(context.Background(), tool.Caller{}, serviceCheckInput{Namespace: "apps", Host: "db"})
	if err != nil || !strings.Contains(out, "truncated to 500 examined") || !strings.Contains(out, "500 matched pods") || strings.Count(out, "not ready:") != 10 {
		t.Fatal(out, err)
	}
	for _, action := range client.Actions() {
		if a, ok := action.(kt.ListActionImpl); ok && action.GetResource().Resource == "pods" && a.GetListOptions().Limit != 500 {
			t.Fatal(action)
		}
	}
	assertServiceCheckActions(t, client, p.policy)
}

func TestServiceCheckRegistration(t *testing.T) {
	for _, cluster := range []string{"kind", "prod", "", "unknown"} {
		t.Run(cluster, func(t *testing.T) {
			cfg, clients := clusterFixture()
			cs := clusterSession(t, cfg, clients, nil)
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "k8s_service_check", Arguments: map[string]any{"cluster": cluster, "namespace": "apps", "host": "db"}})
			if err != nil {
				t.Fatal(err)
			}
			out := result.Content[0].(*mcp.TextContent).Text
			valid := cluster == "kind" || cluster == "prod"
			if result.IsError == valid {
				t.Fatal(out)
			}
			if valid && !strings.HasPrefix(out, "cluster: "+cluster+"\nFINDINGS\n") {
				t.Fatal(out)
			}
			for name, client := range clients {
				c := client.(*fake.Clientset)
				if (len(c.Actions()) > 0) != (valid && name == cluster) {
					t.Fatal(name, c.Actions())
				}
				assertServiceCheckActions(t, c, namespacePolicy{allow: []string{"apps"}})
			}
		})
	}
}

func TestServiceCheckResolverOrder(t *testing.T) {
	for _, tc := range []struct {
		name, host string
		ndots      string
		searches   []string
		policy     corev1.DNSPolicy
		want       []string
	}{
		{name: "short search", host: "db", want: []string{"db.apps.svc.cluster.local", "db.svc.cluster.local", "db.cluster.local", "db"}},
		{name: "namespace search", host: "db.prod", want: []string{"db.prod.apps.svc.cluster.local", "db.prod.svc.cluster.local", "db.prod.cluster.local", "db.prod"}},
		{name: "FQDN search first", host: "db.apps.svc.cluster.local", want: []string{"db.apps.svc.cluster.local.apps.svc.cluster.local", "db.apps.svc.cluster.local.svc.cluster.local", "db.apps.svc.cluster.local.cluster.local", "db.apps.svc.cluster.local"}},
		{name: "absolute first", host: "db.apps.svc.cluster.local", ndots: "1", want: []string{"db.apps.svc.cluster.local", "db.apps.svc.cluster.local.apps.svc.cluster.local", "db.apps.svc.cluster.local.svc.cluster.local", "db.apps.svc.cluster.local.cluster.local"}},
		{name: "extra searches", host: "db", searches: []string{"example.com"}, want: []string{"db.apps.svc.cluster.local", "db.svc.cluster.local", "db.cluster.local", "db.example.com", "db"}},
		{name: "None with custom search", host: "db", searches: []string{"apps.svc.example.internal"}, policy: corev1.DNSNone, want: []string{"db.apps.svc.example.internal", "db"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, pod, _ := serviceCheckFixture()
			pod.Spec.DNSPolicy = tc.policy
			pod.Spec.DNSConfig = &corev1.PodDNSConfig{Searches: tc.searches}
			if tc.ndots != "" {
				pod.Spec.DNSConfig.Options = []corev1.PodDNSConfigOption{{Name: "ndots", Value: &tc.ndots}}
			}
			client := fake.NewClientset(pod)
			p := &provider{client: client, policy: namespacePolicy{allow: []string{"apps"}}}
			got, err := p.serviceDNS(context.Background(), serviceCheckInput{Namespace: "apps", Host: tc.host, Pod: pod.Name}, &serviceCheckReport{})
			if err != nil || !slices.Equal(got.names, tc.want) {
				t.Fatal(got, err)
			}
			assertServiceCheckActions(t, client, p.policy)
		})
	}
}

func TestServiceCheckEndpointPagination(t *testing.T) {
	svc, pod, endpoints := serviceCheckFixture()
	client := fake.NewClientset(svc, pod)
	calls := 0
	client.PrependReactor("list", "endpointslices", func(action kt.Action) (bool, runtime.Object, error) {
		options := action.(kt.ListActionImpl).GetListOptions()
		if options.LabelSelector != "kubernetes.io/service-name=db" {
			t.Fatal(options)
		}
		calls++
		result := &discoveryv1.EndpointSliceList{Items: []discoveryv1.EndpointSlice{*endpoints.DeepCopy()}}
		if calls == 1 {
			result.Continue = "page-2"
		} else if options.Continue != "page-2" {
			t.Fatal(options)
		}
		return true, result, nil
	})
	p := &provider{client: client, policy: namespacePolicy{allow: []string{"apps"}}}
	out, err := p.serviceCheck(context.Background(), tool.Caller{}, serviceCheckInput{Namespace: "apps", Host: "db"})
	if err != nil || calls != 2 || !strings.Contains(out, "2/2 endpoints ready") {
		t.Fatal(out, err, calls)
	}
	assertServiceCheckActions(t, client, p.policy)
}

func TestServiceCheckSuggestionBound(t *testing.T) {
	var objects []runtime.Object
	var allowed []string
	for i := 0; i < 12; i++ {
		svc, _, _ := serviceCheckFixture()
		svc.Namespace = fmt.Sprintf("ns-%02d", i)
		objects = append(objects, svc)
		allowed = append(allowed, svc.Namespace)
	}
	client := fake.NewClientset(objects...)
	p := &provider{client: client, policy: namespacePolicy{allow: append(allowed, "apps")}}
	out, err := p.serviceCheck(context.Background(), tool.Caller{}, serviceCheckInput{Namespace: "apps", Host: "db"})
	if err != nil || strings.Count(out, "try db.") != 10 {
		t.Fatal(out, err)
	}
	assertServiceCheckActions(t, client, p.policy)
}
