package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"git-mirror/internal/config"
	"git-mirror/internal/sshproxy"
	"git-mirror/internal/tlsconf"
)

// ProxyWarning returns a hint when a proxy url carries credentials, which
// would end up in the world readable config.json.
func ProxyWarning(value string) string {
	p, err := sshproxy.ParseURL(value)
	if err != nil || p.Auth == nil {
		return ""
	}
	return "the proxy url contains credentials and config.json is world readable, prefer: git-mirror config set proxy_auth user:password"
}

// SetProxyAuth stores "user:password" for the proxy in <data-dir>/proxy_auth
// (0600). An empty value removes the file.
func (a *App) SetProxyAuth(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if err := os.Remove(a.Dir.ProxyAuthPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if _, err := sshproxy.ParseAuth(value); err != nil {
		return fmt.Errorf("proxy_auth: %w", err)
	}
	if err := a.Dir.Ensure(); err != nil {
		return err
	}
	tmp := a.Dir.ProxyAuthPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(value+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.Dir.ProxyAuthPath())
}

// HasProxyAuth reports whether a proxy_auth file exists.
func (a *App) HasProxyAuth() bool {
	_, err := os.Stat(a.Dir.ProxyAuthPath())
	return err == nil
}

// HasSSHConfig reports whether <data-dir>/ssh_config exists.
func (a *App) HasSSHConfig() bool {
	_, err := os.Stat(a.Dir.SSHConfigPath())
	return err == nil
}

// SetSetting updates a global setting by key.
func (a *App) SetSetting(ctx context.Context, key, value string) error {
	_, err := config.Update(a.Dir, func(c *config.Config) error {
		switch key {
		case "interval", "sync_interval":
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return fmt.Errorf("invalid duration %q", value)
			}
			c.Settings.SyncInterval = config.Duration(d)
		case "timeout", "sync_timeout":
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return fmt.Errorf("invalid duration %q", value)
			}
			c.Settings.SyncTimeout = config.Duration(d)
		case "workers":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid workers %q", value)
			}
			c.Settings.Workers = n
		case "listen":
			c.Settings.Listen = value
		case "public_host":
			c.Settings.PublicHost = strings.TrimSpace(value)
		case "name_style":
			if value == "" {
				value = config.NameStyleRepo
			}
			if !slices.Contains(config.NameStyles, value) {
				return fmt.Errorf("name_style: expected one of %s", strings.Join(config.NameStyles, ", "))
			}
			c.Settings.NameStyle = value
		case "tls_mode":
			if value == "" {
				value = "none"
			}
			if !slices.Contains(tlsconf.Modes, value) {
				return fmt.Errorf("tls_mode: expected one of %s", strings.Join(tlsconf.Modes, ", "))
			}
			c.Settings.TLS.Mode = value
		case "tls_cert", "tls_key":
			if value != "" {
				abs, err := filepath.Abs(value)
				if err != nil {
					return err
				}
				if _, err := os.Stat(abs); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
				value = abs
			}
			if key == "tls_cert" {
				c.Settings.TLS.Cert = value
			} else {
				c.Settings.TLS.Key = value
			}
		case "tls_watch":
			b, err := parseBool(value)
			if err != nil {
				return fmt.Errorf("tls_watch: %w", err)
			}
			c.Settings.TLS.Watch = b
		case "tls_fqdn":
			c.Settings.TLS.FQDN = strings.TrimSpace(value)
		case "tls_alg":
			if value != "" && !slices.Contains(tlsconf.Algs, value) {
				return fmt.Errorf("tls_alg: expected one of %s", strings.Join(tlsconf.Algs, ", "))
			}
			c.Settings.TLS.Alg = value
		case "tls_acme_email":
			c.Settings.TLS.ACMEEmail = strings.TrimSpace(value)
		case "tls_acme_hosts":
			c.Settings.TLS.ACMEHosts = strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })
		case "tls_acme_http":
			c.Settings.TLS.ACMEHTTPAddr = strings.TrimSpace(value)
		case "ssh_listen":
			c.Settings.SSHListen = value
		case "ssh_key":
			v, err := a.normalizeSSHKey(value)
			if err != nil {
				return err
			}
			c.Settings.SSHKey = v
		case "proxy":
			value = strings.TrimSpace(value)
			if value != "" && value != config.ProxyNone {
				if _, err := sshproxy.ParseURL(value); err != nil {
					return err
				}
			} else {
				value = ""
			}
			c.Settings.Proxy = value
		case "proxy_insecure":
			b, err := parseBool(value)
			if err != nil {
				return fmt.Errorf("proxy_insecure: %w", err)
			}
			c.Settings.ProxyInsecure = b
		case "proxy_auth":
			return a.SetProxyAuth(value)
		case "ssh_allow_any_key":
			b, err := parseBool(value)
			if err != nil {
				return fmt.Errorf("ssh_allow_any_key: %w", err)
			}
			c.Settings.SSHAllowAnyKey = b
		default:
			return fmt.Errorf("unknown setting %q (interval, timeout, workers, name_style, listen, public_host, tls_mode, tls_cert, tls_key, tls_watch, tls_fqdn, tls_alg, tls_acme_email, tls_acme_hosts, tls_acme_http, ssh_listen, ssh_allow_any_key, ssh_key, proxy, proxy_auth, proxy_insecure)", key)
		}
		if strings.HasPrefix(key, "tls_") {
			return tlsconf.Validate(c.Settings, a.Dir)
		}
		return nil
	})
	if err == nil {
		a.notifyReload(ctx)
	}
	return err
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "1", "on":
		return true, nil
	case "false", "no", "0", "off", "":
		return false, nil
	}
	return false, fmt.Errorf("expected true/false, got %q", v)
}
