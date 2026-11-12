// Package client talks to the git-mirror server over its unix socket.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"git-mirror/internal/api"
	"git-mirror/internal/datadir"
)

// ErrNotRunning means no server answers on the socket.
var ErrNotRunning = errors.New("git-mirror server is not running")

type Client struct {
	dir  datadir.Dir
	http *http.Client
}

func New(dir datadir.Dir) *Client {
	sock := dir.SocketPath()
	return &Client{
		dir: dir,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					// Never talk to a socket another local user could have planted.
					if err := dir.CheckSocketDir(false); err != nil {
						return nil, err
					}
					var d net.Dialer
					return d.DialContext(ctx, "unix", sock)
				},
			},
		},
	}
}

// Ping reports whether the server is reachable.
func (c *Client) Ping(ctx context.Context) (api.Ping, error) {
	var p api.Ping
	err := c.do(ctx, http.MethodGet, "/v1/ping", nil, &p)
	return p, err
}

// Running is a convenience wrapper around Ping.
func (c *Client) Running(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := c.Ping(ctx)
	return err == nil
}

func (c *Client) Status(ctx context.Context) (api.Status, error) {
	var s api.Status
	err := c.do(ctx, http.MethodGet, "/v1/status", nil, &s)
	return s, err
}

// Sync asks the server to sync the named repos now (all enabled if empty).
func (c *Client) Sync(ctx context.Context, names ...string) (api.SyncResponse, error) {
	q := url.Values{}
	for _, n := range names {
		q.Add("name", n)
	}
	var r api.SyncResponse
	err := c.do(ctx, http.MethodPost, "/v1/sync", q, &r)
	return r, err
}

// Reload tells the server to re-read config.json immediately.
func (c *Client) Reload(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/reload", nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, out any) error {
	u := "http://git-mirror" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ne *net.OpError
		if errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%w (%s)", ErrNotRunning, c.dir.SocketPath())
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		var e api.Error
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
