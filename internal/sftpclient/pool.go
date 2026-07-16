package sftpclient

import (
	"context"
	"fmt"
)

// Pool holds one SSH/SFTP session per parallel worker.
type Pool struct {
	clients []*Client
}

// GrowPool ensures at least n connections, reusing seed as the first session.
func GrowPool(ctx context.Context, seed *Client, n int) (*Pool, error) {
	if n < 1 {
		n = 1
	}
	pool := &Pool{clients: make([]*Client, 0, n)}
	pool.clients = append(pool.clients, seed)

	for i := 1; i < n; i++ {
		c, err := Connect(ctx, seed.name, seed.target, seed.method)
		if err != nil {
			pool.CloseFrom(i)
			return nil, fmt.Errorf("%s pool connection %d/%d: %w", seed.name, i+1, n, err)
		}
		pool.clients = append(pool.clients, c)
	}
	return pool, nil
}

func (p *Pool) Len() int {
	return len(p.clients)
}

func (p *Pool) Client(worker int) *Client {
	if len(p.clients) == 0 {
		return nil
	}
	if worker < 0 {
		worker = 0
	}
	return p.clients[worker%len(p.clients)]
}

func (p *Pool) ReconnectAll(ctx context.Context) error {
	for i, c := range p.clients {
		if err := c.Reconnect(ctx); err != nil {
			return fmt.Errorf("reconnect %s session %d: %w", c.name, i+1, err)
		}
	}
	return nil
}

// Close closes every connection in the pool.
func (p *Pool) Close() {
	p.CloseFrom(0)
}

// CloseFrom closes connections starting at index start.
func (p *Pool) CloseFrom(start int) {
	for i := start; i < len(p.clients); i++ {
		if p.clients[i] != nil {
			p.clients[i].Close()
			p.clients[i] = nil
		}
	}
}
