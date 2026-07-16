package endpoint

import (
	"fmt"
	"strconv"
	"strings"
)

const DefaultPort = 22

// Endpoint describes a remote SFTP location: user@host:port/path
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
	if e.Port == DefaultPort {
		return fmt.Sprintf("%s@%s%s", e.User, host, e.Path)
	}
	return fmt.Sprintf("%s@%s:%d%s", e.User, host, e.Port, e.Path)
}

func (e Endpoint) Addr() string {
	host := e.Host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, e.Port)
}

// Parse parses user@host:port/path. Port defaults to 22.
func Parse(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Endpoint{}, fmt.Errorf("endpoint is empty")
	}

	at := strings.LastIndex(raw, "@")
	if at <= 0 || at == len(raw)-1 {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: expected user@host/path", raw)
	}

	user := raw[:at]
	rest := raw[at+1:]

	slash := strings.Index(rest, "/")
	if slash < 0 {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: remote path is required", raw)
	}

	path := rest[slash:]
	if path == "/" {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: remote path must not be root only", raw)
	}
	if !strings.HasPrefix(path, "/") {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q: remote path must be absolute", raw)
	}

	host, port, err := splitHostPort(rest[:slash])
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
