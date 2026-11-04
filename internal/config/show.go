package config

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Effective renders every setting, defaults included, as YAML that Load
// accepts. A mode hides values the file does not spell out; this shows them.
func (c Config) Effective() ([]byte, error) {
	if c.Watch.Email.Host == "" {
		c.Watch.Email = WatchEmail{}
	}
	n, err := node(reflect.ValueOf(c))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	return b.Bytes(), enc.Close()
}

var durationType = reflect.TypeOf(time.Duration(0))

// node converts by YAML tags. yaml.v3 would encode durations as integer
// nanoseconds, which Load reads back but people cannot.
func node(v reflect.Value) (*yaml.Node, error) {
	switch {
	case v.Type() == durationType:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: time.Duration(v.Int()).String()}, nil
	case v.Kind() == reflect.Struct:
		m := &yaml.Node{Kind: yaml.MappingNode}
		if err := fields(m, v); err != nil {
			return nil, err
		}
		return m, nil
	case v.Kind() == reflect.Slice:
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for i := 0; i < v.Len(); i++ {
			item, err := node(v.Index(i))
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, item)
		}
		if len(seq.Content) == 0 {
			seq.Style = yaml.FlowStyle
		}
		return seq, nil
	case v.Kind() == reflect.Map:
		m := &yaml.Node{Kind: yaml.MappingNode}
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, k := range keys {
			value, err := node(v.MapIndex(k))
			if err != nil {
				return nil, err
			}
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k.String()}, value)
		}
		if len(m.Content) == 0 {
			m.Style = yaml.FlowStyle
		}
		return m, nil
	}
	var n yaml.Node
	if err := n.Encode(v.Interface()); err != nil {
		return nil, fmt.Errorf("encode %s: %w", v.Type(), err)
	}
	return &n, nil
}

func fields(m *yaml.Node, v reflect.Value) error {
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if opts == "inline" {
			// The single-cluster form and clusters are mutually exclusive.
			if k, ok := v.Interface().(Kubernetes); ok && k.Clusters != nil {
				continue
			}
			if err := fields(m, v.Field(i)); err != nil {
				return err
			}
			continue
		}
		// Kubernetes clusters: absent and empty mean different things.
		if opts == "omitempty" && v.Field(i).IsZero() {
			continue
		}
		value, err := node(v.Field(i))
		if err != nil {
			return err
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, value)
	}
	return nil
}
