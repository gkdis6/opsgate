package kube

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/gkdis6/opsgate/internal/tool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

type namespacePolicy struct{ allow, deny []string }

func (p namespacePolicy) validate() error {
	for _, patterns := range [][]string{p.allow, p.deny} {
		for _, pattern := range patterns {
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("invalid namespace pattern %q: %w", pattern, err)
			}
		}
	}
	return nil
}

func (p namespacePolicy) allowed(ns string) bool {
	matches := func(patterns []string) bool {
		for _, pattern := range patterns {
			if ok, _ := path.Match(pattern, ns); ok {
				return true
			}
		}
		return false
	}
	return matches(p.allow) && !matches(p.deny)
}

func validName(name string, namespace bool) error {
	problems := validation.IsDNS1123Subdomain(name)
	if namespace {
		problems = validation.IsDNS1123Label(name)
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid name %q: %s", name, strings.Join(problems, ", "))
	}
	return nil
}

func (p *provider) checkNamespace(ns string) error {
	if err := validName(ns, true); err != nil {
		return err
	}
	if !p.policy.allowed(ns) {
		return tool.Denied("namespace is not allowed")
	}
	return nil
}

func (p *provider) namespaces(ctx context.Context, ns string) ([]string, error) {
	if ns != "" {
		if err := p.checkNamespace(ns); err != nil {
			return nil, err
		}
		return []string{ns}, nil
	}
	if slices.Contains(p.policy.allow, "*") {
		return []string{""}, nil
	}
	glob := false
	for _, pattern := range p.policy.allow {
		if strings.ContainsAny(pattern, "*?[") {
			glob = true
		}
	}
	var result []string
	if glob {
		list, err := p.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			if p.policy.allowed(item.Name) {
				result = append(result, item.Name)
			}
		}
	} else {
		for _, name := range p.policy.allow {
			if err := validName(name, true); err != nil {
				return nil, err
			}
			if p.policy.allowed(name) && !slices.Contains(result, name) {
				result = append(result, name)
			}
		}
	}
	slices.Sort(result)
	return result, nil
}
