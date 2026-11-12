package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git-mirror/internal/config"
	"git-mirror/internal/identity"
	"git-mirror/internal/mirror"
	"git-mirror/internal/sshproxy"
)

// AddOptions for Add.
type AddOptions struct {
	Name     string // optional, derived from URL when empty
	Interval time.Duration
	Branches []string // nil = all branches
	Disabled bool
	SSHKey   string // identity name or path, "" = global setting
	Proxy    string // proxy url, "none" or "" = global setting
}

// EditOptions lists the fields Edit may change. Nil pointers keep the value.
type EditOptions struct {
	URL      *string
	Interval *time.Duration // <= 0 resets to the global default
	Branches *[]string      // empty slice = all branches
	SSHKey   *string        // identity name or path, "" or "default" = global setting
	Proxy    *string        // proxy url, "none" disables, "" or "default" = global setting
}

// Add registers a repository and returns it. Without an explicit name the
// name is derived from the URL according to settings.name_style.
func (a *App) Add(ctx context.Context, url string, o AddOptions) (config.Repo, error) {
	url = strings.TrimSpace(url)
	name := strings.Trim(strings.TrimSpace(o.Name), "/")
	full := ""
	if name == "" {
		var err error
		if full, err = mirror.NameFromURL(url); err != nil {
			return config.Repo{}, err
		}
	}
	sshKey, err := a.normalizeSSHKey(o.SSHKey)
	if err != nil {
		return config.Repo{}, err
	}
	proxy, err := normalizeRepoProxy(o.Proxy)
	if err != nil {
		return config.Repo{}, err
	}
	r := config.Repo{
		URL:      url,
		Interval: config.Duration(o.Interval),
		Branches: o.Branches,
		Enabled:  !o.Disabled,
		SSHKey:   sshKey,
		Proxy:    proxy,
	}
	_, err = config.Update(a.Dir, func(c *config.Config) error {
		r.Name = name
		if r.Name == "" {
			r.Name = config.ApplyNameStyle(full, c.Settings.NameStyle)
		}
		if err := c.Add(r); err != nil {
			if errors.Is(err, config.ErrExists) && name == "" {
				return fmt.Errorf("%w (pick another with --name, e.g. %s)", err, config.ApplyNameStyle(full, config.NameStyleOwner))
			}
			return err
		}
		return nil
	})
	if err != nil {
		return config.Repo{}, err
	}
	a.notifyReload(ctx)
	return r, nil
}

// Remove unregisters a repository. With purge, it also deletes the mirror on disk.
func (a *App) Remove(ctx context.Context, name string, purge bool) error {
	_, err := config.Update(a.Dir, func(c *config.Config) error { return c.Remove(name) })
	if err != nil {
		return err
	}
	_, _ = config.UpdateState(a.Dir, func(s *config.State) error {
		delete(s.Repos, name)
		return nil
	})
	a.notifyReload(ctx)
	if purge {
		if err := mirror.Remove(a.Dir.ReposDir(), a.Dir.RepoPath(name)); err != nil {
			return fmt.Errorf("remove mirror files: %w", err)
		}
	}
	return nil
}

// SetEnabled toggles periodic sync for a repo.
func (a *App) SetEnabled(ctx context.Context, name string, enabled bool) error {
	_, err := config.Update(a.Dir, func(c *config.Config) error {
		r := c.Find(name)
		if r == nil {
			return fmt.Errorf("%w: %s", config.ErrNotFound, name)
		}
		r.Enabled = enabled
		return nil
	})
	if err == nil {
		a.notifyReload(ctx)
	}
	return err
}

// Edit changes url, interval, branch filter, ssh key and/or proxy of a repo.
func (a *App) Edit(ctx context.Context, name string, o EditOptions) error {
	if o.Branches != nil {
		if err := config.ValidateBranches(*o.Branches); err != nil {
			return err
		}
	}
	var sshKey, proxy string
	if o.SSHKey != nil {
		var err error
		if sshKey, err = a.normalizeSSHKey(*o.SSHKey); err != nil {
			return err
		}
	}
	if o.Proxy != nil {
		var err error
		if proxy, err = normalizeRepoProxy(*o.Proxy); err != nil {
			return err
		}
	}
	_, err := config.Update(a.Dir, func(c *config.Config) error {
		r := c.Find(name)
		if r == nil {
			return fmt.Errorf("%w: %s", config.ErrNotFound, name)
		}
		if o.URL != nil && strings.TrimSpace(*o.URL) != "" {
			r.URL = strings.TrimSpace(*o.URL)
		}
		if o.Interval != nil {
			r.Interval = config.Duration(max(*o.Interval, 0))
		}
		if o.Branches != nil {
			r.Branches = *o.Branches
		}
		if o.SSHKey != nil {
			r.SSHKey = sshKey
		}
		if o.Proxy != nil {
			r.Proxy = proxy
		}
		return nil
	})
	if err == nil {
		a.notifyReload(ctx)
	}
	return err
}

// normalizeSSHKey validates an ssh_key value: "" or "default" clears it, an
// identity name must exist in the store, anything else is a path that is
// stored absolute (the server may run with a different working dir).
func (a *App) normalizeSSHKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == identity.DefaultKeyword {
		return "", nil
	}
	if identity.IsName(value) {
		if _, err := identity.Get(a.Dir, value); err != nil {
			return "", fmt.Errorf("%w (list with: git-mirror identity ls)", err)
		}
		return value, nil
	}
	if strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, value[2:])
		}
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("ssh_key: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("ssh_key: %w", err)
	}
	return abs, nil
}

// normalizeRepoProxy validates a per-repo proxy value.
func normalizeRepoProxy(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case "", identity.DefaultKeyword:
		return "", nil
	case config.ProxyNone:
		return config.ProxyNone, nil
	}
	if _, err := sshproxy.ParseURL(value); err != nil {
		return "", err
	}
	return value, nil
}
