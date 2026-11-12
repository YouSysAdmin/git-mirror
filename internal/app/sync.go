package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"git-mirror/internal/config"
	"git-mirror/internal/identity"
	"git-mirror/internal/mirror"
)

// Phase of a SyncEvent.
type Phase string

const (
	PhaseQueued Phase = "queued" // handed to the server (Err set when it was already queued)
	PhaseStart  Phase = "start"  // local sync started
	PhaseDone   Phase = "done"   // local sync finished, Result set
	PhaseError  Phase = "error"  // local sync failed, Err set
)

// SyncEvent is reported by Sync for each repository.
type SyncEvent struct {
	Name   string
	Phase  Phase
	Err    error
	Result mirror.Result
}

// SyncOptions for Sync.
type SyncOptions struct {
	// Local forces running git in this process even if the server is up.
	Local bool
	// Progress receives events (maybe nil).
	Progress func(SyncEvent)
}

// Sync synchronizes the named repos (all enabled if names is empty). It returns
// viaServer=true when the work was handed to the server.
func (a *App) Sync(ctx context.Context, names []string, o SyncOptions) (viaServer bool, err error) {
	if !o.Local && a.Client.Running(ctx) {
		resp, err := a.Client.Sync(ctx, names...)
		if err != nil {
			return true, err
		}
		if o.Progress != nil {
			for _, n := range resp.Queued {
				o.Progress(SyncEvent{Name: n, Phase: PhaseQueued})
			}
			for _, n := range resp.Skipped {
				o.Progress(SyncEvent{Name: n, Phase: PhaseQueued, Err: errors.New("already queued or syncing on the server")})
			}
		}
		if len(resp.Unknown) > 0 {
			return true, fmt.Errorf("%w: %s", config.ErrNotFound, strings.Join(resp.Unknown, ", "))
		}
		return true, nil
	}
	return false, a.syncLocal(ctx, names, o.Progress)
}

func (a *App) syncLocal(ctx context.Context, names []string, progress func(SyncEvent)) error {
	if err := a.Dir.Ensure(); err != nil {
		return err
	}
	cfg, err := config.Load(a.Dir)
	if err != nil {
		return err
	}
	var repos []config.Repo
	if len(names) == 0 {
		for _, r := range cfg.Repos {
			if r.Enabled {
				repos = append(repos, r)
			}
		}
	} else {
		for _, n := range names {
			r := cfg.Find(n)
			if r == nil {
				return fmt.Errorf("%w: %s", config.ErrNotFound, n)
			}
			repos = append(repos, *r)
		}
	}
	var failed []string
	for _, r := range repos {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if progress != nil {
			progress(SyncEvent{Name: r.Name, Phase: PhaseStart})
		}
		res, err := a.SyncRepoLocal(ctx, r, cfg.Settings)
		if progress != nil {
			if err != nil {
				progress(SyncEvent{Name: r.Name, Phase: PhaseError, Err: err})
			} else {
				progress(SyncEvent{Name: r.Name, Phase: PhaseDone, Result: res})
			}
		}
		if err != nil {
			failed = append(failed, r.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d failed: %s", len(failed), len(repos), strings.Join(failed, ", "))
	}
	return nil
}

// SyncRepoLocal runs git for one repo in-process and records the outcome in
// state.json, exactly like the server does.
func (a *App) SyncRepoLocal(ctx context.Context, r config.Repo, set config.Settings) (mirror.Result, error) {
	timeout := set.SyncTimeout.Std()
	if timeout <= 0 {
		timeout = config.DefaultSyncTimeout
	}
	started := time.Now()
	_ = config.MarkSyncing(a.Dir, r.Name, started)
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var res mirror.Result
	opts, err := identity.SyncOptions(a.Dir, r, set)
	if err == nil {
		res, err = mirror.Sync(sctx, a.Dir.RepoPath(r.Name), r.URL, opts)
	}
	_ = config.MarkResult(a.Dir, r.Name, started, res.Head, res.SizeBytes, err)
	return res, err
}
