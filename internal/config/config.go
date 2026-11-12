// Package config holds the shared configuration and sync state stored in the
// data directory. Both the server and the CLI read and modify these files, so
// every read-modify-write goes through an exclusive flock and an atomic rename.
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"git-mirror/internal/access"
	"git-mirror/internal/datadir"
)

// Defaults.
const (
	DefaultSyncInterval = 15 * time.Minute
	DefaultSyncTimeout  = 30 * time.Minute
	DefaultWorkers      = 2
	CurrentVersion      = 1
	// Endpoints enabled in a newly created config.json.
	DefaultListen    = ":8080"
	DefaultSSHListen = ":2222"
)

// ErrExists is returned when adding a repo whose name is already registered.
var ErrExists = errors.New("repository already exists")

// ErrNotFound is returned when a repo name is unknown.
var ErrNotFound = errors.New("repository not found")

// Config is the content of config.json.
type Config struct {
	Version  int      `json:"version"`
	Settings Settings `json:"settings"`
	Repos    []Repo   `json:"repos"`
}

// Default returns a config populated with defaults.
func Default() *Config {
	return &Config{
		Version: CurrentVersion,
		Settings: Settings{
			SyncInterval: Duration(DefaultSyncInterval),
			SyncTimeout:  Duration(DefaultSyncTimeout),
			Workers:      DefaultWorkers,
		},
		Repos: []Repo{},
	}
}

// Initial returns the config written into an empty data dir: the defaults
// plus the HTTP and SSH endpoints.
func Initial() *Config {
	c := Default()
	c.Settings.Listen = DefaultListen
	c.Settings.SSHListen = DefaultSSHListen
	return c
}

// Init creates the data dir and writes Initial() as config.json when the
// file does not exist yet. It reports whether the file was created.
func Init(d datadir.Dir) (bool, error) {
	if err := d.Ensure(); err != nil {
		return false, err
	}
	unlock, err := Lock(d)
	if err != nil {
		return false, err
	}
	defer unlock()
	if _, err := os.Stat(d.ConfigPath()); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := Save(d, Initial()); err != nil {
		return false, err
	}
	return true, nil
}

// normalize fills in zero values and sorts repos by name.
func (c *Config) normalize() {
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Settings.SyncInterval <= 0 {
		c.Settings.SyncInterval = Duration(DefaultSyncInterval)
	}
	if c.Settings.SyncTimeout <= 0 {
		c.Settings.SyncTimeout = Duration(DefaultSyncTimeout)
	}
	if c.Settings.Workers <= 0 {
		c.Settings.Workers = DefaultWorkers
	}
	if c.Repos == nil {
		c.Repos = []Repo{}
	}
	sort.Slice(c.Repos, func(i, j int) bool { return c.Repos[i].Name < c.Repos[j].Name })
}

// Find returns a pointer to the repo with the given name, or nil.
func (c *Config) Find(name string) *Repo {
	for i := range c.Repos {
		if c.Repos[i].Name == name {
			return &c.Repos[i]
		}
	}
	return nil
}

// ReposFor returns the repos a scope grants access to (all for an empty scope).
func (c *Config) ReposFor(scope []string) []Repo {
	var out []Repo
	for _, r := range c.Repos {
		if access.Allowed(scope, r.Name) {
			out = append(out, r)
		}
	}
	return out
}

// Add registers a new repo.
func (c *Config) Add(r Repo) error {
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	if err := ValidateBranches(r.Branches); err != nil {
		return err
	}
	if strings.TrimSpace(r.URL) == "" {
		return errors.New("url is required")
	}
	if c.Find(r.Name) != nil {
		return fmt.Errorf("%w: %s", ErrExists, r.Name)
	}
	if r.Added.IsZero() {
		r.Added = time.Now().UTC()
	}
	c.Repos = append(c.Repos, r)
	c.normalize()
	return nil
}

// Remove unregisters a repo.
func (c *Config) Remove(name string) error {
	for i := range c.Repos {
		if c.Repos[i].Name == name {
			c.Repos = append(c.Repos[:i], c.Repos[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Load reads config.json. A missing file yields Initial().
func Load(d datadir.Dir) (*Config, error) {
	c := Default()
	if err := readJSON(d.ConfigPath(), c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Initial(), nil
		}
		return nil, fmt.Errorf("load config: %w", err)
	}
	c.normalize()
	return c, nil
}

// Save writes config.json atomically. Callers should hold the lock.
func Save(d datadir.Dir, c *Config) error {
	c.normalize()
	if err := writeJSON(d.ConfigPath(), c); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// Update performs a locked read-modify-write of config.json.
func Update(d datadir.Dir, fn func(c *Config) error) (*Config, error) {
	if err := d.Ensure(); err != nil {
		return nil, err
	}
	unlock, err := Lock(d)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := Load(d)
	if err != nil {
		return nil, err
	}
	if err := fn(c); err != nil {
		return nil, err
	}
	if err := Save(d, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ModTime returns config.json modification time (zero if missing).
func ModTime(d datadir.Dir) time.Time {
	fi, err := os.Stat(d.ConfigPath())
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
