package tunrun

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Proxy struct {
	Scheme   string
	Type     string
	Host     string
	Port     int
	Username string
	Password string
}

var ProxyEnvironmentKeys = []string{
	"ALL_PROXY",
	"all_proxy",
	"HTTPS_PROXY",
	"https_proxy",
	"HTTP_PROXY",
	"http_proxy",
	"SOCKS_PROXY",
	"socks_proxy",
}

var scrubEnvironmentKeys = append([]string{
	"NO_PROXY",
	"no_proxy",
}, ProxyEnvironmentKeys...)

var sudoEnvironmentKeys = []string{
	"SUDO_COMMAND",
	"SUDO_GID",
	"SUDO_UID",
	"SUDO_USER",
}

func ProxyFromEnvironment(env []string) (proxyURL string, source string, ok bool) {
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		values[key] = value
	}

	for _, key := range ProxyEnvironmentKeys {
		value := strings.TrimSpace(values[key])
		if value != "" {
			return value, key, true
		}
	}
	return "", "", false
}

func EnvironmentWithoutProxy(env []string) []string {
	return environmentWithoutKeys(env, scrubEnvironmentKeys)
}

func environmentWithoutKeys(env []string, keys []string) []string {
	remove := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		remove[key] = struct{}{}
	}

	cleaned := make([]string, 0, len(env))
	for _, item := range env {
		key, _, found := strings.Cut(item, "=")
		if !found {
			cleaned = append(cleaned, item)
			continue
		}
		if _, ok := remove[key]; ok {
			continue
		}
		cleaned = append(cleaned, item)
	}
	return cleaned
}

func TargetEnvironment(env []string, identity TargetIdentity) []string {
	removeKeys := make([]string, 0, len(scrubEnvironmentKeys)+len(sudoEnvironmentKeys))
	removeKeys = append(removeKeys, scrubEnvironmentKeys...)
	removeKeys = append(removeKeys, sudoEnvironmentKeys...)

	cleaned := environmentWithoutKeys(env, removeKeys)
	if !identity.Valid {
		return cleaned
	}

	cleaned = setEnv(cleaned, "USER", identity.Username)
	cleaned = setEnv(cleaned, "LOGNAME", identity.Username)
	cleaned = setEnv(cleaned, "HOME", identity.HomeDir)
	cleaned = setEnv(cleaned, "SHELL", identity.Shell)
	return cleaned
}

func setEnv(env []string, key, value string) []string {
	if value == "" {
		return env
	}
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func ParseProxy(raw string) (Proxy, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Proxy{}, err
	}

	scheme := strings.ToLower(u.Scheme)
	proxyType := ""
	switch scheme {
	case "socks", "socks5", "socks5h":
		proxyType = "socks"
		scheme = "socks5"
	case "http":
		proxyType = "http"
	default:
		return Proxy{}, fmt.Errorf("unsupported proxy scheme %q; use socks5:// or http://", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return Proxy{}, fmt.Errorf("proxy host is required")
	}

	portText := u.Port()
	if portText == "" {
		return Proxy{}, fmt.Errorf("proxy port is required")
	}

	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return Proxy{}, fmt.Errorf("invalid proxy port %q", portText)
	}

	p := Proxy{
		Scheme: scheme,
		Type:   proxyType,
		Host:   host,
		Port:   port,
	}
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	return p, nil
}

func (p Proxy) Address() string {
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}

func (p Proxy) URLWithEndpoint(host string, port int) string {
	u := &url.URL{
		Scheme: p.Scheme,
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
	}
	if p.Username != "" || p.Password != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u.String()
}

func ParseDNS(raw string) (host string, port int, err error) {
	if raw == "" {
		return "", 0, fmt.Errorf("dns server cannot be empty")
	}

	host, portText, err := net.SplitHostPort(raw)
	if err != nil {
		if strings.Contains(err.Error(), "missing port in address") {
			return raw, 53, nil
		}
		return "", 0, fmt.Errorf("invalid dns server %q: %w", raw, err)
	}

	port, err = strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid dns port %q", portText)
	}
	return host, port, nil
}
