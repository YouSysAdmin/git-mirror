package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"git-mirror/internal/datadir"
)

// Status of the last sync attempt.
type Status string

const (
	StatusNever   Status = "never"   // never synced
	StatusSyncing Status = "syncing" // in progress
	StatusOK      Status = "ok"
	StatusError   Status = "error"
)

// RepoState is the sync status of one repo.
type RepoState struct {
	Status      Status        `json:"status"`
	LastAttempt time.Time     `json:"last_attempt,omitzero"`
	LastSuccess time.Time     `json:"last_success,omitzero"`
	LastError   string        `json:"last_error,omitempty"`
	Duration    time.Duration `json:"duration_ns,omitempty"`
	Head        string        `json:"head,omitempty"`
	// SizeBytes is the on-disk size of the mirror after the last sync.
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

// State is the content of state.json.
type State struct {
	Repos map[string]RepoState `json:"repos"`
}

// Get returns the state for a repo (zero value with StatusNever if unknown).
func (s *State) Get(name string) RepoState {
	if s == nil || s.Repos == nil {
		return RepoState{Status: StatusNever}
	}
	st, ok := s.Repos[name]
	if !ok {
		return RepoState{Status: StatusNever}
	}
	return st
}

// LoadState reads state.json. Missing file yields an empty state.
func LoadState(d datadir.Dir) (*State, error) {
	s := &State{Repos: map[string]RepoState{}}
	if err := readJSON(d.StatePath(), s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("load state: %w", err)
	}
	if s.Repos == nil {
		s.Repos = map[string]RepoState{}
	}
	return s, nil
}

// SaveState writes state.json atomically. Callers should hold the lock.
func SaveState(d datadir.Dir, s *State) error {
	if err := writeJSON(d.StatePath(), s); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

// UpdateState performs a locked read-modify-write of state.json.
func UpdateState(d datadir.Dir, fn func(s *State) error) (*State, error) {
	if err := d.Ensure(); err != nil {
		return nil, err
	}
	unlock, err := Lock(d)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := LoadState(d)
	if err != nil {
		return nil, err
	}
	if err := fn(s); err != nil {
		return nil, err
	}
	if err := SaveState(d, s); err != nil {
		return nil, err
	}
	return s, nil
}

// PruneState drops state entries for repos no longer in the config.
func PruneState(d datadir.Dir, c *Config) error {
	_, err := UpdateState(d, func(s *State) error {
		for name := range s.Repos {
			if c.Find(name) == nil {
				delete(s.Repos, name)
			}
		}
		return nil
	})
	return err
}

// MarkSyncing records that a sync attempt started.
func MarkSyncing(d datadir.Dir, name string, started time.Time) error {
	_, err := UpdateState(d, func(s *State) error {
		rs := s.Get(name)
		rs.Status = StatusSyncing
		rs.LastAttempt = started
		s.Repos[name] = rs
		return nil
	})
	return err
}

// MarkResult records the outcome of a sync attempt started at `started`.
func MarkResult(d datadir.Dir, name string, started time.Time, head string, size int64, syncErr error) error {
	_, err := UpdateState(d, func(s *State) error {
		rs := s.Get(name)
		rs.LastAttempt = started
		rs.Duration = time.Since(started)
		if syncErr != nil {
			rs.Status = StatusError
			rs.LastError = syncErr.Error()
		} else {
			rs.Status = StatusOK
			rs.LastError = ""
			rs.LastSuccess = time.Now()
			rs.Head = head
			rs.SizeBytes = size
		}
		s.Repos[name] = rs
		return nil
	})
	return err
}
