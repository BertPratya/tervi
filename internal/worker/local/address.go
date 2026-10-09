package local

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// StandardAddress validates a worker server URL and returns its comparison form.
func StandardAddress(address string) (string, error) {
	if strings.ContainsAny(address, "?#") {
		return "", fmt.Errorf("invalid server address")
	}
	u, err := url.Parse(address)
	if err != nil || u == nil {
		return "", fmt.Errorf("invalid server address")
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Opaque != "" || u.User != nil || u.Host == "" {
		return "", fmt.Errorf("invalid server address")
	}
	if u.Path != "" && u.Path != "/" || u.RawPath != "" && u.RawPath != "/" {
		return "", fmt.Errorf("invalid server address")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", fmt.Errorf("invalid server address")
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("invalid server address")
	}
	bracketedHost := strings.HasPrefix(u.Host, "[")
	if bracketedHost {
		if !strings.Contains(host, ":") || strings.Contains(host, "%") || net.ParseIP(host) == nil {
			return "", fmt.Errorf("invalid server address")
		}
	} else if strings.Count(u.Host, ":") > 1 {
		return "", fmt.Errorf("invalid server address")
	}
	port := u.Port()
	portNumber := 0
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("invalid server address")
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid server address")
		}
		portNumber = n
		port = strconv.Itoa(n)
	}

	if (scheme == "http" && portNumber == 80) || (scheme == "https" && portNumber == 443) {
		port = ""
	}
	if strings.Contains(host, ":") {
		if port != "" {
			host = net.JoinHostPort(host, port)
		} else {
			host = "[" + host + "]"
		}
	} else if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host, nil
}
