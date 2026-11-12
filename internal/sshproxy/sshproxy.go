// Package sshproxy implements the built-in ssh ProxyCommand: it connects to
// an HTTP CONNECT or SOCKS5 proxy and pipes stdin/stdout to the destination.
// The URL syntax follows corkscrew-go: http://, https://, socks5://, socks5h://.
package sshproxy

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// Proxy is a parsed proxy URL.
type Proxy struct {
	Scheme string // http, https, socks5, socks5h
	Host   string // host:port
	Auth   *Auth  // credentials from the URL, may be nil
}

// Auth is a username and password.
type Auth struct {
	User, Password string
}

// Schemes lists the accepted URL schemes.
var Schemes = []string{"http", "https", "socks5", "socks5h"}

// bufferedConn returns bytes the response reader already consumed.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

// ParseURL validates a proxy URL and fills in the default port.
func ParseURL(s string) (Proxy, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Proxy{}, errors.New("proxy url is required")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return Proxy{}, fmt.Errorf("invalid proxy url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch scheme {
	case "http":
		port = cmp.Or(port, "80")
	case "https":
		port = cmp.Or(port, "443")
	case "socks5", "socks5h":
		port = cmp.Or(port, "1080")
	default:
		return Proxy{}, fmt.Errorf("proxy url: unsupported scheme %q (use %s)", u.Scheme, strings.Join(Schemes, ", "))
	}
	if u.Hostname() == "" {
		return Proxy{}, errors.New("proxy url: host is required")
	}
	if u.Path != "" && u.Path != "/" {
		return Proxy{}, errors.New("proxy url: path is not allowed")
	}
	p := Proxy{Scheme: scheme, Host: net.JoinHostPort(u.Hostname(), port)}
	if u.User != nil {
		pw, _ := u.User.Password()
		p.Auth = &Auth{User: u.User.Username(), Password: pw}
	}
	return p, nil
}

// ParseAuth parses "user:password".
func ParseAuth(s string) (*Auth, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	user, pw, ok := strings.Cut(s, ":")
	if !ok || user == "" {
		return nil, errors.New("expected user:password")
	}
	return &Auth{User: user, Password: pw}, nil
}

// WithAuth returns the proxy url with credentials in its userinfo, for
// clients like git that take http.proxy as one url. Credentials already in
// the url win, a nil auth leaves it unchanged.
func WithAuth(raw string, auth *Auth) (string, error) {
	p, err := ParseURL(raw)
	if err != nil {
		return "", err
	}
	u := &url.URL{Scheme: p.Scheme, Host: p.Host}
	switch {
	case p.Auth != nil:
		u.User = url.UserPassword(p.Auth.User, p.Auth.Password)
	case auth != nil:
		u.User = url.UserPassword(auth.User, auth.Password)
	}
	return u.String(), nil
}

// ReadAuthFile reads "user:password" from a file.
func ReadAuthFile(path string) (*Auth, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	a, err := ParseAuth(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

// Dial opens a connection to host:port through the proxy. Credentials in the
// URL win over the auth argument. insecure skips TLS verification for https.
func Dial(ctx context.Context, p Proxy, host, port string, auth *Auth, insecure bool) (net.Conn, error) {
	if p.Auth != nil {
		auth = p.Auth
	}
	dest := net.JoinHostPort(host, port)
	switch p.Scheme {
	case "socks5", "socks5h":
		var pa *proxy.Auth
		if auth != nil {
			pa = &proxy.Auth{User: auth.User, Password: auth.Password}
		}
		d, err := proxy.SOCKS5("tcp", p.Host, pa, &net.Dialer{})
		if err != nil {
			return nil, err
		}
		if p.Scheme == "socks5" {
			// Resolve locally, socks5h leaves it to the proxy.
			addrs, err := net.DefaultResolver.LookupHost(ctx, host)
			if err != nil {
				return nil, err
			}
			dest = net.JoinHostPort(addrs[0], port)
		}
		return d.(proxy.ContextDialer).DialContext(ctx, "tcp", dest)
	default:
		return dialHTTPConnect(ctx, p, dest, auth, insecure)
	}
}

func dialHTTPConnect(ctx context.Context, p Proxy, dest string, auth *Auth, insecure bool) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", p.Host)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy %s: %w", p.Host, err)
	}
	if p.Scheme == "https" {
		host, _, _ := net.SplitHostPort(p.Host)
		tc := tls.Client(conn, &tls.Config{ServerName: host, InsecureSkipVerify: insecure}) //nolint:gosec // operator opt-in
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = tc.Close()
			return nil, fmt.Errorf("tls to proxy %s: %w", p.Host, err)
		}
		conn = tc
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: dest}, Host: dest, Header: http.Header{}}
	if auth != nil {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth.User+":"+auth.Password)))
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy request: %w", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy response: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy refused CONNECT %s: %s", dest, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// Run pipes in -> conn and conn -> out until either side ends, which is how
// OpenSSH expects a ProxyCommand to behave: when ssh exits its pipes close
// and the proxy must exit too, when the remote closes ssh sees EOF.
func Run(ctx context.Context, in io.Reader, out io.Writer, conn net.Conn) error {
	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, in)
		errc <- err
	}()
	go func() {
		_, err := io.Copy(out, conn)
		errc <- err
	}()
	select {
	case err := <-errc:
		_ = conn.Close()
		if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
			return nil
		}
		return err
	case <-ctx.Done():
		_ = conn.Close()
		return ctx.Err()
	}
}
