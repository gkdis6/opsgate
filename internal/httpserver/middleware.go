package httpserver

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		allowed := strings.HasPrefix(path, "/.well-known/") || path == "/mcp" || path == "/oauth/token" || path == "/oauth/register" || path == "/oauth/revoke"
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
			w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func common(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		response := &statusWriter{ResponseWriter: w}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		defer func() {
			status := response.status
			if status == 0 {
				status = http.StatusOK
			}
			path := r.URL.Path
			if strings.HasPrefix(path, "/approve/") {
				path = "/approve/*"
			}
			log.Printf("http method=%q path=%q status=%d duration=%s", r.Method, path, status, time.Since(start))
		}()
		// A byte limit alone lets a slow sender hold a connection and its
		// goroutine. Bound the upload, and lift the bound once the body has
		// been read so a long response (an approval wait, a stream) is not cut.
		if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
			controller := http.NewResponseController(response)
			if controller.SetReadDeadline(time.Now().Add(bodyReadTimeout)) == nil {
				r.Body = &deadlineBody{ReadCloser: r.Body, done: func() { _ = controller.SetReadDeadline(time.Time{}) }}
			}
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				// Panic values and stacks can contain credentials from downstream handlers.
				log.Print("HTTP handler panic")
				if response.status == 0 {
					http.Error(response, "internal server error", http.StatusInternalServerError)
				} else {
					panic(http.ErrAbortHandler)
				}
			}
		}()
		next.ServeHTTP(response, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *statusWriter) Flush() { _ = w.FlushError() }

// bodyReadTimeout covers the largest accepted upload (about 0.5 MiB of note
// arguments) on a slow mobile link. A variable so tests can shorten it.
var bodyReadTimeout = 30 * time.Second

type deadlineBody struct {
	io.ReadCloser
	done func()
}

// Read lifts the deadline at the end of the body. A body that is closed
// unread keeps it, which bounds the server's own drain of the remainder.
func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && b.done != nil {
		b.done()
		b.done = nil
	}
	return n, err
}
