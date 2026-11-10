//go:build e2e

// Package e2e runs the real binary against a real cluster: a person signs in
// with a passkey, opens an action link, approves it, and the workload
// restarts and is reported recovered; a crashing pod reaches a signed webhook.
//
// It needs KUBECONFIG, kubectl and a built binary (OPSGATE_BIN, default
// ../../bin/opsgate). It creates and deletes its own namespace. Run with:
//
//	go test -tags e2e -count=1 -timeout 15m ./test/e2e/
package e2e

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
)

var b64 = base64.RawURLEncoding

func kubectl(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// cluster skips without a cluster and returns a fresh namespace holding one
// ready deployment named web.
func cluster(t *testing.T) (kubeconfig, namespace string) {
	t.Helper()
	kubeconfig = os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG is not set")
	}
	suffix := make([]byte, 4)
	rand.Read(suffix)
	namespace = "opsgate-e2e-" + hex.EncodeToString(suffix)
	kubectl(t, "create", "namespace", namespace)
	t.Cleanup(func() {
		_ = exec.Command("kubectl", "delete", "namespace", namespace, "--wait=false").Run()
	})
	kubectl(t, "-n", namespace, "create", "deployment", "web", "--image=registry.k8s.io/pause:3.10")
	kubectl(t, "-n", namespace, "rollout", "status", "deployment/web", "--timeout=180s")
	return kubeconfig, namespace
}

func opsgateBinary(t *testing.T) string {
	t.Helper()
	path := os.Getenv("OPSGATE_BIN")
	if path == "" {
		path = "../../bin/opsgate"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("build the binary first (make build): %v", err)
	}
	return abs
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

// process is one run of the binary with its log kept for failures.
type process struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	log bytes.Buffer
}

func start(t *testing.T, args ...string) *process {
	t.Helper()
	p := &process{cmd: exec.Command(opsgateBinary(t), args...)}
	pipe, err := p.cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stdout = io.Discard
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			p.mu.Lock()
			p.log.WriteString(scanner.Text() + "\n")
			p.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		p.stop()
		if t.Failed() {
			// Invite links in this log are single-use and already spent.
			t.Logf("opsgate %s log:\n%s", strings.Join(args, " "), p.output())
		}
	})
	return p
}

func (p *process) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.log.String()
}

func (p *process) stop() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = p.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = p.cmd.Process.Kill()
		}
		p.cmd.Process = nil
	}
}

func waitFor(t *testing.T, what string, limit time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// browser is a client that keeps cookies by hand: the server's __Host-
// cookies are Secure, which a cookie jar would not send over loopback http.
type browser struct {
	origin  string
	cookies map[string]string
	client  *http.Client
}

func newBrowser(origin string) *browser {
	return &browser{origin: origin, cookies: map[string]string{}, client: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (b *browser) do(t *testing.T, method, target, contentType string, body []byte) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(method, b.origin+target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// What a browser sends for a same-origin form post.
	req.Header.Set("Origin", b.origin)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for name, value := range b.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	text, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(text), resp.Header.Get("Location")
}

func (b *browser) get(t *testing.T, target string) (int, string, string) {
	return b.do(t, "GET", target, "", nil)
}

func (b *browser) postJSON(t *testing.T, target string, value any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(value)
	code, text, _ := b.do(t, "POST", target, "application/json", raw)
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	if out == nil {
		out = map[string]any{"body": text}
	}
	return code, out
}

// authenticator is a minimal ES256 platform passkey with user verification.
type authenticator struct {
	key    *ecdsa.PrivateKey
	id     []byte
	handle []byte
	count  uint32
}

func (a *authenticator) authData(t *testing.T, rpID string, flags byte, attested bool) []byte {
	t.Helper()
	rp := sha256.Sum256([]byte(rpID))
	var buf bytes.Buffer
	buf.Write(rp[:])
	buf.WriteByte(flags)
	_ = binary.Write(&buf, binary.BigEndian, a.count)
	if attested {
		buf.Write(make([]byte, 16))
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(a.id)))
		buf.Write(a.id)
		x, y := a.key.PublicKey.X.FillBytes(make([]byte, 32)), a.key.PublicKey.Y.FillBytes(make([]byte, 32))
		cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(cose)
	}
	return buf.Bytes()
}

func clientData(typ, challenge, origin string) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return raw
}

func (a *authenticator) create(t *testing.T, options map[string]any, origin string) map[string]any {
	t.Helper()
	pk, ok := options["publicKey"].(map[string]any)
	if !ok {
		t.Fatalf("registration options: %v", options)
	}
	a.handle, _ = b64.DecodeString(pk["user"].(map[string]any)["id"].(string))
	rpID := pk["rp"].(map[string]any)["id"].(string)
	att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(t, rpID, 0x45, true)})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"id": b64.EncodeToString(a.id), "rawId": b64.EncodeToString(a.id), "type": "public-key", "response": map[string]any{
		"clientDataJSON":    b64.EncodeToString(clientData("webauthn.create", pk["challenge"].(string), origin)),
		"attestationObject": b64.EncodeToString(att),
	}}
}

func (a *authenticator) get(t *testing.T, options map[string]any, origin, rpID string) map[string]any {
	t.Helper()
	pk, ok := options["publicKey"].(map[string]any)
	if !ok {
		t.Fatalf("login options: %v", options)
	}
	a.count++
	auth := a.authData(t, rpID, 0x05, false)
	cd := clientData("webauthn.get", pk["challenge"].(string), origin)
	sum := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, auth...), sum[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"id": b64.EncodeToString(a.id), "rawId": b64.EncodeToString(a.id), "type": "public-key", "response": map[string]any{
		"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(auth),
		"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(a.handle),
	}}
}

// signIn follows a protected page through passkey sign-in and returns the
// page it lands on.
func (b *browser) signIn(t *testing.T, a *authenticator, target, rpID string) (int, string, string) {
	t.Helper()
	code, text, location := b.get(t, target)
	if code != http.StatusSeeOther || location != "/oauth/passkey" {
		t.Fatalf("protected page did not ask for sign-in: %d %q %s", code, location, text)
	}
	if code, text, _ := b.get(t, "/oauth/passkey"); code != http.StatusOK || !strings.Contains(text, "passkey") {
		t.Fatalf("sign-in page: %d %s", code, text)
	}
	code, options := b.postJSON(t, "/oauth/passkey/begin", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("passkey begin: %d %v", code, options)
	}
	if code, out := b.postJSON(t, "/oauth/passkey/finish", map[string]any{"credential": a.get(t, options, b.origin, rpID)}); code != http.StatusOK || out["next"] != "/oauth/passkey/complete" {
		t.Fatalf("passkey finish: %d %v", code, out)
	}
	code, text, location = b.get(t, "/oauth/passkey/complete")
	if code != http.StatusSeeOther && code != http.StatusFound {
		t.Fatalf("passkey complete: %d %s", code, text)
	}
	return b.get(t, location)
}

var (
	inviteLine = regexp.MustCompile(`/passkey/register\?invite=([A-Za-z0-9_-]+)`)
	csrfField  = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
)

func TestApprovedRestartOnARealCluster(t *testing.T) {
	kubeconfig, namespace := cluster(t)
	port := freePort(t)
	origin := "http://127.0.0.1:" + port
	dir := t.TempDir()
	audit := filepath.Join(dir, "audit.jsonl")
	config := filepath.Join(dir, "opsgate.yaml")
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`
server:
  audit_log: %q
http:
  listen: 127.0.0.1:%s
  public_url: %s
auth:
  mode: builtin
  builtin:
    state_file: %q
    users:
      - subject: passkey:e2e
        scopes: [ops:read, ops:write]
    upstream:
      provider: passkey
approval:
  web: true
  state_file: %q
providers:
  kubernetes:
    enabled: true
    kubeconfig: %q
    namespaces: [%q]
    rollout_restart: ["%s/*"]
`, audit, port, origin, filepath.Join(dir, "oauth.json"), filepath.Join(dir, "approvals.json"), kubeconfig, namespace, namespace)), 0600); err != nil {
		t.Fatal(err)
	}
	healthy := func() bool {
		resp, err := http.Get(origin + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	server := start(t, "-config", config, "-transport", "http")
	waitFor(t, "the server to listen", 30*time.Second, healthy)

	// The first start logs a one-time passkey invite for the configured user.
	var invite string
	waitFor(t, "the passkey invite", 10*time.Second, func() bool {
		match := inviteLine.FindStringSubmatch(server.output())
		if match != nil {
			invite = match[1]
		}
		return invite != ""
	})
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	passkey := &authenticator{key: key, id: make([]byte, 16)}
	rand.Read(passkey.id)
	user := newBrowser(origin)
	code, options := user.postJSON(t, "/passkey/register/begin", map[string]string{"invite": invite})
	if code != http.StatusOK {
		t.Fatalf("register begin: %d %v", code, options)
	}
	if code, out := user.postJSON(t, "/passkey/register/finish", map[string]any{"invite": invite, "credential": passkey.create(t, options, origin)}); code != http.StatusOK {
		t.Fatalf("register finish: %d %v", code, out)
	}

	// A target outside the allowlist never becomes an approval.
	denied := "/approve/action/k8s_rollout_restart?" + url.Values{"cluster": {"default"}, "kind": {"deployment"}, "namespace": {"kube-system"}, "name": {"coredns"}}.Encode()
	if code, text, _ := user.signIn(t, passkey, denied, "127.0.0.1"); code != http.StatusForbidden || !strings.Contains(text, "Action unavailable") {
		t.Fatalf("restart outside the allowlist: %d %s", code, text)
	}

	// Opening the link changes nothing; it only creates the approval card.
	link := "/approve/action/k8s_rollout_restart?" + url.Values{"cluster": {"default"}, "kind": {"deployment"}, "namespace": {namespace}, "name": {"web"}, "source": {"rule"}}.Encode()
	code, text, approval := user.get(t, link)
	if code != http.StatusSeeOther || !strings.HasPrefix(approval, "/approve/") {
		t.Fatalf("open link: %d %q %s", code, approval, text)
	}
	code, page, _ := user.get(t, approval)
	if code != http.StatusOK || !strings.Contains(page, namespace+"/web") || !strings.Contains(page, "did not sign") {
		t.Fatalf("approval card: %d %s", code, page)
	}
	restartedAt := func() string {
		return kubectl(t, "-n", namespace, "get", "deployment", "web", "-o", `jsonpath={.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}`)
	}
	if at := restartedAt(); at != "" {
		t.Fatalf("opening the link restarted the workload at %s", at)
	}
	staleCSRF := csrfField.FindStringSubmatch(page)

	// The card survives a server restart; the person signs in again and the
	// form token from before the restart is refused.
	server.stop()
	server = start(t, "-config", config, "-transport", "http")
	waitFor(t, "the server to listen again", 30*time.Second, healthy)
	code, page, _ = user.signIn(t, passkey, approval, "127.0.0.1")
	fresh := csrfField.FindStringSubmatch(page)
	if code != http.StatusOK || fresh == nil || staleCSRF == nil || fresh[1] == staleCSRF[1] {
		t.Fatalf("approval card after restart: %d %s", code, page)
	}
	form := func(csrf, decision string) []byte {
		return []byte(url.Values{"csrf": {csrf}, "decision": {decision}}.Encode())
	}
	if code, _, _ := user.do(t, "POST", approval, "application/x-www-form-urlencoded", form(staleCSRF[1], "approve")); code != http.StatusForbidden || restartedAt() != "" {
		t.Fatalf("stale form token: %d, restarted %q", code, restartedAt())
	}
	// Another origin cannot submit the form either.
	foreign := newBrowser(origin)
	foreign.cookies = user.cookies
	req, _ := http.NewRequest("POST", origin+approval, bytes.NewReader(form(fresh[1], "approve")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	for name, value := range user.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	if resp, err := foreign.client.Do(req); err != nil || resp.StatusCode != http.StatusForbidden || restartedAt() != "" {
		t.Fatalf("cross-site approve: %v %v, restarted %q", resp, err, restartedAt())
	}

	code, page, _ = user.do(t, "POST", approval, "application/x-www-form-urlencoded", form(fresh[1], "approve"))
	if code != http.StatusOK || !strings.Contains(page, "Requested rollout restart") {
		t.Fatalf("approve: %d %s", code, page)
	}
	if restartedAt() == "" {
		t.Fatal("approved restart did not reach the cluster")
	}
	// Approving again must not restart again.
	first := restartedAt()
	if code, _, _ := user.do(t, "POST", approval, "application/x-www-form-urlencoded", form(fresh[1], "approve")); code != http.StatusConflict || restartedAt() != first {
		t.Fatalf("second approve: %d", code)
	}
	waitFor(t, "the recovery check to report Recovered", 4*time.Minute, func() bool {
		_, page, _ := user.get(t, approval)
		if strings.Contains(page, "Not recovered yet") {
			t.Fatalf("healthy workload reported as not recovered: %s", page)
		}
		return strings.Contains(page, "Recovered")
	})
	log, _ := os.ReadFile(audit)
	for _, want := range []string{`"tool":"k8s_rollout_restart"`, `"outcome":"approved"`, `"outcome":"ok"`, `"source":"link"`, `"user":"passkey:e2e"`} {
		if !strings.Contains(string(log), want) {
			t.Errorf("audit log lacks %s:\n%s", want, log)
		}
	}
}

func TestCrashingPodReachesSignedWebhook(t *testing.T) {
	kubeconfig, namespace := cluster(t)
	secret := []byte("0123456789abcdef0123456789abcdef")
	type delivery struct {
		body      []byte
		signature string
	}
	received := make(chan delivery, 16)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- delivery{body, r.Header.Get("X-Opsgate-Signature")}
	}))
	defer receiver.Close()
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretFile, secret, 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "opsgate.yaml")
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`
providers:
  kubernetes:
    enabled: true
    kubeconfig: %q
    namespaces: [%q]
watch:
  outputs:
    - name: test
      type: webhook
      url: %s
      secret_file: %q
  problems_only: true
  batch: 1s
`, kubeconfig, namespace, receiver.URL, secretFile)), 0600); err != nil {
		t.Fatal(err)
	}
	start(t, "watch", "-config", config)
	time.Sleep(3 * time.Second)
	kubectl(t, "-n", namespace, "create", "deployment", "crash", "--image=registry.k8s.io/e2e-test-images/busybox:1.36.1-1", "--", "sh", "-c", "exit 1")

	deadline := time.After(4 * time.Minute)
	for {
		select {
		case d := <-received:
			mac := hmac.New(sha256.New, secret)
			mac.Write(d.body)
			if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); d.signature != want {
				t.Fatalf("signature %q, want %q", d.signature, want)
			}
			var batch struct {
				Source string `json:"source"`
				Alerts []struct {
					Namespace, Name, Kind, Reason, Severity string
				} `json:"alerts"`
			}
			if err := json.Unmarshal(d.body, &batch); err != nil || batch.Source != "opsgate" {
				t.Fatalf("batch: %v %s", err, d.body)
			}
			for _, a := range batch.Alerts {
				if a.Namespace == namespace && strings.HasPrefix(a.Name, "crash") && a.Severity == "warning" {
					t.Logf("alert: %s %s %s", a.Kind, a.Name, a.Reason)
					return
				}
			}
		case <-deadline:
			t.Fatal("no warning for the crashing pod reached the webhook")
		}
	}
}
