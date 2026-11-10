package httpserver

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// New sessions are admitted at a bounded rate; requests on an existing
// session and sessionless-protocol requests are not counted.
func TestSessionAdmission(t *testing.T) {
	sessions = &sessionAdmission{users: map[string]*rate.Limiter{}}
	t.Cleanup(func() { sessions = &sessionAdmission{users: map[string]*rate.Limiter{}} })
	served := 0
	h := protocolSplit(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served++ }), http.NotFoundHandler())
	post := func(session string) int {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		r.Header.Set("Mcp-Session-Id", session)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := range 20 {
		if code := post(""); code != http.StatusOK {
			t.Fatalf("session %d: %d", i, code)
		}
	}
	if code := post(""); code != http.StatusTooManyRequests {
		t.Fatalf("session over the burst: %d", code)
	}
	if code := post("existing"); code != http.StatusOK || served != 21 {
		t.Fatalf("existing session: %d, served %d", code, served)
	}
}

// A sender that stops mid-body is cut off; a handler that has read the whole
// body may keep responding past the upload deadline.
func TestBodyReadDeadline(t *testing.T) {
	old := bodyReadTimeout
	bodyReadTimeout = 100 * time.Millisecond
	errs := make(chan error, 2)
	srv := httptest.NewServer(common(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		errs <- err
		if r.URL.Path == "/slow-response" {
			time.Sleep(300 * time.Millisecond)
		}
		_, _ = w.Write([]byte("done"))
	})))
	defer func() {
		srv.Close()
		bodyReadTimeout = old
	}()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "POST /stall HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\npartial")
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("stalled upload read without error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stalled upload was not cut off")
	}
	resp, err := http.Post(srv.URL+"/slow-response", "text/plain", strings.NewReader("complete body"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := <-errs; err != nil || string(body) != "done" {
		t.Fatalf("response after a full upload: %q, %v", body, err)
	}
}
