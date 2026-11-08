package approval

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
)

const maxPushState = 4 << 20

type pushSubscription struct {
	Endpoint       string            `json:"endpoint"`
	ExpirationTime *float64          `json:"expirationTime,omitempty"`
	Keys           map[string]string `json:"keys,omitempty"`
}

type pushDisk struct {
	Version int                           `json:"version"`
	Origin  string                        `json:"origin"`
	Key     []byte                        `json:"key"`
	Users   map[string][]pushSubscription `json:"users"`
}

type pushJob struct {
	user    string
	expires time.Time
}

type pushSender struct {
	mu        sync.Mutex // separate from the approval lock; serializes atomic state writes
	cfg       config.ApprovalPush
	state     pushDisk
	key       *ecdsa.PrivateKey
	publicKey string
	client    *http.Client
	jobs      chan pushJob
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
}

// EnablePush loads durable subscriptions before HTTP serving starts. A storage
// error fails startup rather than silently replacing the devices' VAPID key.
func (m *Manager) EnablePush(cfg config.ApprovalPush) error {
	if !cfg.Enabled {
		return nil
	}
	if m.browser == nil || cfg.StateFile == "" {
		return fmt.Errorf("push requires browser approval and a state file")
	}
	p, err := newPushSender(cfg, m.publicURL)
	if err != nil {
		return err
	}
	m.push = p
	go p.work()
	return nil
}

// Close stops push delivery, including any in-flight HTTP request.
func (m *Manager) Close() {
	if m.push != nil {
		m.push.cancel()
		<-m.push.done
		m.push.client.CloseIdleConnections()
	}
}

func newPushSender(cfg config.ApprovalPush, origin string) (*pushSender, error) {
	p := &pushSender{cfg: cfg, jobs: make(chan pushJob, 100), done: make(chan struct{})}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.client = &http.Client{Transport: publicPushTransport(), Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	p.state = pushDisk{Version: 1, Origin: origin, Users: map[string][]pushSubscription{}}
	f, err := os.Open(cfg.StateFile)
	if err == nil {
		defer f.Close()
		b, readErr := io.ReadAll(io.LimitReader(f, maxPushState+1))
		var loaded pushDisk
		if readErr != nil || len(b) > maxPushState || json.Unmarshal(b, &loaded) != nil || loaded.Version != 1 || loaded.Origin != origin || loaded.Users == nil {
			return nil, fmt.Errorf("cannot load push state")
		}
		p.state = loaded
		p.key, err = x509.ParseECPrivateKey(p.state.Key)
		if err != nil || p.key.Curve != elliptic.P256() {
			return nil, fmt.Errorf("invalid push signing key")
		}
		if len(p.state.Users) > 1000 {
			return nil, fmt.Errorf("too many push users")
		}
		for user, subscriptions := range p.state.Users {
			if user == "" || len(user) > 1024 || len(subscriptions) > 10 {
				return nil, fmt.Errorf("invalid push subscriptions")
			}
			for _, s := range subscriptions {
				if _, err := p.endpoint(s.Endpoint); err != nil {
					return nil, fmt.Errorf("push state contains an endpoint outside the current policy")
				}
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		p.key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("cannot generate push signing key")
		}
		p.state.Key, err = x509.MarshalECPrivateKey(p.key)
		if err != nil {
			return nil, fmt.Errorf("cannot encode push signing key")
		}
	} else {
		return nil, fmt.Errorf("cannot read push state")
	}
	p.publicKey = base64.RawURLEncoding.EncodeToString(elliptic.Marshal(p.key.Curve, p.key.X, p.key.Y))
	if err := p.saveLocked(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *pushSender) saveLocked() error {
	b, err := json.Marshal(p.state)
	if err != nil || len(b) > maxPushState {
		return fmt.Errorf("push state exceeds size limit")
	}
	dir := filepath.Dir(p.cfg.StateFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create push state directory")
	}
	f, err := os.CreateTemp(dir, ".push-*")
	if err != nil {
		return fmt.Errorf("cannot create push state")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), p.cfg.StateFile)
	}
	if err == nil {
		parent, openErr := os.Open(dir)
		err = openErr
		if err == nil {
			err = parent.Sync()
			_ = parent.Close()
		}
	}
	if err != nil {
		return fmt.Errorf("cannot persist push state")
	}
	return nil
}

func (p *pushSender) update(user string, sub pushSubscription, remove bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous, existed := p.state.Users[user]
	next := slices.DeleteFunc(slices.Clone(previous), func(s pushSubscription) bool { return s.Endpoint == sub.Endpoint })
	if !remove {
		if !existed && len(p.state.Users) >= 1000 {
			return fmt.Errorf("too many push users")
		}
		next = append(next, sub)
		if len(next) > 10 {
			next = next[len(next)-10:]
		}
	}
	if len(next) == 0 {
		delete(p.state.Users, user)
	} else {
		p.state.Users[user] = next
	}
	if err := p.saveLocked(); err != nil {
		if existed {
			p.state.Users[user] = previous
		} else {
			delete(p.state.Users, user)
		}
		return err
	}
	return nil
}

func (p *pushSender) subscriptions(user string) []pushSubscription {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.state.Users[user])
}

func (p *pushSender) enqueue(user string, expires time.Time) {
	// Never wait on subscription persistence in a tool call. The worker takes
	// its own snapshot only after the nonblocking enqueue.
	select {
	case p.jobs <- pushJob{user, expires}:
	default:
		log.Print("approval push queue full; notification skipped")
	}
}

func (p *pushSender) work() {
	defer close(p.done)
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.jobs:
			for _, sub := range p.subscriptions(job.user) {
				if p.ctx.Err() != nil {
					return
				}
				gone, err := p.send(p.ctx, sub.Endpoint, job.expires)
				if gone {
					if err := p.update(job.user, sub, true); err != nil {
						log.Print("cannot remove expired push subscription")
					}
				} else if err != nil {
					// Never log the URL, response body or underlying net/http error: they
					// can contain the endpoint's capability path and query string.
					log.Print("approval push delivery failed")
				}
			}
		}
	}
}

func (p *pushSender) endpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 1024 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("invalid push endpoint")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil && !publicPushIP(ip) {
		return nil, fmt.Errorf("push endpoint must be public")
	}
	for _, allowed := range p.cfg.AllowedHosts {
		allowed = strings.ToLower(allowed)
		if host == allowed || strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, allowed[1:]) && len(host) > len(allowed)-1 && !strings.HasPrefix(host, ".") && !strings.Contains(host, "..") {
			return u, nil
		}
	}
	return nil, fmt.Errorf("push endpoint host is not allowed")
}

// Mirror the watch transport's dial-time check, inverted. Control runs on
// each resolved address before connecting, so DNS rebinding cannot bypass it.
func publicPushIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// Also exclude shared carrier, benchmark, documentation and reserved
		// ranges, which IsGlobalUnicast alone considers unicast.
		return v4[0] != 0 && v4[0] < 224 &&
			!(v4[0] == 100 && v4[1]&0xc0 == 64) &&
			!(v4[0] == 192 && v4[1] == 0 && (v4[2] == 0 || v4[2] == 2)) &&
			!(v4[0] == 198 && (v4[1] == 18 || v4[1] == 19 || v4[1] == 51 && v4[2] == 100)) &&
			!(v4[0] == 203 && v4[1] == 0 && v4[2] == 113)
	}
	// Accept IPv6 global unicast only; exclude transition/translation and
	// documentation ranges that could otherwise tunnel to private addresses.
	return ip[0]&0xe0 == 0x20 &&
		!(ip[0] == 0x20 && ip[1] == 0x02) &&
		!(ip[0] == 0x20 && ip[1] == 0x01 && (ip[2] == 0 && ip[3] == 0 || ip[2] == 0x0d && ip[3] == 0xb8))
}

func pushAddressCheck(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !publicPushIP(net.ParseIP(host)) {
		return fmt.Errorf("push destination is not public")
	}
	return nil
}

func publicPushTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil // Do not let an environment proxy bypass address validation.
	t.DialContext = (&net.Dialer{Timeout: 3 * time.Second, Control: pushAddressCheck}).DialContext
	t.TLSHandshakeTimeout = 3 * time.Second
	t.ResponseHeaderTimeout = 3 * time.Second
	t.MaxResponseHeaderBytes = 16 << 10
	return t
}

func (p *pushSender) authorization(origin string, now time.Time) (string, error) {
	enc := base64.RawURLEncoding.EncodeToString
	claims, err := json.Marshal(struct {
		Audience string `json:"aud"`
		Expires  int64  `json:"exp"`
		Subject  string `json:"sub"`
	}{origin, now.Add(12 * time.Hour).Unix(), p.cfg.Subject})
	if err != nil {
		return "", err
	}
	unsigned := enc([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + enc(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, p.key, digest[:])
	if err != nil {
		return "", err
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return "vapid t=" + unsigned + "." + enc(signature) + ", k=" + p.publicKey, nil
}

func (p *pushSender) send(ctx context.Context, endpoint string, expires time.Time) (bool, error) {
	u, err := p.endpoint(endpoint)
	if err != nil {
		return false, err
	}
	now := time.Now()
	ttl := int64(expires.Sub(now).Seconds())
	if ttl <= 0 {
		return false, nil
	}
	origin := u.Scheme + "://" + strings.ToLower(u.Host)
	// A default port does not form part of the URL origin.
	if u.Port() == "443" {
		origin = "https://" + strings.TrimSuffix(strings.ToLower(u.Host), ":443")
	}
	auth, err := p.authorization(origin, now)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("cannot build push request")
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("TTL", strconv.FormatInt(ttl, 10))
	req.Header.Set("Urgency", "high")
	res, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("push transport failed")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone {
		return true, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return false, fmt.Errorf("push service rejected notification")
	}
	return false, nil
}
