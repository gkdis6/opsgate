package kube

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
)

type serviceCheckInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace string `json:"namespace" jsonschema:"Required allowed namespace where the client looks up the host"`
	Host      string `json:"host" jsonschema:"Required lowercase DNS name; at most 253 bytes; IP addresses are not checked"`
	Port      string `json:"port,omitempty" jsonschema:"Optional Service port number or name"`
	Pod       string `json:"pod,omitempty" jsonschema:"Optional pod in namespace whose DNS settings are used"`
}

type serviceCheckReport struct {
	findings, dns, service, backends strings.Builder
}

func (r *serviceCheckReport) finding(format string, args ...any) {
	fmt.Fprintln(&r.findings, redact.String(fmt.Sprintf(format, args...)))
}

func (r *serviceCheckReport) text() string {
	return redact.String("FINDINGS\n" + r.findings.String() +
		"INFO: Configuration check only; sends no DNS queries or connections. NetworkPolicies are not evaluated.\n\nDETAILS\nDNS\n" + r.dns.String() +
		"\nSERVICE\n" + r.service.String() + "\nBACKENDS\n" + r.backends.String())
}

func (in serviceCheckInput) validate() error {
	if net.ParseIP(in.Host) != nil {
		return fmt.Errorf("IP addresses are not checked; provide a Service DNS name")
	}
	if len(in.Host) == 0 || len(in.Host) > 253 {
		return fmt.Errorf("host must be a lowercase DNS name of 1–253 bytes")
	}
	for _, label := range strings.Split(in.Host, ".") {
		if len(validation.IsDNS1123Label(label)) > 0 {
			return fmt.Errorf("invalid host: each label must be DNS-1123")
		}
	}
	if in.Pod != "" {
		if err := validName(in.Pod, false); err != nil {
			return err
		}
	}
	if in.Port != "" {
		n, err := strconv.Atoi(in.Port)
		if err == nil {
			if n < 1 || n > 65535 || strconv.Itoa(n) != in.Port {
				return fmt.Errorf("port must be 1–65535 or a port name")
			}
		} else if len(validation.IsValidPortName(in.Port)) > 0 {
			return fmt.Errorf("port must be 1–65535 or a port name")
		}
	}
	return nil
}

func (p *provider) serviceCheck(ctx context.Context, _ tool.Caller, in serviceCheckInput) (string, error) {
	if err := in.validate(); err != nil {
		return "", err
	}
	if err := p.checkNamespace(in.Namespace); err != nil {
		return "", err
	}
	r := &serviceCheckReport{}
	candidates, err := p.serviceDNS(ctx, in, r)
	if err != nil {
		return "", err
	}
	var found *corev1.Service
	var selected serviceDNSName
	var missing []serviceDNSName
	recognized, blocked := false, false
	// Cache repeated interpretations without changing resolver order.
	cache := map[string]*corev1.Service{}
	for _, fqdn := range candidates.names {
		if found != nil {
			fmt.Fprintf(&r.dns, "%s -> not tried after Service match\n", redact.String(fqdn))
			continue
		}
		name, ok := interpretServiceDNS(fqdn, in.Host, candidates.domains)
		if !ok {
			fmt.Fprintf(&r.dns, "%s -> not a Service name\n", redact.String(fqdn))
			continue
		}
		if !p.policy.allowed(name.namespace) {
			blocked = true
			r.finding("INFO: namespace %s not allowed", name.namespace)
			fmt.Fprintf(&r.dns, "%s -> namespace %s not allowed\n", redact.String(fqdn), redact.String(name.namespace))
			continue
		}
		if err := p.checkNamespace(name.namespace); err != nil {
			return "", err
		}
		key := name.namespace + "/" + name.service
		svc, seen := cache[key]
		if !seen {
			svc, err = p.client.CoreV1().Services(name.namespace).Get(ctx, name.service, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				svc, err = nil, nil
			}
			if err != nil {
				return "", err
			}
			cache[key] = svc
		}
		if name.hostname != "" && (svc == nil || svc.Spec.ClusterIP != corev1.ClusterIPNone) {
			fmt.Fprintf(&r.dns, "%s -> no matching headless Service\n", redact.String(fqdn))
			continue
		}
		recognized = true
		if svc == nil {
			missing = append(missing, name)
			fmt.Fprintf(&r.dns, "%s -> Service %s not found\n", redact.String(fqdn), redact.String(key))
			continue
		}
		found, selected = svc, name
		selected.fqdn = fqdn
		fmt.Fprintf(&r.dns, "%s -> Service %s\n", redact.String(fqdn), redact.String(key))
	}
	if found == nil {
		switch {
		case recognized:
			r.finding("PROBLEM: no matching Service found for %s", in.Host)
			if err := p.serviceSuggestions(ctx, missing, r); err != nil {
				return "", err
			}
		case !blocked:
			r.finding("INFO: not a cluster Service name; resolved by upstream DNS; opsgate does not resolve it")
		default:
			r.finding("INFO: Service lookup incomplete because candidate namespaces are not allowed")
		}
		fmt.Fprintln(&r.service, "No matching Service inspected.")
		fmt.Fprintln(&r.backends, "No matching Service inspected.")
		return r.text(), nil
	}
	if err := p.serviceDetails(ctx, in, found, selected, r); err != nil {
		return "", err
	}
	return r.text(), nil
}

type serviceDNSCandidates struct{ names, domains []string }

func (p *provider) serviceDNS(ctx context.Context, in serviceCheckInput, r *serviceCheckReport) (serviceDNSCandidates, error) {
	searches := []string{in.Namespace + ".svc.cluster.local", "svc.cluster.local", "cluster.local"}
	ndots, policy, hostNetwork := 5, corev1.DNSClusterFirst, false
	var dns *corev1.PodDNSConfig
	if in.Pod != "" {
		pod, err := p.client.CoreV1().Pods(in.Namespace).Get(ctx, in.Pod, metav1.GetOptions{})
		if err != nil {
			return serviceDNSCandidates{}, err
		}
		policy, hostNetwork, dns = pod.Spec.DNSPolicy, pod.Spec.HostNetwork, pod.Spec.DNSConfig
		if policy == "" {
			policy = corev1.DNSClusterFirst
		}
	}
	clusterDNS := policy == corev1.DNSClusterFirstWithHostNet || policy == corev1.DNSClusterFirst && !hostNetwork
	if !clusterDNS {
		searches = nil
	}
	fmt.Fprintln(&r.dns, "Assumed cluster domain: cluster.local; explicit fully qualified Service names may use any domain.")
	fmt.Fprintf(&r.dns, "dnsPolicy: %s; hostNetwork: %t\n", redact.String(string(policy)), hostNetwork)
	if dns == nil {
		fmt.Fprintln(&r.dns, "dnsConfig: not set")
	}
	if dns != nil {
		for _, search := range dns.Searches {
			if !slices.Contains(searches, search) {
				searches = append(searches, search)
			}
		}
		fmt.Fprintf(&r.dns, "dnsConfig nameservers: %s\ndnsConfig searches: %s\n", redact.String(strings.Join(dns.Nameservers, ", ")), redact.String(strings.Join(dns.Searches, ", ")))
		for _, option := range dns.Options {
			value := ""
			if option.Value != nil {
				value = *option.Value
			}
			fmt.Fprintf(&r.dns, "dnsConfig option: %s=%s\n", redact.String(option.Name), redact.String(value))
			if option.Name == "ndots" {
				if n, err := strconv.Atoi(value); err == nil && n >= 0 && n <= 15 {
					ndots = n
				}
			}
		}
	}
	hasClusterSearch := false
	for _, search := range searches {
		hasClusterSearch = hasClusterSearch || strings.Contains(search, ".svc.") || strings.HasPrefix(search, "svc.")
	}
	if (policy == corev1.DNSDefault || policy == corev1.DNSNone) && !hasClusterSearch {
		r.finding("PROBLEM: dnsPolicy %s without cluster search domains: cluster Service names do not resolve via cluster DNS", policy)
	}
	if hostNetwork && policy == corev1.DNSClusterFirst {
		r.finding("PROBLEM: hostNetwork with ClusterFirst uses node DNS; use ClusterFirstWithHostNet for cluster DNS")
	}
	if !clusterDNS {
		fmt.Fprintln(&r.dns, "Node resolver settings are not inspected; cluster DNS availability cannot be verified from this policy. Fully qualified names below are configuration candidates only.")
	}
	fmt.Fprintf(&r.dns, "search path: %s; ndots: %d\nResolver candidates (stop at the first successful answer; preceding queries are assumed unsuccessful):\n", redact.String(strings.Join(searches, ", ")), ndots)
	candidates := serviceDNSCandidates{domains: []string{"cluster.local"}}
	for _, search := range searches {
		parts := strings.SplitN(strings.TrimPrefix(search, "svc."), ".svc.", 2)
		if strings.HasPrefix(search, "svc.") {
			candidates.domains = append(candidates.domains, strings.TrimPrefix(search, "svc."))
		} else if len(parts) == 2 {
			candidates.domains = append(candidates.domains, parts[1])
		}
	}
	if !clusterDNS && !hasClusterSearch {
		for _, candidate := range serviceSearchNames(in.Host, searches, ndots) {
			fmt.Fprintln(&r.dns, redact.String(candidate))
		}
		fmt.Fprintln(&r.dns, "ClusterFirst reference candidates for Service inspection below; these are not the active resolver search path:")
		searches = []string{in.Namespace + ".svc.cluster.local", "svc.cluster.local", "cluster.local"}
	}
	candidates.names = serviceSearchNames(in.Host, searches, ndots)
	return candidates, nil
}

func serviceSearchNames(host string, searches []string, ndots int) []string {
	var candidates []string
	add := func(name string) {
		if !slices.Contains(candidates, name) {
			candidates = append(candidates, name)
		}
	}
	if strings.Count(host, ".") >= ndots {
		add(host)
	}
	for _, search := range searches {
		add(host + "." + strings.TrimSuffix(search, "."))
	}
	add(host)
	return candidates
}

type serviceDNSName struct{ service, namespace, hostname, fqdn string }

func interpretServiceDNS(candidate, host string, domains []string) (serviceDNSName, bool) {
	parts := strings.Split(candidate, ".")
	// Recognize search expansions only for known domains, so an appended search
	// suffix cannot turn an external hostname into a fully qualified Service.
	expanded := false
	for _, domain := range domains {
		suffix := ".svc." + domain
		if strings.HasSuffix(candidate, suffix) {
			parts = strings.Split(strings.TrimSuffix(candidate, suffix), ".")
			expanded = true
			break
		}
	}
	if !expanded {
		if candidate != host {
			return serviceDNSName{}, false
		}
		switch {
		case len(parts) >= 3 && parts[2] == "svc":
			parts = parts[:2]
		case len(parts) >= 4 && parts[3] == "svc":
			parts = parts[:3]
		default:
			return serviceDNSName{}, false
		}
	}

	switch len(parts) {
	case 2:
		return serviceDNSName{service: parts[0], namespace: parts[1]}, true
	case 3:
		return serviceDNSName{hostname: parts[0], service: parts[1], namespace: parts[2]}, true
	}
	// An explicit domain may itself end in a known Service suffix.
	if candidate == host {
		original := strings.Split(host, ".")
		if len(original) >= 4 && original[2] == "svc" {
			return serviceDNSName{service: original[0], namespace: original[1]}, true
		}
		if len(original) >= 5 && original[3] == "svc" {
			return serviceDNSName{hostname: original[0], service: original[1], namespace: original[2]}, true
		}
	}
	return serviceDNSName{}, false
}

func (p *provider) serviceSuggestions(ctx context.Context, missing []serviceDNSName, r *serviceCheckReport) error {
	namespaces, err := p.namespaces(ctx, "")
	if err != nil {
		return err
	}
	if slices.Contains(namespaces, "") {
		list, err := p.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		namespaces = nil
		for _, ns := range list.Items {
			if p.policy.allowed(ns.Name) {
				namespaces = append(namespaces, ns.Name)
			}
		}
	}
	slices.Sort(namespaces)
	seen, hits := map[string]bool{}, 0
	for _, name := range missing {
		if seen[name.service] {
			continue
		}
		seen[name.service] = true
		for _, ns := range namespaces {
			if ns == name.namespace || !p.policy.allowed(ns) {
				continue
			}
			if err := p.checkNamespace(ns); err != nil {
				return err
			}
			list, err := p.client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + name.service, Limit: 10})
			if err != nil {
				return err
			}
			for _, svc := range list.Items {
				if svc.Name != name.service {
					continue
				}
				r.finding("INFO: Service exists in another allowed namespace; try %s.%s", svc.Name, ns)
				hits++
				if hits == 10 {
					return nil
				}
			}
		}
	}
	return nil
}

func (p *provider) serviceDetails(ctx context.Context, in serviceCheckInput, svc *corev1.Service, name serviceDNSName, r *serviceCheckReport) error {
	typ := svc.Spec.Type
	if typ == "" {
		typ = corev1.ServiceTypeClusterIP
	}
	r.finding("INFO: %s -> Service %s/%s (%s %s)", name.fqdn, name.namespace, name.service, typ, svc.Spec.ClusterIP)
	fmt.Fprintf(&r.service, "type: %s; clusterIP: %s\nselector: %s\n", redact.String(string(typ)), redact.String(svc.Spec.ClusterIP), redact.String(labels.SelectorFromSet(svc.Spec.Selector).String()))
	var ports []corev1.ServicePort
	for _, port := range svc.Spec.Ports {
		fmt.Fprintf(&r.service, "port: name=%s port=%d targetPort=%s protocol=%s\n", redact.String(port.Name), port.Port, redact.String(port.TargetPort.String()), redact.String(string(cmp.Or(port.Protocol, corev1.ProtocolTCP))))
		if in.Port == "" || in.Port == port.Name || in.Port == strconv.Itoa(int(port.Port)) {
			ports = append(ports, port)
		}
	}
	if in.Port != "" && len(ports) == 0 {
		r.finding("PROBLEM: Service has no port matching %s", in.Port)
	}
	if typ == corev1.ServiceTypeExternalName {
		r.finding("INFO: ExternalName: DNS returns a CNAME to %s; opsgate does not follow it", svc.Spec.ExternalName)
		fmt.Fprintf(&r.service, "ExternalName: %s\n", redact.String(svc.Spec.ExternalName))
		fmt.Fprintln(&r.backends, "ExternalName has no Service backends to inspect.")
		return nil
	}
	if svc.Spec.ClusterIP == corev1.ClusterIPNone {
		r.finding("INFO: headless Service: DNS returns ready pod IPs; publishNotReadyAddresses=%t", svc.Spec.PublishNotReadyAddresses)
	}
	if svc.Spec.PublishNotReadyAddresses {
		r.finding("INFO: publishNotReadyAddresses=true: endpoints can be published even when pods are not ready")
	}
	if len(svc.Spec.Selector) == 0 {
		fmt.Fprintln(&r.backends, "Empty selector: endpoints are managed manually.")
	} else {
		if err := p.servicePods(ctx, name, svc, ports, r); err != nil {
			return err
		}
	}
	if len(svc.Spec.Ports) == 0 && in.Port == "" {
		ports = []corev1.ServicePort{{}}
	}
	return p.serviceEndpoints(ctx, name, ports, r)
}

func (p *provider) servicePods(ctx context.Context, name serviceDNSName, svc *corev1.Service, ports []corev1.ServicePort, r *serviceCheckReport) error {
	selector := labels.SelectorFromSet(svc.Spec.Selector).String()
	list, err := p.client.CoreV1().Pods(name.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 500})
	if err != nil {
		return err
	}
	truncated := list.Continue != "" || len(list.Items) > 500
	pods := list.Items[:min(len(list.Items), 500)]
	ready, shown := 0, 0
	for _, pod := range pods {
		isReady, reason := false, pod.Status.Reason
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady {
				isReady = condition.Status == corev1.ConditionTrue
				if reason == "" {
					reason = condition.Reason
				}
			}
		}
		if isReady {
			ready++
			continue
		}
		if shown < 10 {
			for _, status := range append(slices.Clone(pod.Status.InitContainerStatuses), pod.Status.ContainerStatuses...) {
				if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
					reason += "; " + status.Name + ": " + status.State.Waiting.Reason
				}
			}
			fmt.Fprintf(&r.backends, "not ready: %s phase=%s reason=%s\n", redact.String(pod.Name), redact.String(string(pod.Status.Phase)), redact.String(reason))
			shown++
		}
	}
	fmt.Fprintf(&r.backends, "Selector %s: %d matched pods, %d ready\n", redact.String(selector), len(pods), ready)
	if truncated {
		r.finding("INFO: selector pods truncated to 500 examined; counts and targetPort checks may be incomplete")
	}
	if len(pods) == 0 {
		r.finding("PROBLEM: selector %s matches no pods in %s", selector, name.namespace)
	} else if ready == 0 {
		r.finding("PROBLEM: selector matches %d pods but none ready in %s", len(pods), name.namespace)
	}
	for _, port := range ports {
		target := port.TargetPort
		if target == intstr.FromInt32(0) {
			target = intstr.FromInt32(port.Port)
		}
		declared := 0
		for _, pod := range pods {
			containers := slices.Clone(pod.Spec.Containers)
			for _, c := range pod.Spec.InitContainers {
				if c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways {
					containers = append(containers, c)
				}
			}
			found := false
			for _, c := range containers {
				for _, cp := range c.Ports {
					protocol := cp.Protocol
					if protocol == "" {
						protocol = corev1.ProtocolTCP
					}
					serviceProtocol := port.Protocol
					if serviceProtocol == "" {
						serviceProtocol = corev1.ProtocolTCP
					}
					if protocol == serviceProtocol && (target.Type == intstr.String && cp.Name == target.StrVal || target.Type == intstr.Int && cp.ContainerPort == target.IntVal) {
						found = true
					}
				}
			}
			if found {
				declared++
			}
		}
		if target.Type == intstr.String && declared < len(pods) {
			r.finding("PROBLEM: named targetPort %s missing on %d/%d matched pods", target.StrVal, len(pods)-declared, len(pods))
		}
		if target.Type == intstr.Int && declared == 0 && len(pods) > 0 {
			r.finding("INFO: no matched pod declares numeric targetPort %d; undeclared ports still work", target.IntVal)
		}
	}
	return nil
}

func (p *provider) serviceEndpoints(ctx context.Context, name serviceDNSName, ports []corev1.ServicePort, r *serviceCheckReport) error {
	type counts struct{ total, ready, serving, terminating, defaultServing int }
	totals := make([]counts, len(ports))
	options := metav1.ListOptions{LabelSelector: labels.Set{"kubernetes.io/service-name": name.service}.String(), Limit: 500}
	for {
		list, err := p.client.DiscoveryV1().EndpointSlices(name.namespace).List(ctx, options)
		if apierrors.IsForbidden(err) {
			r.finding("INFO: EndpointSlices not readable; grant discovery.k8s.io endpointslices get/list to see endpoints")
			return nil
		}
		if err != nil {
			return err
		}
		for _, slice := range list.Items {
			for i, port := range ports {
				matches := port.Port == 0
				for _, epPort := range slice.Ports {
					portName := ""
					if epPort.Name != nil {
						portName = *epPort.Name
					}
					protocol := corev1.ProtocolTCP
					if epPort.Protocol != nil {
						protocol = *epPort.Protocol
					}
					serviceProtocol := port.Protocol
					if serviceProtocol == "" {
						serviceProtocol = corev1.ProtocolTCP
					}
					if portName == port.Name && protocol == serviceProtocol && epPort.Port != nil {
						matches = true
						fmt.Fprintf(&r.backends, "EndpointSlice port: name=%s port=%d protocol=%s\n", redact.String(portName), *epPort.Port, redact.String(string(protocol)))
					}
				}
				if !matches {
					continue
				}
				for _, ep := range slice.Endpoints {
					if name.hostname != "" && (ep.Hostname == nil || *ep.Hostname != name.hostname) {
						continue
					}
					c := &totals[i]
					c.total++
					if ep.Conditions.Ready == nil || *ep.Conditions.Ready {
						c.ready++
					}
					if ep.Conditions.Serving == nil {
						c.defaultServing++
						c.serving++
					} else if *ep.Conditions.Serving {
						c.serving++
					}
					if ep.Conditions.Terminating != nil && *ep.Conditions.Terminating {
						c.terminating++
					}
				}
			}
		}
		if list.Continue == "" {
			break
		}
		options.Continue = list.Continue
	}
	for i, c := range totals {
		port := ports[i]
		fmt.Fprintf(&r.backends, "EndpointSlices port %s/%d: %d/%d ready, %d serving (%d defaulted), %d terminating\n", redact.String(port.Name), port.Port, c.ready, c.total, c.serving, c.defaultServing, c.terminating)
		level := "OK"
		if c.ready == 0 {
			level = "PROBLEM"
		}
		if port.Port == 0 {
			r.finding("%s: %s -> Service %s/%s, %d/%d endpoints ready (Service declares no ports)", level, name.fqdn, name.namespace, name.service, c.ready, c.total)
		} else {
			r.finding("%s: %s -> Service %s/%s, %d/%d endpoints ready on port %d", level, name.fqdn, name.namespace, name.service, c.ready, c.total, port.Port)
		}
	}
	if name.hostname != "" {
		fmt.Fprintf(&r.backends, "Endpoint counts restricted to hostname %s.\n", redact.String(name.hostname))
	}
	return nil
}
