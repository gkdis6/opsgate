// Package action defines the server's link action catalog and URL format.
// Registration and live target policy remain the tool provider's responsibility.
package action

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/gkdis6/opsgate/internal/redact"
)

type Entry struct {
	Tool            string
	Arguments       []string
	English, Korean string
	Reversible      bool
}

var catalog = []Entry{
	{"k8s_rollout_restart", []string{"cluster", "kind", "namespace", "name"}, "Restart workload", "워크로드 재시작", false},
	{"k8s_restore_image", []string{"cluster", "namespace", "name", "container", "expected_image", "image", "revert"}, "Restore image", "이미지 복구", true},
	{"k8s_scale", []string{"cluster", "kind", "namespace", "name", "replicas"}, "Scale workload", "워크로드 규모 조정", true},
	{"k8s_delete_pod", []string{"cluster", "namespace", "name"}, "Delete controller-owned pod", "컨트롤러 소유 파드 삭제", false},
	{"host_restart_unit", []string{"unit"}, "Restart systemd unit", "systemd 유닛 재시작", false},
	{"host_restart_container", []string{"container"}, "Restart container", "컨테이너 재시작", false},
}

func Lookup(name string) (Entry, bool) {
	for _, e := range catalog {
		if e.Tool == name {
			e.Arguments = append([]string(nil), e.Arguments...)
			return e, true
		}
	}
	return Entry{}, false
}

func (e Entry) Label(language string) string {
	if language == "ko" {
		return e.Korean
	}
	return e.English
}

func Source(value, fallback string) (string, error) {
	if value == "" {
		value = fallback
	}
	if value != "rule" && value != "ai" {
		return "", fmt.Errorf("source must be rule or ai")
	}
	return value, nil
}

// Unsigned is the source of a link this server did not issue, or whose
// signature no longer verifies. Its claimed source is not shown as fact.
const Unsigned = "link"

var signingKey []byte

// SetKey makes URL sign the links it builds and Parse distrust the source of
// any link without a valid signature. Call it once before serving. Without a
// key (stdio and tests) sources are taken as written.
func SetKey(key []byte) { signingKey = append([]byte(nil), key...) }

// signature binds the tool, every argument and the source, so a signed link
// cannot be edited into another action or relabelled.
func signature(name string, args map[string]any, source string) string {
	q := url.Values{}
	for k, v := range args {
		q.Set(k, fmt.Sprint(v))
	}
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(name + "\x00" + source + "\x00" + q.Encode()))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// optional arguments may be omitted from a link: the cluster defaults on the
// server, and revert selects a narrower restore mode only when present.
func optional(key string) bool { return key == "cluster" || key == "revert" }

// Validate rejects all fields outside the catalog, wrong scalar types and
// values that redaction would mask. Provider Plan validates live targets.
func Validate(name string, args map[string]any) error {
	e, ok := Lookup(name)
	if !ok {
		return fmt.Errorf("unknown link action")
	}
	if args == nil {
		return fmt.Errorf("arguments must be an object")
	}
	for _, key := range e.Arguments {
		if !optional(key) {
			if _, exists := args[key]; !exists {
				return fmt.Errorf("missing argument %q", key)
			}
		}
	}
	for k, v := range args {
		allowed := false
		for _, key := range e.Arguments {
			allowed = allowed || key == k
		}
		if !allowed {
			return fmt.Errorf("argument %q is not allowed in this action", k)
		}
		if k == "replicas" {
			raw, err := json.Marshal(v)
			var n int32
			if err != nil || json.Unmarshal(raw, &n) != nil || string(raw) == "null" {
				return fmt.Errorf("replicas must be an integer")
			}
		} else if k == "revert" {
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("revert must be true or false")
			}
		} else {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("argument %q must be a string", k)
			}
			if len(s) > 1024 || strings.ContainsAny(s, "\\\r\n\x00") || strings.Contains(s, redact.Mask) || redact.String(s) != s {
				return fmt.Errorf("argument %q cannot be safely included in a link", k)
			}
		}
	}
	return nil
}

// ParseQuery consumes source separately; it can never become a tool argument.
func ParseQuery(name string, q url.Values) (map[string]any, string, error) {
	args := map[string]any{}
	source, sig := "rule", ""
	for key, values := range q {
		if len(values) != 1 {
			return nil, "", fmt.Errorf("field %q must occur at most once", key)
		}
		if key == "sig" {
			sig = values[0]
			continue
		}
		if key == "source" {
			var err error
			source, err = Source(values[0], "rule")
			if err != nil {
				return nil, "", err
			}
			continue
		}
		if key == "revert" {
			b, err := strconv.ParseBool(values[0])
			if err != nil || (values[0] != "true" && values[0] != "false") {
				return nil, "", fmt.Errorf("revert must be true or false")
			}
			args[key] = b
			continue
		}
		if key == "replicas" {
			n, err := strconv.ParseInt(values[0], 10, 32)
			if err != nil {
				return nil, "", fmt.Errorf("replicas must be an integer")
			}
			args[key] = int32(n)
		} else {
			args[key] = values[0]
		}
	}
	if err := Validate(name, args); err != nil {
		return nil, "", err
	}
	if len(signingKey) > 0 && !hmac.Equal([]byte(sig), []byte(signature(name, args, source))) {
		source = Unsigned
	}
	return args, source, nil
}

// URL is the sole builder for new action links. Empty base supports local routes.
func URL(base, name string, args map[string]any, source string) (string, error) {
	if err := Validate(name, args); err != nil {
		return "", err
	}
	source, err := Source(source, "rule")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil || u == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (base != "" && (u.Host == "" || u.Scheme != "https")) {
		return "", fmt.Errorf("approval base must be an HTTPS origin")
	}
	q := url.Values{"source": {source}}
	for k, v := range args {
		q.Set(k, fmt.Sprint(v))
	}
	if len(signingKey) > 0 {
		q.Set("sig", signature(name, args, source))
	}
	u.Path = "/approve/action/" + name
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Parse accepts the generic route and both historical aliases, with the same
// strict query rules. A query may never override an alias's path arguments.
func Parse(u *url.URL) (Entry, map[string]any, string, error) {
	if u == nil || !strings.HasPrefix(u.EscapedPath(), "/approve/") {
		return Entry{}, nil, "", fmt.Errorf("unknown link action")
	}
	name := ""
	pathArgs := map[string]string{}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/approve/"), "/")
	for i := range parts {
		part, err := url.PathUnescape(parts[i])
		if err != nil {
			return Entry{}, nil, "", fmt.Errorf("invalid action path")
		}
		parts[i] = part
	}
	switch {
	case len(parts) == 2 && parts[0] == "action":
		name = parts[1]
	case len(parts) == 5 && parts[0] == "restart":
		name = "k8s_rollout_restart"
		for i, key := range []string{"cluster", "kind", "namespace", "name"} {
			pathArgs[key] = parts[i+1]
		}
	case len(parts) == 4 && parts[0] == "restore-image":
		name = "k8s_restore_image"
		for i, key := range []string{"cluster", "namespace", "name"} {
			pathArgs[key] = parts[i+1]
		}
	default:
		return Entry{}, nil, "", fmt.Errorf("unknown link action")
	}
	e, ok := Lookup(name)
	if !ok {
		return Entry{}, nil, "", fmt.Errorf("unknown link action")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Entry{}, nil, "", fmt.Errorf("invalid action query")
	}
	for key, value := range pathArgs {
		if _, exists := q[key]; exists {
			return Entry{}, nil, "", fmt.Errorf("argument %q repeats a path field", key)
		}
		q[key] = []string{value}
	}
	args, source, err := ParseQuery(name, q)
	return e, args, source, err
}
