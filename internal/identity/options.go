package identity

import (
	"fmt"
	"os"
	"strings"

	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
	"git-mirror/internal/mirror"
	"git-mirror/internal/sshproxy"
)

// executable is overridable in tests.
var executable = os.Executable

// IsName reports whether value looks like an identity name rather than a path.
func IsName(value string) bool {
	return value != "" && !strings.ContainsAny(value, "/\\") && !strings.HasPrefix(value, "~")
}

// Resolve turns a settings value into a private key path: "" keeps the plain
// ssh of the user, an identity name maps into the store (with the data dir
// known_hosts), anything else is a legacy path that must exist.
func Resolve(d datadir.Dir, value string) (keyPath, knownHosts string, err error) {
	switch {
	case value == "":
		return "", "", nil
	case IsName(value):
		if _, err := Get(d, value); err != nil {
			return "", "", err
		}
		return d.IdentityPath(value), d.KnownHostsPath(), nil
	default:
		if _, err := os.Stat(value); err != nil {
			return "", "", fmt.Errorf("ssh key: %w", err)
		}
		return value, "", nil
	}
}

// SyncOptions builds the mirror options for a repo: key, known_hosts,
// ssh_config and proxy (for ssh and for http upstreams) from the data dir
// plus the branch filter.
func SyncOptions(d datadir.Dir, r config.Repo, s config.Settings) (mirror.Options, error) {
	o := mirror.Options{Branches: r.Branches}
	key, kh, err := Resolve(d, r.EffectiveSSHKey(s))
	if err != nil {
		return o, err
	}
	o.SSHKey, o.KnownHosts = key, kh
	if _, err := os.Stat(d.SSHConfigPath()); err == nil {
		o.ConfigFile = d.SSHConfigPath()
		o.KnownHosts = d.KnownHostsPath()
	}
	if proxy := r.EffectiveProxy(s); proxy != "" {
		cmd, err := ProxyCommand(d, proxy, s.ProxyInsecure)
		if err != nil {
			return o, err
		}
		o.ProxyCommand = cmd
		o.KnownHosts = d.KnownHostsPath()
		var auth *sshproxy.Auth
		if _, err := os.Stat(d.ProxyAuthPath()); err == nil {
			if auth, err = sshproxy.ReadAuthFile(d.ProxyAuthPath()); err != nil {
				return o, err
			}
		}
		if o.HTTPProxy, err = sshproxy.WithAuth(proxy, auth); err != nil {
			return o, err
		}
		o.HTTPProxyInsecure = s.ProxyInsecure
	}
	return o, nil
}

// ProxyCommand renders the ssh ProxyCommand running the built-in proxy:
// "<git-mirror> ssh-proxy [--insecure] <url> %h %p [<data-dir>/proxy_auth]".
// Every argument is quoted for the /bin/sh ssh uses to run it.
func ProxyCommand(d datadir.Dir, proxyURL string, insecure bool) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", fmt.Errorf("locate git-mirror binary: %w", err)
	}
	parts := []string{mirror.ShellQuote(exe), "ssh-proxy"}
	if insecure {
		parts = append(parts, "--insecure")
	}
	parts = append(parts, mirror.ShellQuote(proxyURL), "%h", "%p")
	if _, err := os.Stat(d.ProxyAuthPath()); err == nil {
		parts = append(parts, mirror.ShellQuote(d.ProxyAuthPath()))
	}
	return strings.Join(parts, " "), nil
}
