package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func requireKnownHost(cfg Config, next http.Handler) http.Handler {
	allowedHosts := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
		"::1":       {},
	}
	if publicURL, err := url.Parse(cfg.PublicURL); err == nil {
		if host := strings.ToLower(publicURL.Hostname()); host != "" {
			allowedHosts[host] = struct{}{}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(requestHostName(r.Host))
		if _, ok := allowedHosts[host]; !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error": "unknown_host"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestHostName(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}
