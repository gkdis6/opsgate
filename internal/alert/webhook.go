package alert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"text/template"
	"time"
)

type webhookSender struct {
	opts         Options
	name, format string
	headers      map[string]string
	template     *template.Template
}

func newWebhook(opts Options) Sender {
	if opts.Client == nil {
		opts.Client = &http.Client{}
		if opts.PrivateOnly {
			opts.Client.Transport = privateTransport()
		}
	}
	client := *opts.Client
	client.Timeout = 10 * time.Second
	// A redirect could move signed alerts to an unconfigured host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	opts.Client = &client
	return &webhookSender{opts: opts}
}

func privateTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// The checked address must be the receiver's, not a proxy's.
	t.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsPrivate() && !ip.IsLoopback() {
			return fmt.Errorf("webhook address %s is not private", host)
		}
		return nil
	}}
	t.DialContext = dialer.DialContext
	return t
}

func (s *webhookSender) Name() string {
	if s.name != "" {
		return s.name
	}
	return "webhook"
}

func (s *webhookSender) Send(ctx context.Context, b *Batch) (retry bool, err error) {
	body, err := s.body(b)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.URL, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	if len(s.opts.Secret) > 0 {
		req.Header.Set("X-Opsgate-Signature", Sign(s.opts.Secret, body))
	}
	resp, err := s.opts.Client.Do(req)
	if err != nil {
		// The URL may carry a secret query key; report only the failure.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return true, fmt.Errorf("post failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500, fmt.Errorf("post returned HTTP %d", resp.StatusCode)
	}
	return false, nil
}
