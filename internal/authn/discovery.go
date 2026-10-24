package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type issuerMetadata struct {
	Issuer string `json:"issuer"`
	JWKS   string `json:"jwks_uri"`
}

func discover(ctx context.Context, client *http.Client, issuer string) (issuerMetadata, error) {
	var metadata issuerMetadata
	u, _ := url.Parse(issuer)
	// RFC 8414 inserts the well-known component before the issuer path.
	fallback := *u
	fallback.Path = "/.well-known/oauth-authorization-server" + strings.TrimSuffix(u.Path, "/")
	for _, endpoint := range []string{strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration", fallback.String()} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return metadata, fmt.Errorf("invalid discovery URL")
		}
		response, err := client.Do(req)
		if err != nil {
			return metadata, fmt.Errorf("issuer discovery failed")
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			continue
		}
		if readErr != nil || json.Unmarshal(body, &metadata) != nil {
			return metadata, fmt.Errorf("invalid issuer metadata")
		}
		if metadata.Issuer != issuer || !validEndpoint(metadata.JWKS) {
			return metadata, fmt.Errorf("issuer metadata mismatch or invalid JWKS URL")
		}
		return metadata, nil
	}
	return metadata, fmt.Errorf("issuer discovery unavailable")
}

func validEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err == nil {
		response.Body = http.MaxBytesReader(nil, response.Body, 1<<20)
	}
	return response, err
}
