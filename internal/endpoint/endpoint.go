package endpoint

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const DefaultPort = 22

// Endpoint describes a remote SFTP location.
// Canonical form (Pterodactyl-compatible): sftp://user@host:port[/path]
type Endpoint struct {
	User string
	Host string
	Port int
	Path string
}

func (e Endpoint) String() string {
	host := e.Host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	path := e.Path
	if path == "/" {
		path = ""
	}
	if e.Port == DefaultPort {
		return fmt.Sprintf("sftp://%s@%s%s", e.User, host, path)
	}
	return fmt.Sprintf("sftp://%s@%s:%d%s", e.User, host, e.Port, path)
}

func (e Endpoint) Addr() string {
	host := e.Host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, e.Port)
}

// Parse parses an SFTP endpoint.
// Preferred form is the Pterodactyl Launch SFTP URL: sftp://user@host:port[/path].
// Legacy form user@host:port/path is still accepted. Port defaults to 22.
// Path defaults to "/" when omitted (Pterodactyl jail root).
func Parse(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Endpoint{}, fmt.Errorf("endpoint is empty")
	}

	if i := strings.Index(raw, "://"); i >= 0 {
		scheme := raw[:i]
		if !strings.EqualFold(scheme, "sftp") {
			return Endpoint{}, fmt.Errorf("invalid endpoint %q: unsupported scheme %q (expected sftp)", raw, scheme)
		}
		return parseURL(raw)
	}
	return parseLegacy(raw)
}

func parseURL(raw string) (Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: %w", raw, err)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: query and fragment are not allowed", raw)
	}
	if u.User == nil || u.User.Username() == "" {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: expected sftp://user@host:port", raw)
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: password in URL is not supported; use the auth prompt or SSH key", raw)
	}
	if u.Hostname() == "" {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: host is empty", raw)
	}

	port := DefaultPort
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return Endpoint{}, fmt.Errorf("invalid endpoint %q: invalid port %q", raw, u.Port())
		}
	}

	path := u.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: remote path must be absolute", raw)
	}

	return Endpoint{
		User: u.User.Username(),
		Host: u.Hostname(),
		Port: port,
		Path: cleanRemotePath(path),
	}, nil
}

// parseLegacy accepts user@host:port/path (path optional; defaults to "/").
func parseLegacy(raw string) (Endpoint, error) {
	at := strings.LastIndex(raw, "@")
	if at <= 0 || at == len(raw)-1 {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: expected sftp://user@host:port", raw)
	}

	user := raw[:at]
	rest := raw[at+1:]

	hostport := rest
	path := "/"
	if slash := strings.Index(rest, "/"); slash >= 0 {
		hostport = rest[:slash]
		path = rest[slash:]
		if !strings.HasPrefix(path, "/") {
			return Endpoint{}, fmt.Errorf("invalid endpoint %q: remote path must be absolute", raw)
		}
	}

	host, port, err := splitHostPort(hostport)
	if err != nil {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: %w", raw, err)
	}

	return Endpoint{
		User: user,
		Host: host,
		Port: port,
		Path: cleanRemotePath(path),
	}, nil
}

func splitHostPort(hostport string) (string, int, error) {
	if hostport == "" {
		return "", 0, fmt.Errorf("host is empty")
	}

	if strings.HasPrefix(hostport, "[") {
		end := strings.Index(hostport, "]")
		if end < 0 {
			return "", 0, fmt.Errorf("unterminated IPv6 address")
		}
		host := hostport[1:end]
		remainder := hostport[end+1:]
		if remainder == "" {
			return host, DefaultPort, nil
		}
		if !strings.HasPrefix(remainder, ":") {
			return "", 0, fmt.Errorf("invalid host/port %q", hostport)
		}
		port, err := strconv.Atoi(remainder[1:])
		if err != nil || port < 1 || port > 65535 {
			return "", 0, fmt.Errorf("invalid port %q", remainder[1:])
		}
		return host, port, nil
	}

	if strings.Count(hostport, ":") == 1 {
		parts := strings.SplitN(hostport, ":", 2)
		port, err := strconv.Atoi(parts[1])
		if err != nil || port < 1 || port > 65535 {
			return "", 0, fmt.Errorf("invalid port %q", parts[1])
		}
		return parts[0], port, nil
	}

	if strings.Contains(hostport, ":") {
		return "", 0, fmt.Errorf("ambiguous host %q; use [ipv6]:port form", hostport)
	}

	return hostport, DefaultPort, nil
}

func cleanRemotePath(path string) string {
	if path == "/" {
		return path
	}
	return strings.TrimRight(path, "/")
}
