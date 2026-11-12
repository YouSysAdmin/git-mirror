// Package app implements the user-facing operations shared by the CLI and the
// TUI. Mutations edit config.json under lock. Sync goes through the server when
// it is running and falls back to running git locally otherwise.
package app

import (
	"context"
	"time"

	"git-mirror/internal/api"
	"git-mirror/internal/client"
	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

type App struct {
	Dir    datadir.Dir
	Client *client.Client
}

func New(dir datadir.Dir) *App {
	return &App{Dir: dir, Client: client.New(dir)}
}

func (a *App) notifyReload(ctx context.Context) {
	if a.Client.Running(ctx) {
		_ = a.Client.Reload(ctx)
	}
}

// Status returns the server view when it runs, otherwise a snapshot built
// from the files in the data dir.
func (a *App) Status(ctx context.Context) (api.Status, error) {
	if a.Client.Running(ctx) {
		return a.Client.Status(ctx)
	}
	cfg, err := config.Load(a.Dir)
	if err != nil {
		return api.Status{}, err
	}
	st, err := config.LoadState(a.Dir)
	if err != nil {
		return api.Status{}, err
	}
	out := api.Status{
		Server:   api.ServerInfo{Running: false, DataDir: a.Dir.String()},
		Settings: cfg.Settings,
	}
	for _, r := range cfg.Repos {
		rs := st.Get(r.Name)
		var due time.Time
		if r.Enabled && !rs.LastAttempt.IsZero() {
			due = rs.LastAttempt.Add(r.EffectiveInterval(cfg.Settings))
		}
		out.Repos = append(out.Repos, api.RepoStatus{Repo: r, State: rs, NextDue: due})
	}
	return out, nil
}

// Init creates the data dir and the initial config.json if it is missing.
func (a *App) Init() (bool, error) { return config.Init(a.Dir) }
