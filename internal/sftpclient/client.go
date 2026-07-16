package sftpclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackh54/sftp2sftp/internal/auth"
	"github.com/jackh54/sftp2sftp/internal/endpoint"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	keepAliveInterval = 30 * time.Second
	dialTimeout       = 30 * time.Second

	// Tuned for throughput on OpenSSH / Pterodactyl hosts over WAN links.
	sftpMaxPacket         = 256 * 1024
	sftpConcurrentPerFile = 128
)

// Client wraps an SSH+SFTP session with reconnect support.
type Client struct {
	name   string
	target endpoint.Endpoint
	method auth.Method

	mu   sync.Mutex
	conn *ssh.Client
	sftp *sftp.Client

	stopKeepAlive context.CancelFunc
}

func Connect(ctx context.Context, name string, target endpoint.Endpoint, method auth.Method) (*Client, error) {
	c := &Client{name: name, target: target, method: method}
	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) connect(ctx context.Context) error {
	signers, err := c.method.Signers()
	if err != nil {
		return err
	}

	authMethods := []ssh.AuthMethod{}
	if len(signers) > 0 {
		authMethods = append(authMethods, ssh.PublicKeys(signers...))
	}
	if c.method.Password != "" {
		authMethods = append(authMethods, ssh.Password(c.method.Password))
	}
	if len(authMethods) == 0 {
		return fmt.Errorf("%s: no authentication method configured", c.name)
	}

	cfg := &ssh.ClientConfig{
		User:            c.target.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         dialTimeout,
	}

	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.target.Addr())
	if err != nil {
		return fmt.Errorf("%s: dial %s: %w", c.name, c.target.Addr(), err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, c.target.Addr(), cfg)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("%s: ssh handshake: %w", c.name, err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)
	sftpClient, err := sftp.NewClient(client,
		sftp.MaxPacketUnchecked(sftpMaxPacket),
		sftp.UseConcurrentWrites(true),
		sftp.MaxConcurrentRequestsPerFile(sftpConcurrentPerFile),
	)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("%s: open sftp: %w", c.name, err)
	}

	c.conn = client
	c.sftp = sftpClient
	c.startKeepAlive()
	return nil
}

func (c *Client) startKeepAlive() {
	if c.stopKeepAlive != nil {
		c.stopKeepAlive()
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.stopKeepAlive = cancel

	go func() {
		ticker := time.NewTicker(keepAliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.mu.Lock()
				if c.conn != nil {
					_, _, _ = c.conn.SendRequest("keepalive@openssh.com", true, nil)
				}
				c.mu.Unlock()
			}
		}
	}()
}

func (c *Client) SFTP() *sftp.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sftp
}

func (c *Client) WithSFTP(fn func(*sftp.Client) error) error {
	c.mu.Lock()
	client := c.sftp
	c.mu.Unlock()
	if client == nil {
		return fmt.Errorf("%s: not connected", c.name)
	}
	return fn(client)
}

func (c *Client) Reconnect(ctx context.Context) error {
	c.Close()
	return c.connect(ctx)
}

func (c *Client) Close() {
	if c.stopKeepAlive != nil {
		c.stopKeepAlive()
		c.stopKeepAlive = nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sftp != nil {
		_ = c.sftp.Close()
		c.sftp = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// Manager holds pooled source and destination SFTP sessions.
type Manager struct {
	Source *Pool
	Dest   *Pool
}

func NewManager(source, dest *Pool) *Manager {
	return &Manager{Source: source, Dest: dest}
}

// NewPooledManager opens one SSH/SFTP session per worker on each side.
func NewPooledManager(ctx context.Context, source, dest *Client, workers int) (*Manager, error) {
	sourcePool, err := GrowPool(ctx, source, workers)
	if err != nil {
		return nil, err
	}
	destPool, err := GrowPool(ctx, dest, workers)
	if err != nil {
		sourcePool.Close()
		return nil, err
	}
	return NewManager(sourcePool, destPool), nil
}

func (m *Manager) Close() {
	if m.Source != nil {
		m.Source.Close()
	}
	if m.Dest != nil {
		m.Dest.Close()
	}
}

func (m *Manager) ReconnectAll(ctx context.Context) error {
	if err := m.Source.ReconnectAll(ctx); err != nil {
		return fmt.Errorf("reconnect source: %w", err)
	}
	if err := m.Dest.ReconnectAll(ctx); err != nil {
		return fmt.Errorf("reconnect dest: %w", err)
	}
	return nil
}

func (m *Manager) EnsureReconnect(ctx context.Context, fn func() error) error {
	err := fn()
	if err == nil || !isConnectionError(err) {
		return err
	}

	if reconnErr := m.ReconnectAll(ctx); reconnErr != nil {
		return fmt.Errorf("%v (original: %v)", reconnErr, err)
	}
	return fn()
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if err == io.EOF {
		return true
	}
	if ne, ok := err.(net.Error); ok {
		return ne.Timeout() || !ne.Temporary()
	}
	msg := err.Error()
	for _, part := range []string{
		"connection lost",
		"connection reset",
		"broken pipe",
		"EOF",
		"use of closed network connection",
		"ssh: disconnect",
	} {
		if strings.Contains(msg, part) {
			return true
		}
	}
	return false
}

// MkdirAll creates parent directories on the remote host.
func MkdirAll(client *sftp.Client, dir string) error {
	if dir == "" || dir == "/" {
		return nil
	}

	// Fast path: directory already exists.
	if st, err := client.Stat(dir); err == nil {
		if st.IsDir() {
			return nil
		}
		return fmt.Errorf("%s exists and is not a directory", dir)
	}

	parts := splitPath(dir)
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if current == "" {
			current = "/" + part
		} else {
			current += "/" + part
		}

		if err := client.Mkdir(current); err != nil {
			if st, statErr := client.Stat(current); statErr == nil && st.IsDir() {
				continue
			}
			if os.IsExist(err) {
				continue
			}
			return fmt.Errorf("mkdir %s: %w", current, err)
		}
	}
	return nil
}

func splitPath(path string) []string {
	path = trimSlash(path)
	if path == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}
	return parts
}

func trimSlash(path string) string {
	for len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	if path == "/" {
		return ""
	}
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	return path
}
