// Package server implements the git-mirror server: a scheduler that keeps
// mirrors fresh, a control API over a unix socket, and an optional read-only
// Git smart-HTTP endpoint for the private network.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
	"git-mirror/internal/identity"
	"git-mirror/internal/mirror"
	"git-mirror/internal/tlsconf"
)

// Options tune the server. Zero values fall back to config.json settings.
type Options struct {
	// Listen overrides Settings.Listen (git HTTP endpoint). "-" disables it.
	Listen string
	// SSHListen overrides Settings.SSHListen (git SSH endpoint). "-" disables it.
	SSHListen string
	// Tick is how often the scheduler re-reads config and checks due repos.
	Tick time.Duration
	// Version is reported by the API.
	Version string
}

// Server is the running state.
type Server struct {
	dir      datadir.Dir
	opts     Options
	log      *slog.Logger
	started  time.Time
	gitHTTP  string // effective git HTTP listen address ("" = disabled)
	gitHTTPS bool   // gitHTTP serves TLS
	gitSSH   string // effective git SSH listen address ("" = disabled)

	mu       sync.Mutex
	cfg      *config.Config
	cfgMtime time.Time
	inflight map[string]context.CancelFunc // running syncs, by repo name
	pending  map[string]bool               // queued but not yet started
	queue    chan job
	wake     chan struct{}
	reload   chan struct{}
}

type job struct {
	repo  config.Repo
	force bool
}

// New constructs a server bound to the data directory.
func New(dir datadir.Dir, opts Options, log *slog.Logger) *Server {
	if opts.Tick <= 0 {
		opts.Tick = 15 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		dir:      dir,
		opts:     opts,
		log:      log,
		inflight: map[string]context.CancelFunc{},
		pending:  map[string]bool{},
		queue:    make(chan job, 1024),
		wake:     make(chan struct{}, 1),
		reload:   make(chan struct{}, 1),
	}
}

// Run blocks until ctx is canceled.
func (s *Server) Run(ctx context.Context) error {
	if created, err := config.Init(s.dir); err != nil {
		return err
	} else if created {
		s.log.Info("created the initial config", "path", s.dir.ConfigPath())
	}
	if _, err := mirror.Version(ctx); err != nil {
		return fmt.Errorf("git is required: %w", err)
	}
	if err := s.loadConfig(true); err != nil {
		return err
	}
	s.started = time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Clear stale "syncing" markers left by a previous crash.
	_, _ = config.UpdateState(s.dir, func(st *config.State) error {
		for n, rs := range st.Repos {
			if rs.Status == config.StatusSyncing {
				rs.Status = config.StatusError
				rs.LastError = "interrupted by server restart"
				st.Repos[n] = rs
			}
		}
		return nil
	})

	// Resolve the public endpoints before any goroutine can read them.
	set := s.config().Settings
	s.gitHTTP = effectiveAddr(s.opts.Listen, set.Listen)
	s.gitSSH = effectiveAddr(s.opts.SSHListen, set.SSHListen)
	if s.gitHTTP != "" {
		if err := tlsconf.Validate(set, s.dir); err != nil {
			return fmt.Errorf("tls settings: %w", err)
		}
		s.gitHTTPS = set.TLS.Enabled()
		if created, err := config.EnsureTokens(s.dir); err != nil {
			return fmt.Errorf("http tokens: %w", err)
		} else if created {
			s.log.Info("generated the git http access token \"default\", show it with: git-mirror token")
		}
	}

	var wg sync.WaitGroup
	errc := make(chan error, 8) // room for every component to fail without blocking
	start := func(name string, fn func(context.Context) error) {
		wg.Go(func() {
			if err := fn(ctx); err != nil && !errors.Is(err, context.Canceled) {
				errc <- fmt.Errorf("%s: %w", name, err)
			}
		})
	}
	start("api", s.serveAPI)
	if s.gitHTTP != "" {
		start("git http", func(ctx context.Context) error { return s.serveGitHTTP(ctx, s.gitHTTP) })
	}
	if s.gitSSH != "" {
		start("git ssh", func(ctx context.Context) error { return s.serveGitSSH(ctx, s.gitSSH) })
	}
	start("workers", func(ctx context.Context) error {
		s.runWorkers(ctx)
		return nil
	})
	start("scheduler", func(ctx context.Context) error {
		s.schedule(ctx)
		return nil
	})

	s.log.Info("git-mirror server started",
		"data_dir", s.dir.String(),
		"socket", s.dir.SocketPath(),
		"repos", len(s.config().Repos),
		"workers", set.Workers,
		"git_http", s.gitHTTP,
		"git_https", s.gitHTTPS,
		"git_ssh", s.gitSSH,
		"pid", os.Getpid())

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errc:
		cancel()
	}
	wg.Wait()
	return runErr
}

// effectiveAddr picks the flag over the setting. "-" disables the endpoint.
func effectiveAddr(flag, setting string) string {
	addr := flag
	if addr == "" {
		addr = setting
	}
	if addr == "-" {
		return ""
	}
	return addr
}

func (s *Server) config() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// loadConfig re-reads config.json if it changed on disk (or force).
func (s *Server) loadConfig(force bool) error {
	mtime := config.ModTime(s.dir)
	s.mu.Lock()
	unchanged := !force && s.cfg != nil && mtime.Equal(s.cfgMtime)
	s.mu.Unlock()
	if unchanged {
		return nil
	}
	c, err := config.Load(s.dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	prev := s.cfg
	s.cfg = c
	s.cfgMtime = mtime
	// Stop syncs of repositories that were just removed.
	for name, cancel := range s.inflight {
		if c.Find(name) == nil {
			cancel()
		}
	}
	s.mu.Unlock()
	if prev != nil {
		s.log.Info("config reloaded", "repos", len(c.Repos))
	}
	return config.PruneState(s.dir, c)
}

// schedule enqueues due repositories on every tick.
func (s *Server) schedule(ctx context.Context) {
	t := time.NewTicker(s.opts.Tick)
	defer t.Stop()
	s.enqueueDue()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.reload:
		case <-s.wake:
		}
		if err := s.loadConfig(false); err != nil {
			s.log.Error("reload config", "err", err)
			continue
		}
		s.enqueueDue()
	}
}

func (s *Server) enqueueDue() {
	cfg := s.config()
	st, err := config.LoadState(s.dir)
	if err != nil {
		s.log.Error("load state", "err", err)
		return
	}
	now := time.Now()
	for _, r := range cfg.Repos {
		if !r.Enabled {
			continue
		}
		rs := st.Get(r.Name)
		if rs.LastAttempt.IsZero() || now.Sub(rs.LastAttempt) >= r.EffectiveInterval(cfg.Settings) {
			s.enqueue(job{repo: r})
		}
	}
}

// enqueue schedules a repo unless it is already queued or running.
// Non-blocking. Returns false when nothing was queued.
func (s *Server) enqueue(j job) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := j.repo.Name
	if s.pending[name] {
		return false
	}
	if _, running := s.inflight[name]; running {
		return false
	}
	select {
	case s.queue <- j:
		s.pending[name] = true
		return true
	default:
		s.log.Warn("queue full, skipping", "repo", name)
		return false
	}
}

func (s *Server) runWorkers(ctx context.Context) {
	n := s.config().Settings.Workers
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case j := <-s.queue:
					s.syncOne(ctx, j)
				}
			}
		})
	}
	wg.Wait()
}

// syncOne runs a single sync and records the outcome in state.json.
func (s *Server) syncOne(ctx context.Context, j job) {
	name := j.repo.Name
	s.mu.Lock()
	delete(s.pending, name)
	if _, running := s.inflight[name]; running {
		s.mu.Unlock()
		return
	}
	set := s.cfg.Settings
	// The repo may have been removed or disabled since it was queued.
	cur := s.cfg.Find(name)
	if cur == nil || (!cur.Enabled && !j.force) {
		s.mu.Unlock()
		return
	}
	j.repo = *cur
	jctx, cancel := context.WithTimeout(ctx, set.SyncTimeout.Std())
	s.inflight[name] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.inflight, name)
		s.mu.Unlock()
	}()

	started := time.Now()
	if err := config.MarkSyncing(s.dir, name, started); err != nil {
		s.log.Error("write state", "repo", name, "err", err)
	}
	s.log.Info("sync start", "repo", name, "url", mirror.RedactURL(j.repo.URL))
	var res mirror.Result
	opts, err := identity.SyncOptions(s.dir, j.repo, set)
	if err == nil {
		res, err = mirror.Sync(jctx, s.dir.RepoPath(name), j.repo.URL, opts)
	}
	if werr := config.MarkResult(s.dir, name, started, res.Head, res.SizeBytes, err); werr != nil {
		s.log.Error("write state", "repo", name, "err", werr)
	}
	if err != nil {
		s.log.Error("sync failed", "repo", name, "err", err, "took", time.Since(started).Round(time.Millisecond))
		if s.config().Find(name) == nil {
			// Canceled because the repo was removed: drop empty parent dirs a
			// half-finished clone may have left behind.
			mirror.PruneEmptyParents(s.dir.ReposDir(), s.dir.RepoPath(name))
		}
		return
	}
	s.log.Info("sync done", "repo", name, "created", res.Created, "head", short(res.Head), "took", res.Duration.Round(time.Millisecond))
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
