// Package api defines the JSON types exchanged between the server and the CLI/TUI.
package api

import (
	"time"

	"git-mirror/internal/config"
)

type Ping struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

type ServerInfo struct {
	Running bool      `json:"running"`
	PID     int       `json:"pid,omitempty"`
	Version string    `json:"version,omitempty"`
	Started time.Time `json:"started,omitzero"`
	DataDir string    `json:"data_dir"`
	Queued  int       `json:"queued"`
	// GitHTTP is the effective git smart-HTTP listen address ("" = disabled).
	GitHTTP string `json:"git_http,omitempty"`
	// GitHTTPS reports whether GitHTTP serves TLS.
	GitHTTPS bool `json:"git_https,omitempty"`
	// GitSSH is the effective built-in SSH listen address ("" = disabled).
	GitSSH string `json:"git_ssh,omitempty"`
}

type RepoStatus struct {
	Repo     config.Repo      `json:"repo"`
	State    config.RepoState `json:"state"`
	InFlight bool             `json:"in_flight"`
	NextDue  time.Time        `json:"next_due,omitzero"`
}

type Status struct {
	Server   ServerInfo      `json:"server"`
	Settings config.Settings `json:"settings"`
	Repos    []RepoStatus    `json:"repos"`
}

type SyncResponse struct {
	Queued  []string `json:"queued,omitempty"`
	Skipped []string `json:"skipped,omitempty"` // already running
	Unknown []string `json:"unknown,omitempty"`
}

type Error struct {
	Error string `json:"error"`
}
