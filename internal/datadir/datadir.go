// Package datadir resolves the on-disk layout shared by the server and the CLI.
//
// Layout:
//
//	<data-dir>/
//	  config.json        repository list and settings (edited by CLI, read by server)
//	  state.json         per-repository sync status (written by server / local sync)
//	  lock               flock(2) file guarding config.json and state.json updates
//	  server.sock        unix socket exposing the server API
//	  tokens.json        named access tokens of the git HTTP endpoint (0600)
//	  http_token         legacy single token, migrated into tokens.json
//	  certs/             TLS material for the HTTP endpoint (self-signed cache, ACME cache)
//	  authorized_keys    public keys allowed to clone over the built-in SSH server
//	  ssh_host_ed25519_key  host key of the built-in SSH server (auto-generated)
//	  identities/<name>  private keys for fetching ssh:// upstreams (0600, plus <name>.pub)
//	  known_hosts        host keys learned while fetching with the data dir ssh setup
//	  ssh_config         optional OpenSSH client config for upstream fetches (ssh -F)
//	  proxy_auth         optional "user:password" for the built-in ssh proxy (0600)
//	  repos/<name>.git   bare mirrors
package datadir

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// maxSocketPath keeps unix socket paths under the kernel's sun_path limit
// (108 bytes on Linux, 104 on macOS) with some headroom.
const maxSocketPath = 96

// EnvVar overrides the default data directory location.
const EnvVar = "GIT_MIRROR_DIR"

// Dir is an absolute path to a data directory.
type Dir string

// Resolve picks the data directory: explicit flag value, then $GIT_MIRROR_DIR,
// then $XDG_DATA_HOME/git-mirror (or ~/.local/share/git-mirror).
func Resolve(flag string) (Dir, error) {
	p := flag
	if p == "" {
		p = os.Getenv(EnvVar)
	}
	if p == "" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve data dir: %w", err)
			}
			base = filepath.Join(home, ".local", "share")
		}
		p = filepath.Join(base, "git-mirror")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve data dir: %w", err)
	}
	return Dir(abs), nil
}

// Ensure creates the directory structure if it does not exist yet.
func (d Dir) Ensure() error {
	for _, p := range []string{string(d), d.ReposDir()} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", p, err)
		}
	}
	return nil
}

func (d Dir) String() string     { return string(d) }
func (d Dir) ConfigPath() string { return filepath.Join(string(d), "config.json") }
func (d Dir) StatePath() string  { return filepath.Join(string(d), "state.json") }
func (d Dir) LockPath() string   { return filepath.Join(string(d), "lock") }

// SocketPath is the control API socket. It lives in the data dir unless that
// path would exceed the unix socket length limit, in which case it goes into
// a private per-user directory in the system temp dir (see CheckSocketDir).
func (d Dir) SocketPath() string {
	p := filepath.Join(string(d), "server.sock")
	if len(p) <= maxSocketPath {
		return p
	}
	sum := sha256.Sum256([]byte(d))
	return filepath.Join(socketFallbackDir(), hex.EncodeToString(sum[:6])+".sock")
}

func socketFallbackDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("git-mirror-%d", os.Getuid()))
}

// CheckSocketDir verifies that the fallback socket directory in the shared temp
// dir is a real directory owned by the current user with mode 0700, so another
// local user cannot pre-create or impersonate the socket. With create set it
// makes the directory first. A socket inside the data dir needs no check.
func (d Dir) CheckSocketDir(create bool) error {
	dir := filepath.Dir(d.SocketPath())
	if dir == string(d) {
		return nil
	}
	if create {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create socket dir: %w", err)
		}
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Getuid() || fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("unsafe socket dir %s: must be a directory owned by uid %d with mode 0700", dir, os.Getuid())
	}
	return nil
}
func (d Dir) ReposDir() string { return filepath.Join(string(d), "repos") }
func (d Dir) RepoPath(name string) string {
	return filepath.Join(d.ReposDir(), filepath.FromSlash(name)+".git")
}

// AuthorizedKeysPath lists public keys allowed on the built-in SSH server.
func (d Dir) AuthorizedKeysPath() string { return filepath.Join(string(d), "authorized_keys") }

// CertsDir caches TLS certificates for the HTTP endpoint.
func (d Dir) CertsDir() string { return filepath.Join(string(d), "certs") }

// TokensPath holds the named access tokens of the git HTTP endpoint.
func (d Dir) TokensPath() string { return filepath.Join(string(d), "tokens.json") }

// HTTPTokenPath is the legacy single-token file, migrated into tokens.json.
func (d Dir) HTTPTokenPath() string { return filepath.Join(string(d), "http_token") }

// IdentitiesDir stores private keys used to fetch ssh:// upstreams.
func (d Dir) IdentitiesDir() string { return filepath.Join(string(d), "identities") }

// IdentityPath is the private key of a stored identity.
func (d Dir) IdentityPath(name string) string { return filepath.Join(d.IdentitiesDir(), name) }

// IdentityPubPath is the public key of a stored identity.
func (d Dir) IdentityPubPath(name string) string { return d.IdentityPath(name) + ".pub" }

// KnownHostsPath is the known_hosts file used with the data dir ssh setup.
func (d Dir) KnownHostsPath() string { return filepath.Join(string(d), "known_hosts") }

// SSHConfigPath is the optional OpenSSH client config for upstream fetches.
func (d Dir) SSHConfigPath() string { return filepath.Join(string(d), "ssh_config") }

// ProxyAuthPath holds "user:password" for the built-in ssh proxy (0600).
func (d Dir) ProxyAuthPath() string { return filepath.Join(string(d), "proxy_auth") }

// HostKeyPath is the built-in SSH server's host key (auto-generated).
func (d Dir) HostKeyPath() string { return filepath.Join(string(d), "ssh_host_ed25519_key") }
