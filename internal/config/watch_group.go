package config

import (
	"fmt"
	"path"
	"time"
)

// WatchGroup routes the first matching namespace to its selected outputs.
type WatchGroup struct {
	Name         string        `yaml:"name"`
	Namespaces   []string      `yaml:"namespaces"`
	Outputs      []string      `yaml:"outputs"`
	ProblemsOnly bool          `yaml:"problems_only"`
	Batch        time.Duration `yaml:"batch"`
}

func (w Watch) validateGroups() error {
	if w.Groups == nil {
		return nil
	}
	if len(w.Groups) < 1 || len(w.Groups) > 50 {
		return fmt.Errorf("watch.groups must have 1 to 50 groups")
	}
	outputs := map[string]bool{}
	for _, o := range w.Outputs {
		if o.Name == "email" || o.Name == "inbox" {
			return fmt.Errorf("watch.groups reserves output names email and inbox")
		}
		outputs[o.Name] = true
	}
	outputs["email"] = w.Email.Host != ""
	outputs["inbox"] = w.Inbox.URL != ""
	names := map[string]bool{}
	for i, g := range w.Groups {
		prefix := fmt.Sprintf("watch.groups[%d]", i)
		if !watchName(g.Name) || names[g.Name] {
			return fmt.Errorf("%s: name must be unique, with 1 to 64 ASCII letters, digits, '_' or '-'", prefix)
		}
		names[g.Name] = true
		if len(g.Namespaces) < 1 || len(g.Namespaces) > 100 {
			return fmt.Errorf("%s.namespaces must have 1 to 100 patterns", prefix)
		}
		for _, pattern := range g.Namespaces {
			if _, err := path.Match(pattern, ""); err != nil || len(pattern) > 253 {
				return fmt.Errorf("%s.namespaces: invalid glob", prefix)
			}
		}
		if len(g.Outputs) < 1 || len(g.Outputs) > 22 {
			return fmt.Errorf("%s.outputs must have 1 to 22 names", prefix)
		}
		seen := map[string]bool{}
		for _, name := range g.Outputs {
			if !outputs[name] || seen[name] {
				return fmt.Errorf("%s.outputs: unknown, disabled or duplicate output %q", prefix, name)
			}
			seen[name] = true
			low, high := time.Second, 10*time.Minute
			if name == "email" {
				low, high = time.Minute, time.Hour
			}
			if g.Batch < low || g.Batch > high {
				return fmt.Errorf("%s.batch for %s must be between %s and %s", prefix, name, low, high)
			}
		}
	}
	return nil
}

func watchName(name string) bool {
	if len(name) < 1 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
