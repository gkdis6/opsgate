// opsgate-analyzer is a separate, read-only investigation worker.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gkdis6/opsgate/internal/analyzer"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	fs := flag.NewFlagSet("opsgate-analyzer", flag.ContinueOnError)
	path := fs.String("config", "", "analyzer YAML path (required)")
	check := fs.Bool("check", false, "validate configuration and credential files without contacting services")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return fmt.Errorf("usage: opsgate-analyzer -config analyzer.yaml [-check]")
	}
	c, err := analyzer.Load(*path)
	if err != nil {
		return err
	}
	worker, err := analyzer.New(c)
	if err != nil {
		return err
	}
	if *check {
		fmt.Println("analyzer configuration and credential files are valid")
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: c.Listen, Handler: worker.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() { worker.Run(ctx) })
	go func() { done <- srv.ListenAndServe() }()
	log.Printf("analyzer listening on %s; signed alerts only; one read-only worker", c.Listen)
	select {
	case err := <-done:
		stop()
		wg.Wait()
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
		}
		wg.Wait()
		return nil
	}
}
