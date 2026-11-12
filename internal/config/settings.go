package config

import "strings"

// Name styles accepted in Settings.NameStyle.
const (
	NameStyleRepo  = "repo"
	NameStyleOwner = "owner"
	NameStyleFull  = "full"
)

// NameStyles lists the accepted values.
var NameStyles = []string{NameStyleRepo, NameStyleOwner, NameStyleFull}

// Settings are server-wide knobs.
type Settings struct {
	// SyncInterval is the default period between syncs of each repository.
	SyncInterval Duration `json:"sync_interval"`
	// SyncTimeout bounds a single clone/fetch.
	SyncTimeout Duration `json:"sync_timeout"`
	// Workers is the number of repositories synced concurrently.
	Workers int `json:"workers"`
	// Listen, when set (e.g. ":8080"), makes the server expose the mirrors
	// over the Git smart HTTP protocol (read-only). Access requires one of
	// the tokens from <data-dir>/tokens.json (HTTP basic password or bearer).
	Listen string `json:"listen,omitempty"`
	// PublicHost is the host name clients use to reach this server. It is used
	// to render clone URLs and as the default certificate name (defaults to
	// the machine host name).
	PublicHost string `json:"public_host,omitempty"`
	// TLS configures HTTPS for the git HTTP endpoint.
	TLS TLSSettings `json:"tls,omitzero"`
	// NameStyle controls how mirror names are derived from upstream URLs when
	// no explicit name is given: "repo" (default, "go"), "owner" ("golang/go")
	// or "full" ("github.com/golang/go").
	NameStyle string `json:"name_style,omitempty"`
	// SSHListen, when set (e.g. ":2222"), starts the built-in read-only SSH
	// server: git clone ssh://git@host:2222/<name>.git
	SSHListen string `json:"ssh_listen,omitempty"`
	// SSHAllowAnyKey accepts any public key on the built-in SSH server instead
	// of checking <data-dir>/authorized_keys (for fully trusted networks).
	SSHAllowAnyKey bool `json:"ssh_allow_any_key,omitempty"`
	// SSHKey is the default private key for fetching upstreams over SSH: the
	// name of an identity in <data-dir>/identities or an absolute path.
	// Repos may override it. It is passed to git as GIT_SSH_COMMAND="ssh -i ...".
	SSHKey string `json:"ssh_key,omitempty"`
	// Proxy, when set, routes upstream fetches through a proxy (http://,
	// https://, socks5:// or socks5h:// URL): git gets it as http.proxy for
	// http(s) upstreams and ssh runs the built-in "git-mirror ssh-proxy" as
	// ProxyCommand. Repos may override it.
	Proxy string `json:"proxy,omitempty"`
	// ProxyInsecure skips certificate verification for https:// proxies.
	ProxyInsecure bool `json:"proxy_insecure,omitzero"`
}

// TLSSettings selects how the HTTP endpoint gets its certificate
// (see github.com/yousysadmin/go-tlsutils).
type TLSSettings struct {
	// Mode: "none" (plain HTTP, default), "manual" (Cert/Key files), "self"
	// (self-signed, cached in <data-dir>/certs) or "acme" (Let's Encrypt).
	Mode string `json:"mode,omitempty"`
	// Cert and Key are PEM files for manual mode. Watch reloads them on change.
	Cert  string `json:"cert,omitempty"`
	Key   string `json:"key,omitempty"`
	Watch bool   `json:"watch,omitempty"`
	// FQDN is the name in the self-signed certificate (default: public_host).
	FQDN string `json:"fqdn,omitempty"`
	// Alg is the self-signed key algorithm: rsa, ecdsa (default) or ed25519.
	Alg string `json:"alg,omitempty"`
	// ACME settings: contact e-mail, allowed host names and the HTTP-01
	// challenge listener (must be reachable as port 80, default ":80").
	ACMEEmail    string   `json:"acme_email,omitempty"`
	ACMEHosts    []string `json:"acme_hosts,omitempty"`
	ACMEHTTPAddr string   `json:"acme_http_addr,omitempty"`
}

// ApplyNameStyle shortens a full "host/owner/repo" name according to style.
// Unknown or empty styles behave like NameStyleRepo.
func ApplyNameStyle(full, style string) string {
	segs := strings.Split(strings.Trim(full, "/"), "/")
	switch style {
	case NameStyleFull:
		return full
	case NameStyleOwner:
		if len(segs) >= 3 {
			return strings.Join(segs[len(segs)-2:], "/")
		}
		if len(segs) == 2 {
			return segs[1] // "host/repo" has no owner part
		}
		return full
	default:
		return segs[len(segs)-1]
	}
}

// Enabled reports whether the endpoint serves HTTPS.
func (t TLSSettings) Enabled() bool { return t.Mode != "" && t.Mode != "none" }
