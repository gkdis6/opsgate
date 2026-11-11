package httpserver

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/metrics"
)

// Serve listens on cfg.HTTP.Listen until ctx is done, then shuts down gracefully.
func Serve(ctx context.Context, cfg config.Config, h http.Handler) error {
	srv := &http.Server{Addr: cfg.HTTP.Listen, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
	listener, err := net.Listen("tcp", cfg.HTTP.Listen)
	if err != nil {
		return err
	}
	return serve(ctx, srv, listener)
}

func serve(ctx context.Context, srv *http.Server, listener net.Listener) error {
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			_ = srv.Close()
		}
		<-done
		if err != nil {
			log.Print("HTTP graceful shutdown timed out; connections closed")
		}
		return err
	}
}

// ServeMetrics serves only /metrics on addr until ctx is done or the returned
// function is called. It is a separate listener so the metrics never share the
// public origin, its Host check or its authentication.
func ServeMetrics(ctx context.Context, addr string) (func(), error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { _ = srv.Serve(listener) }()
	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if srv.Shutdown(shutdownCtx) != nil {
			_ = srv.Close()
		}
	}
	go func() { <-ctx.Done(); stop() }()
	return stop, nil
}
