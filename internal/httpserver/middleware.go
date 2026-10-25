package httpserver

import (
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
