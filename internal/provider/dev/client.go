package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

type provider struct{ client *http.Client }

func newProvider(socket string) (*provider, error) {
	if !filepath.IsAbs(socket) {
		return nil, fmt.Errorf("dev runner_socket must be an absolute path")
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
		MaxConnsPerHost: 8, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 35 * time.Second,
	}
	return &provider{client: &http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (p *provider) request(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://dev-runner"+path, body)
	if err != nil {
		return fmt.Errorf("invalid runner request")
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("dev runner unavailable or request timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dev runner rejected request (HTTP %d)", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return fmt.Errorf("invalid or oversized runner response")
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("invalid runner response")
	}
	return nil
}
