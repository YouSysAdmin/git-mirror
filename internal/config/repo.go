package config

import "time"

// ProxyNone is the per-repo Proxy value that disables the global proxy.
const ProxyNone = "none"

// Repo is one mirrored repository.
type Repo struct {
	// Name is the mirror path relative to repos/, e.g. "github.com/golang/go".
	Name string `json:"name"`
	// URL is the upstream remote.
	URL string `json:"url"`
	// Interval overrides Settings.SyncInterval when non-zero.
	Interval Duration `json:"interval,omitempty"`
	// Branches limits which branches are mirrored (names or glob patterns
	// like "release/*"). Empty means every branch. Tags are always mirrored.
	Branches []string `json:"branches,omitempty"`
	// Enabled toggles periodic sync.
	Enabled bool `json:"enabled"`
	// Added records when the repo was registered.
	Added time.Time `json:"added"`
	// SSHKey overrides Settings.SSHKey for this repo (identity name or path).
	SSHKey string `json:"ssh_key,omitempty"`
	// Proxy overrides Settings.Proxy for this repo. The literal value
	// "none" disables the global proxy for this repo.
	Proxy string `json:"proxy,omitempty"`
}

// EffectiveInterval returns the repo interval or the global default.
func (r Repo) EffectiveInterval(s Settings) time.Duration {
	if r.Interval > 0 {
		return r.Interval.Std()
	}
	if s.SyncInterval > 0 {
		return s.SyncInterval.Std()
	}
	return DefaultSyncInterval
}

// EffectiveSSHKey returns the repo ssh key or the global default.
func (r Repo) EffectiveSSHKey(s Settings) string {
	if r.SSHKey != "" {
		return r.SSHKey
	}
	return s.SSHKey
}

// EffectiveProxy returns the repo proxy, "" when the repo disables it,
// or the global default.
func (r Repo) EffectiveProxy(s Settings) string {
	switch r.Proxy {
	case "":
		return s.Proxy
	case ProxyNone:
		return ""
	}
	return r.Proxy
}
