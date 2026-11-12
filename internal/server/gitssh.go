package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	gssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"

	"git-mirror/internal/access"
	"git-mirror/internal/authkeys"
	"git-mirror/internal/config"
	"git-mirror/internal/identity"
)

// grantKey stores the sshGrant in the connection context.
type grantKey struct{}

// sshGrant is what an authenticated key may do.
type sshGrant struct {
	allowAll    bool     // ssh_allow_any_key: every mirror
	repos       []string // scope of the matched key, empty = every mirror
	key         []byte   // wire format of the matched key
	fingerprint string
}

// serveGitSSH runs a minimal SSH server that only executes git-upload-pack /
// git-upload-archive on mirrors, so clients can:
//
//	git clone ssh://git@<host>:<port>/<name>.git
//
// Clients authenticate with public keys listed in <data-dir>/authorized_keys
// (OpenSSH format), or any key when settings.ssh_allow_any_key is true. The
// scope of the matched key decides which mirrors it may clone.
func (s *Server) serveGitSSH(ctx context.Context, addr string) error {
	signer, err := loadOrCreateHostKey(s.dir.HostKeyPath())
	if err != nil {
		return fmt.Errorf("host key: %w", err)
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return err
	}

	srv := &gssh.Server{
		Version:     "git-mirror",
		IdleTimeout: 10 * time.Minute,
		Handler:     func(sess gssh.Session) { s.handleSSHSession(sess, gitBin) },
		PublicKeyHandler: func(c gssh.Context, key gssh.PublicKey) bool {
			g, ok := s.sshGrantFor(key)
			if !ok {
				s.log.Warn("ssh auth rejected", "user", c.User(), "remote", c.RemoteAddr().String(),
					"fingerprint", ssh.FingerprintSHA256(key))
				return false
			}
			c.SetValue(grantKey{}, g)
			return true
		},
		// git never needs a pty. Refusing it blocks interactive shells.
		PtyCallback: func(gssh.Context, gssh.Pty) bool { return false },
	}
	srv.AddHostKey(signer)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()
	s.log.Info("git ssh endpoint listening", "addr", ln.Addr().String(),
		"host_key", ssh.FingerprintSHA256(signer.PublicKey()),
		"authorized_keys", s.dir.AuthorizedKeysPath())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, gssh.ErrServerClosed) {
		return err
	}
	return nil
}

// allows reports whether the grant covers the mirror.
func (g sshGrant) allows(name string) bool {
	return g.allowAll || access.Allowed(g.repos, name)
}

// sshGrantFor checks the key against authorized_keys (re-read on every
// attempt so `git-mirror keys add` takes effect immediately).
func (s *Server) sshGrantFor(key gssh.PublicKey) (sshGrant, bool) {
	fp := ssh.FingerprintSHA256(key)
	if s.config().Settings.SSHAllowAnyKey {
		return sshGrant{allowAll: true, key: key.Marshal(), fingerprint: fp}, true
	}
	keys, err := authkeys.Load(s.dir)
	if err != nil {
		s.log.Error("read authorized_keys", "err", err)
		return sshGrant{}, false
	}
	k := authkeys.Find(keys, key)
	if k == nil {
		return sshGrant{}, false
	}
	// Fail closed on restrictions this server cannot enforce (from=, expiry-time=, ...).
	if u := k.Unenforced(); len(u) > 0 {
		s.log.Warn("ssh key refused, unsupported authorized_keys options", "fingerprint", fp, "options", strings.Join(u, ","))
		return sshGrant{}, false
	}
	return sshGrant{repos: k.Repos, key: key.Marshal(), fingerprint: fp}, true
}

// handleSSHSession executes exactly one read-only git command.
func (s *Server) handleSSHSession(sess gssh.Session, gitBin string) {
	args := sess.Command()
	fail := func(msg string) {
		_, _ = fmt.Fprintln(sess.Stderr(), "git-mirror: "+msg)
		_ = sess.Exit(1)
	}
	if len(args) == 0 {
		fail("interactive shell is not available, this server only serves git clone/fetch")
		return
	}
	// Accept both "git-upload-pack <path>" and "git upload-pack <path>".
	cmd := args[0]
	rest := args[1:]
	if cmd == "git" && len(rest) > 0 {
		cmd = "git-" + rest[0]
		rest = rest[1:]
	}
	switch cmd {
	case "git-upload-pack", "git-upload-archive":
	case "git-receive-pack":
		fail("read-only mirror, push is not allowed")
		return
	default:
		fail("command not allowed: " + sess.RawCommand())
		return
	}
	// The path is the last argument (git may pass options like --strict first).
	if len(rest) == 0 {
		fail("repository path missing")
		return
	}
	rawPath := rest[len(rest)-1]
	name := strings.TrimSuffix(strings.Trim(rawPath, "/"), ".git")
	name = strings.TrimSuffix(name, "/")
	if err := config.ValidateName(name); err != nil {
		fail("invalid repository path")
		return
	}
	if s.config().Find(name) == nil {
		fail("repository not found: " + name)
		return
	}
	g, ok := sess.Context().Value(grantKey{}).(sshGrant)
	if !ok || sess.PublicKey() == nil || !bytes.Equal(g.key, sess.PublicKey().Marshal()) {
		fail("access denied")
		return
	}
	// Re-check the key on every session, so a removed or narrowed key loses
	// access on connections that are already open.
	g, ok = s.sshGrantFor(sess.PublicKey())
	if !ok {
		s.log.Warn("ssh key revoked on open connection", "fingerprint", ssh.FingerprintSHA256(sess.PublicKey()), "remote", sess.RemoteAddr().String())
		fail("access denied")
		return
	}
	if !g.allows(name) {
		s.log.Warn("ssh access denied", "repo", name, "fingerprint", g.fingerprint, "remote", sess.RemoteAddr().String())
		fail("key has no access to " + name)
		return
	}
	repoDir := s.dir.RepoPath(name)
	if _, err := os.Stat(repoDir); err != nil {
		fail("repository not synced yet: " + name)
		return
	}

	subcmd := strings.TrimPrefix(cmd, "git-")
	gitArgs := []string{subcmd}
	if subcmd == "upload-pack" {
		gitArgs = append(gitArgs, "--strict") // upload-archive has no such flag
	}
	gitArgs = append(gitArgs, repoDir)

	ctx, cancel := context.WithCancel(sess.Context())
	defer cancel()
	c := exec.CommandContext(ctx, gitBin, gitArgs...)
	c.Env = append(os.Environ(), "LC_ALL=C")
	for _, kv := range sess.Environ() {
		if strings.HasPrefix(kv, "GIT_PROTOCOL=") { // protocol v2 negotiation
			c.Env = append(c.Env, kv)
		}
	}
	c.Stdout = sess
	c.Stderr = sess.Stderr()
	stdin, err := c.StdinPipe()
	if err != nil {
		fail("internal error")
		return
	}
	started := time.Now()
	if err := c.Start(); err != nil {
		fail("cannot start git: " + err.Error())
		return
	}
	go func() {
		_, _ = io.Copy(stdin, sess)
		_ = stdin.Close()
	}()
	err = c.Wait()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			code = ee.ExitCode()
		}
	}
	s.log.Info("ssh "+subcmd, "repo", name, "user", sess.User(), "fingerprint", g.fingerprint,
		"remote", sess.RemoteAddr().String(), "exit", code, "took", time.Since(started).Round(time.Millisecond))
	_ = sess.Exit(code)
}

// loadOrCreateHostKey reads the ed25519 host key or generates a new one.
func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pemBytes, _, err := identity.GenerateED25519PEM("git-mirror host key")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(pemBytes)
}
