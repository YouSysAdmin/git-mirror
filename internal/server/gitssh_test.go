package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"git-mirror/internal/authkeys"
	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

func TestSSHGrantAllows(t *testing.T) {
	if !(sshGrant{allowAll: true}).allows("anything") {
		t.Fatal("allow-any must allow")
	}
	if !(sshGrant{}).allows("anything") {
		t.Fatal("empty scope must allow")
	}
	g := sshGrant{repos: []string{"team/*", "go"}}
	if !g.allows("team/x") || !g.allows("go") || g.allows("other") {
		t.Fatal("scoped grant")
	}
}

type testKey struct {
	signer ssh.Signer
	line   string
}

func newTestKey(t *testing.T, comment string) testKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{signer: s, line: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey()))) + " " + comment}
}

func (k testKey) fp() string { return ssh.FingerprintSHA256(k.signer.PublicKey()) }

// startSSH runs the built-in SSH server on a free loopback port with mirrors go and other.
func startSSH(t *testing.T) (datadir.Dir, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := datadir.Dir(t.TempDir())
	if _, err := config.Update(dir, func(c *config.Config) error {
		if err := c.Add(config.Repo{Name: "go", URL: "u", Enabled: true}); err != nil {
			return err
		}
		return c.Add(config.Repo{Name: "other", URL: "u", Enabled: true})
	}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"go", "other"} {
		if out, err := exec.Command("git", "init", "-q", "--bare", dir.RepoPath(n)).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	s := New(dir, Options{}, nil)
	if err := s.loadConfig(true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = s.serveGitSSH(ctx, addr) }()
	for range 50 {
		if c, err := net.Dial("tcp", addr); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return dir, addr
}

func dialSSH(addr string, k testKey) (*ssh.Client, error) {
	return ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "git",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(k.signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

// uploadPack opens a new session on c and reports whether the mirror was served.
func uploadPack(t *testing.T, c *ssh.Client, repo string) bool {
	t.Helper()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer func() { _ = sess.Close() }()
	var out, errOut bytes.Buffer
	sess.Stdout, sess.Stderr = &out, &errOut
	sess.Stdin = strings.NewReader("0000")
	_ = sess.Run("git-upload-pack '" + repo + ".git'")
	return out.Len() > 0 && !strings.Contains(errOut.String(), "git-mirror:")
}

func TestSSHRevocationAppliesToOpenConnection(t *testing.T) {
	dir, addr := startSSH(t)

	a := newTestKey(t, "alice@test")
	if _, _, _, err := authkeys.Add(dir, []string{a.line}, nil); err != nil {
		t.Fatal(err)
	}
	ca, err := dialSSH(addr, a)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ca.Close() }()
	if !uploadPack(t, ca, "go") {
		t.Fatal("authorized key must be served")
	}
	if _, err := authkeys.Remove(dir, []string{a.fp()}); err != nil {
		t.Fatal(err)
	}
	if uploadPack(t, ca, "go") {
		t.Fatal("removed key must not be served on its open connection")
	}

	b := newTestKey(t, "bob@test")
	if _, _, _, err := authkeys.Add(dir, []string{b.line}, nil); err != nil {
		t.Fatal(err)
	}
	cb, err := dialSSH(addr, b)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cb.Close() }()
	if _, err := authkeys.SetRepos(dir, b.fp(), []string{"other"}); err != nil {
		t.Fatal(err)
	}
	if uploadPack(t, cb, "go") {
		t.Fatal("narrowed key must lose the old scope on its open connection")
	}
	if !uploadPack(t, cb, "other") {
		t.Fatal("narrowed key must keep the new scope")
	}
}
