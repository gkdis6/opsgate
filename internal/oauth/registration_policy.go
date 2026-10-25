package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/ui"
)

func redirectHost(raw string) string {
	u, _ := url.Parse(raw) // Registration validates every URI before policy evaluation.
	if u.Scheme != "http" && u.Scheme != "https" {
		return strings.ToLower(u.Scheme) + ":"
	}
	return strings.ToLower(u.Hostname())
}

func redirectHosts(uris []string) []string {
	hosts := []string{}
	for _, uri := range uris {
		host := redirectHost(uri)
		if !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func (s *Server) redirectsAllowed(uris []string) bool {
	if len(s.cfg.AllowedRedirectHosts) == 0 {
		return true
	}
	for _, uri := range uris {
		host := redirectHost(uri)
		allowed := false
		for _, entry := range s.cfg.AllowedRedirectHosts {
			if strings.EqualFold(host, strings.Trim(entry, "[]")) || strings.EqualFold(entry, "localhost") && loopbackOrigin(host) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

type clientNotice struct {
	Event         string   `json:"event"`
	ClientID      string   `json:"client_id"`
	ClientName    string   `json:"client_name"`
	RedirectHosts []string `json:"redirect_hosts"`
	ApproveURL    string   `json:"approve_url"`
}

// Deliver outside mu and without retries or redirects. A failing webhook must
// neither block other authorization requests nor leak its URL or response.
func (s *Server) notifyClient(ctx context.Context, notice clientNotice) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(notice)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.NotifyURL, bytes.NewReader(body))
	if err != nil {
		log.Print("client pending notification failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := *s.httpClient
	client.Timeout = 5 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		log.Print("client pending notification failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Print("client pending notification failed")
	}
}

func pendingClientResponse() response {
	return func(w http.ResponseWriter, _ *http.Request) {
		htmlHeaders(w)
		ui.Message(w, http.StatusOK, "Client approval pending", "This client is waiting for administrator approval")
	}
}
