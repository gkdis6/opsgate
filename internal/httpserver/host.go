package httpserver

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gkdis6/opsgate/internal/config"
)

func checkHost(cfg config.Config, next http.Handler) http.Handler {
	public, err := url.Parse(cfg.HTTP.PublicURL)
	publicHost := ""
	if err == nil && public.User == nil {
		publicHost = public.Host
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listenerHost, _, _ := net.SplitHostPort(cfg.HTTP.Listen)
		loopbackListener := isLoopbackHost(listenerHost)
		// A connection on loopback does not make a wildcard listener local.
		// Require both the configured bind address and the connection to be local.
		if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			localHost, _, _ := net.SplitHostPort(addr.String())
			loopbackListener = loopbackListener && isLoopbackHost(localHost)
		}
		host := authorityHostname(r.Host)
		if host == "" || !(publicHost != "" && strings.EqualFold(r.Host, publicHost) || loopbackListener && isLoopbackHost(host)) {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authorityHostname(authority string) string {
	u, err := url.Parse("//" + authority)
	if err != nil || u.Host != authority || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(authority, "\\?#@ \t\r\n") {
		return ""
	}
	if strings.HasSuffix(authority, ":") {
		return ""
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ""
		}
	}
	if strings.Contains(u.Hostname(), ":") && (!strings.HasPrefix(authority, "[") || net.ParseIP(u.Hostname()) == nil) {
		return ""
	}
	return u.Hostname()
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
