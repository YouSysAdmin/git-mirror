package sshproxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseURL(t *testing.T) {
	cases := map[string]Proxy{
		"http://proxy:8080":   {Scheme: "http", Host: "proxy:8080"},
		"proxy.internal":      {Scheme: "http", Host: "proxy.internal:80"},
		"https://p":           {Scheme: "https", Host: "p:443"},
		"socks5://p":          {Scheme: "socks5", Host: "p:1080"},
		"socks5h://p:9":       {Scheme: "socks5h", Host: "p:9"},
		"http://u:pw@p:3128/": {Scheme: "http", Host: "p:3128", Auth: &Auth{User: "u", Password: "pw"}},
		"HTTP://[::1]:8080":   {Scheme: "http", Host: "[::1]:8080"},
	}
	for in, want := range cases {
		got, err := ParseURL(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got.Scheme != want.Scheme || got.Host != want.Host || (got.Auth == nil) != (want.Auth == nil) {
			t.Errorf("%s: got %+v want %+v", in, got, want)
		}
		if want.Auth != nil && *got.Auth != *want.Auth {
			t.Errorf("%s: auth %+v", in, got.Auth)
		}
	}
	for _, bad := range []string{"", "ftp://p", "http://", "http://p/path", "socks4://p"} {
		if _, err := ParseURL(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if a, err := ParseAuth("user:pa:ss"); err != nil || a.User != "user" || a.Password != "pa:ss" {
		t.Fatalf("auth: %+v %v", a, err)
	}
	if _, err := ParseAuth("nocolon"); err == nil {
		t.Fatal("expected error")
	}
	if a, err := ParseAuth(" "); err != nil || a != nil {
		t.Fatal("empty auth must be nil")
	}
	withAuth := map[string]string{
		"http://proxy:8080": "http://u:p%40w@proxy:8080",
		"socks5h://proxy":   "socks5h://u:p%40w@proxy:1080",
		"https://x:y@proxy": "https://x:y@proxy:443", // credentials in the url win
	}
	for in, want := range withAuth {
		got, err := WithAuth(in, &Auth{User: "u", Password: "p@w"})
		if err != nil || got != want {
			t.Errorf("WithAuth(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	if got, _ := WithAuth("http://proxy", nil); got != "http://proxy:80" {
		t.Errorf("WithAuth without auth = %q", got)
	}
}

// echoServer accepts one connection and echoes what it receives.
func echoServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(c, c)
				_ = c.Close()
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func TestHTTPConnect(t *testing.T) {
	echo := echoServer(t)
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		seenAuth = r.Header.Get("Proxy-Authorization")
		if seenAuth == "" {
			w.Header().Set("Proxy-Authenticate", "Basic")
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijack")
			return
		}
		cl, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = cl.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { _, _ = io.Copy(up, cl) }()
		_, _ = io.Copy(cl, up)
		_ = cl.Close()
		_ = up.Close()
	}))
	defer srv.Close()

	p, err := ParseURL(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(echo.Addr().String())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if _, err := Dial(ctx, p, host, port, nil, false); err == nil || !strings.Contains(err.Error(), "407") {
		t.Fatalf("expected 407, got %v", err)
	}
	conn, err := Dial(ctx, p, host, port, &Auth{User: "u", Password: "p"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if seenAuth != "Basic dTpw" {
		t.Fatalf("auth header %q", seenAuth)
	}
	if got := roundTrip(t, ctx, conn, "hello proxy"); got != "hello proxy" {
		t.Fatalf("echo: %q", got)
	}
}

// roundTrip runs the pump like ssh would: write msg, wait for the echo, then
// close stdin (ssh exited) and expect Run to return.
func roundTrip(t *testing.T, ctx context.Context, conn net.Conn, msg string) string {
	t.Helper()
	pr, pw := io.Pipe()
	or, ow := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, pr, ow, conn) }()
	if _, err := pw.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(or, buf); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

// socks5Server is a minimal SOCKS5 CONNECT server with optional user/pass auth.
func socks5Server(t *testing.T, user, pass string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 512)
				n, _ := io.ReadAtLeast(c, buf, 2)
				nmethods := int(buf[1])
				for n < 2+nmethods {
					m, _ := c.Read(buf[n:])
					n += m
				}
				if user != "" {
					_, _ = c.Write([]byte{5, 2})
					_, _ = io.ReadFull(c, buf[:2])
					ul := int(buf[1])
					_, _ = io.ReadFull(c, buf[:ul])
					gotUser := string(buf[:ul])
					_, _ = io.ReadFull(c, buf[:1])
					pl := int(buf[0])
					_, _ = io.ReadFull(c, buf[:pl])
					if gotUser != user || string(buf[:pl]) != pass {
						_, _ = c.Write([]byte{1, 1})
						return
					}
					_, _ = c.Write([]byte{1, 0})
				} else {
					_, _ = c.Write([]byte{5, 0})
				}
				_, _ = io.ReadFull(c, buf[:4])
				var host string
				switch buf[3] {
				case 1:
					_, _ = io.ReadFull(c, buf[:4])
					host = net.IP(buf[:4]).String()
				case 3:
					_, _ = io.ReadFull(c, buf[:1])
					l := int(buf[0])
					_, _ = io.ReadFull(c, buf[:l])
					host = string(buf[:l])
				default:
					return
				}
				_, _ = io.ReadFull(c, buf[:2])
				port := binary.BigEndian.Uint16(buf[:2])
				up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
				if err != nil {
					_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
				_ = up.Close()
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func TestSOCKS5(t *testing.T) {
	echo := echoServer(t)
	host, port, _ := net.SplitHostPort(echo.Addr().String())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	for _, scheme := range []string{"socks5", "socks5h"} {
		ln := socks5Server(t, "", "")
		p, _ := ParseURL(scheme + "://" + ln.Addr().String())
		conn, err := Dial(ctx, p, host, port, nil, false)
		if err != nil {
			t.Fatalf("%s: %v", scheme, err)
		}
		if got := roundTrip(t, ctx, conn, "ping"); got != "ping" {
			t.Fatalf("%s: %q", scheme, got)
		}
	}
	ln := socks5Server(t, "alice", "s3cret")
	p, _ := ParseURL("socks5://" + ln.Addr().String())
	if _, err := Dial(ctx, p, host, port, &Auth{User: "alice", Password: "wrong"}, false); err == nil {
		t.Fatal("wrong password must fail")
	}
	conn, err := Dial(ctx, p, host, port, &Auth{User: "alice", Password: "s3cret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestRunStopsWhenRemoteCloses(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		_, _ = b.Write([]byte("bye"))
		_ = b.Close()
	}()
	var out bytes.Buffer
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := Run(ctx, pr, &out, a); err != nil {
		t.Fatal(err)
	}
	if out.String() != "bye" {
		t.Fatalf("got %q", out.String())
	}
}
