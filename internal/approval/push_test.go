package approval

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

type pushRoundTripper func(*http.Request) (*http.Response, error)

func (f pushRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func pushConfig(t *testing.T) config.ApprovalPush {
	t.Helper()
	cfg := config.Default().Approval.Push
	cfg.Enabled = true
	cfg.Subject = "mailto:owner@example.com"
	cfg.StateFile = filepath.Join(t.TempDir(), "state", "push.json")
	return cfg
}

func testPush(t *testing.T) *pushSender {
	t.Helper()
	p, err := newPushSender(pushConfig(t), "https://ops.example")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.cancel)
	return p
}

func pushResponse(code int) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("capability-do-not-log"))}
}

func TestVAPIDRequest(t *testing.T) {
	p := testPush(t)
	now := time.Now()
	endpoint := "https://fcm.googleapis.com:443/send/capability?secret=value"
	p.client.Transport = pushRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != endpoint || req.ContentLength != 0 || req.Body != nil {
			t.Fatalf("unexpected push request: %s body %v", req.Method, req.Body)
		}
		ttl, err := strconv.Atoi(req.Header.Get("TTL"))
		if err != nil || ttl < 58 || ttl > 60 || req.Header.Get("Urgency") != "high" {
			t.Fatal("bad TTL or urgency", req.Header)
		}
		if req.Header.Get("Content-Encoding") != "" {
			t.Fatal("payload-less push must not encrypt or include content")
		}
		auth := req.Header.Get("Authorization")
		parts := strings.Split(strings.TrimPrefix(auth, "vapid t="), ", k=")
		if len(parts) != 2 || !strings.HasPrefix(auth, "vapid t=") || parts[1] != p.publicKey {
			t.Fatal("invalid VAPID authorization")
		}
		jwt := strings.Split(parts[0], ".")
		if len(jwt) != 3 {
			t.Fatal("invalid JWT")
		}
		decode := func(s string) []byte {
			b, err := base64.RawURLEncoding.DecodeString(s)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		var header map[string]string
		if json.Unmarshal(decode(jwt[0]), &header) != nil || header["typ"] != "JWT" || header["alg"] != "ES256" {
			t.Fatal("bad JWT header")
		}
		var claims struct {
			Audience string `json:"aud"`
			Expires  int64  `json:"exp"`
			Subject  string `json:"sub"`
		}
		if json.Unmarshal(decode(jwt[1]), &claims) != nil || claims.Audience != "https://fcm.googleapis.com" || claims.Subject != p.cfg.Subject || claims.Expires > now.Add(12*time.Hour).Unix() || claims.Expires < now.Add(11*time.Hour).Unix() {
			t.Fatalf("bad JWT claims: %+v", claims)
		}
		x, y := elliptic.Unmarshal(elliptic.P256(), decode(parts[1]))
		if x == nil {
			t.Fatal("bad public key")
		}
		digest := sha256.Sum256([]byte(jwt[0] + "." + jwt[1]))
		sig := decode(jwt[2])
		if len(sig) != 64 || !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			t.Fatal("JWT signature does not verify")
		}
		return pushResponse(http.StatusCreated), nil
	})
	if gone, err := p.send(context.Background(), endpoint, now.Add(time.Minute)); gone || err != nil {
		t.Fatalf("send = %v, %v", gone, err)
	}
}

func TestPushEndpointPolicy(t *testing.T) {
	p := testPush(t)
	for _, raw := range []string{
		"http://fcm.googleapis.com/send/secret", "https://127.0.0.1/secret", "https://10.0.0.1/secret", "https://[::1]/secret", "https://[fc00::1]/secret",
		"https://evil.example/secret", "https://fcm.googleapis.com.evil.example/secret", "https://notify.windows.com/secret", "https://.notify.windows.com/secret", "https://a..notify.windows.com/secret", "https://fcm.googleapis.com@evil.example/secret",
		"https://fcm.googleapis.com:8443/secret", "https://fcm.googleapis.com:/secret", "https://user@fcm.googleapis.com/secret", "https://fcm.googleapis.com/secret#fragment", "https://fcm.googleapis.com/" + strings.Repeat("x", 1024),
	} {
		if _, err := p.endpoint(raw); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("accepted or leaked invalid endpoint %q", raw)
		}
	}
	for _, raw := range []string{"https://fcm.googleapis.com/send/secret", "https://updates.push.services.mozilla.com/wpush/v2/secret", "https://web.push.apple.com/secret", "https://a.notify.windows.com/secret"} {
		if _, err := p.endpoint(raw); err != nil {
			t.Errorf("valid endpoint rejected: %v", err)
		}
	}
	// Even an operator allowlist entry for a private literal cannot allow SSRF.
	p.cfg.AllowedHosts = []string{"127.0.0.1", "10.0.0.1", "100.64.0.1"}
	for _, ip := range p.cfg.AllowedHosts {
		if _, err := p.endpoint("https://" + ip + "/secret"); err == nil {
			t.Fatal("allowlist bypasses private check")
		}
	}
}

func TestPushDialAddressPolicy(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "0.0.0.0", "ff02::1", "64:ff9b::a00:1", "2002:7f00:1::", "2001:db8::1", "198.18.0.1", "203.0.113.1", "240.0.0.1"} {
		if pushAddressCheck("tcp", net.JoinHostPort(host, "443"), nil) == nil {
			t.Errorf("private destination accepted: %s", host)
		}
	}
	for _, host := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if err := pushAddressCheck("tcp", net.JoinHostPort(host, "443"), nil); err != nil {
			t.Error(err)
		}
	}
	tr := publicPushTransport()
	if tr.Proxy != nil || tr.DialContext == nil || tr.TLSHandshakeTimeout > 5*time.Second {
		t.Fatal("insecure push transport")
	}
}

func TestPushDoesNotFollowRedirect(t *testing.T) {
	p := testPush(t)
	count := 0
	p.client.Transport = pushRoundTripper(func(*http.Request) (*http.Response, error) {
		count++
		res := pushResponse(http.StatusTemporaryRedirect)
		res.Header.Set("Location", "https://127.0.0.1/private-secret")
		return res, nil
	})
	if _, err := p.send(context.Background(), "https://fcm.googleapis.com/secret", time.Now().Add(time.Minute)); err == nil || count != 1 {
		t.Fatalf("redirect followed: count=%d err=%v", count, err)
	}
}

func TestPushStatePermissionsReloadAndNewestWins(t *testing.T) {
	p := testPush(t)
	for i := 0; i < 11; i++ {
		if err := p.update("alice", pushSubscription{Endpoint: "https://fcm.googleapis.com/" + strconv.Itoa(i), Keys: map[string]string{"auth": "device-key"}}, false); err != nil {
			t.Fatal(err)
		}
	}
	bob := pushSubscription{Endpoint: "https://web.push.apple.com/bob"}
	if err := p.update("bob", bob, false); err != nil {
		t.Fatal(err)
	}
	subs := p.subscriptions("alice")
	if len(subs) != 10 || subs[0].Endpoint != "https://fcm.googleapis.com/1" || len(p.subscriptions("bob")) != 1 {
		t.Fatal("subscription limit or isolation failed")
	}
	if err := p.update("alice", subs[0], false); err != nil {
		t.Fatal(err)
	}
	if p.subscriptions("alice")[9].Endpoint != subs[0].Endpoint {
		t.Fatal("renewed device is not newest")
	}
	info, err := os.Stat(p.cfg.StateFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state permissions %v %v", info, err)
	}
	// Reload also tightens permissions on an existing state file.
	if err := os.Chmod(p.cfg.StateFile, 0644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newPushSender(p.cfg, p.state.Origin)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.cancel()
	if reloaded.publicKey != p.publicKey || !reflect.DeepEqual(reloaded.state.Users, p.state.Users) {
		t.Fatal("state or VAPID key lost on reload")
	}
	info, _ = os.Stat(p.cfg.StateFile)
	if info.Mode().Perm() != 0600 {
		t.Fatal("reload did not tighten permissions")
	}
	if err := reloaded.update("alice", bob, true); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.subscriptions("bob")) != 1 {
		t.Fatal("another user removed bob's device")
	}
	if _, err := newPushSender(p.cfg, "https://other.example"); err == nil {
		t.Fatal("state accepted for different server")
	}
}

func TestPushStorageFailureRollsBack(t *testing.T) {
	p := testPush(t)
	sub := pushSubscription{Endpoint: "https://fcm.googleapis.com/secret"}
	if err := p.update("alice", sub, false); err != nil {
		t.Fatal(err)
	}
	p.cfg.StateFile = filepath.Dir(p.cfg.StateFile) // cannot atomically replace a directory
	if err := p.update("alice", sub, true); err == nil {
		t.Fatal("storage failure ignored")
	}
	if len(p.subscriptions("alice")) != 1 {
		t.Fatal("failed persistence changed memory")
	}
}

func TestPushGoneRemovalAndRedactedFailures(t *testing.T) {
	for _, code := range []int{404, 410, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			p := testPush(t)
			sub := pushSubscription{Endpoint: "https://fcm.googleapis.com/capability-do-not-log"}
			if err := p.update("alice", sub, false); err != nil {
				t.Fatal(err)
			}
			// A round trip completion channel followed by a marker job lets us wait
			// for the removal/persistence step, without polling or opening sockets.
			processed := make(chan struct{}, 2)
			p.client.Transport = pushRoundTripper(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "marker") {
					processed <- struct{}{}
					return pushResponse(201), nil
				}
				if code == 500 {
					return nil, errors.New("https://fcm.googleapis.com/capability-do-not-log")
				}
				return pushResponse(code), nil
			})
			if err := p.update("marker", pushSubscription{Endpoint: "https://fcm.googleapis.com/marker"}, false); err != nil {
				t.Fatal(err)
			}
			var logs strings.Builder
			original := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(original)
			go p.work()
			p.enqueue("alice", time.Now().Add(time.Minute))
			p.enqueue("marker", time.Now().Add(time.Minute))
			select {
			case <-processed:
			case <-time.After(3 * time.Second):
				t.Fatal("push worker stuck")
			}
			p.cancel()
			<-p.done
			want := 0
			if code == 500 {
				want = 1
			}
			if len(p.subscriptions("alice")) != want {
				t.Fatal("incorrect expired subscription removal")
			}
			if strings.Contains(logs.String(), "capability-do-not-log") {
				t.Fatal("capability leaked to logs")
			}
			loaded, err := newPushSender(p.cfg, p.state.Origin)
			if err != nil {
				t.Fatal(err)
			}
			defer loaded.cancel()
			if len(loaded.subscriptions("alice")) != want {
				t.Fatal("removal not persisted")
			}
		})
	}
}

func TestMCPPushIsAsyncAndNoPushFromLinks(t *testing.T) {
	f := newRestartFixture(t, writer())
	p := testPush(t)
	f.manager.push = p
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	p.client.Transport = pushRoundTripper(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return pushResponse(201), nil
	})
	if err := p.update("github:operator", pushSubscription{Endpoint: "https://fcm.googleapis.com/secret"}, false); err != nil {
		t.Fatal(err)
	}
	go p.work()
	defer func() { close(release); p.cancel(); <-p.done }()
	// A link creates an approval while already in the browser; it never queues push.
	w := webRequest(f.mux, http.MethodGet, "action/"+restartTool+"?cluster=prod&kind=deployment&namespace=apps&name=web", "github:operator", nil, nil)
	if w.Code != http.StatusSeeOther || len(p.jobs) != 0 || calls.Load() != 0 {
		t.Fatalf("link push/status: %d calls=%d", w.Code, calls.Load())
	}
	request := approvalRequest()
	callDone := make(chan error, 1)
	go func() {
		_, err := f.manager.Confirm(context.Background(), nil, hashedRequest(request))
		callDone <- err
	}()
	select {
	case err := <-callDone:
		if !errors.Is(err, tool.ErrApprovalPending) {
			t.Fatalf("expected pending, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("push blocked the tool call")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("MCP approval did not send push")
	}
	// Slow push holds neither the tool call nor the manager lock.
	unlocked := make(chan struct{})
	go func() { f.manager.mu.Lock(); f.manager.mu.Unlock(); close(unlocked) }()
	select {
	case <-unlocked:
	case <-time.After(3 * time.Second):
		t.Fatal("push held manager lock")
	}
	r := httptest.NewRequest(http.MethodGet, "https://ops.example/approvals", nil)
	r.Header.Set("X-Test-User", request.Caller.User)
	f.mux.ServeHTTP(httptest.NewRecorder(), r)
}

func TestPushQueueNeverWaitsForPersistence(t *testing.T) {
	p := testPush(t)
	p.mu.Lock()
	completed := make(chan struct{})
	go func() {
		for i := 0; i < cap(p.jobs)+1; i++ {
			p.enqueue("alice", time.Now().Add(time.Minute))
		}
		close(completed)
	}()
	select {
	case <-completed:
	case <-time.After(time.Second):
		p.mu.Unlock()
		t.Fatal("enqueue blocked on persistence or a full queue")
	}
	p.mu.Unlock()
	if len(p.jobs) != cap(p.jobs) {
		t.Fatal("queue is not bounded")
	}
}

func TestInvalidPushStateFailsClosed(t *testing.T) {
	p := testPush(t)
	original, err := os.ReadFile(p.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{[]byte("{"), []byte(`{"version":2}`), []byte(`{"version":1,"origin":"https://ops.example","users":{},"key":"AA=="}`), make([]byte, maxPushState+1)} {
		if err := os.WriteFile(p.cfg.StateFile, body, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := newPushSender(p.cfg, p.state.Origin); err == nil {
			t.Fatal("invalid state replaced instead of rejected")
		}
		after, err := os.ReadFile(p.cfg.StateFile)
		if err != nil || !reflect.DeepEqual(after, body) {
			t.Fatal("failed startup changed state")
		}
	}
	if err := os.WriteFile(p.cfg.StateFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	p.cfg.AllowedHosts = []string{"web.push.apple.com"}
	if err := p.update("alice", pushSubscription{Endpoint: "https://fcm.googleapis.com/secret"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := newPushSender(p.cfg, p.state.Origin); err == nil {
		t.Fatal("reload ignored narrower endpoint policy")
	}
}
