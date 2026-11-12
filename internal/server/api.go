package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	"git-mirror/internal/api"
	"git-mirror/internal/config"
)

// serveAPI exposes the control API on the unix socket in the data dir.
func (s *Server) serveAPI(ctx context.Context) error {
	sock := s.dir.SocketPath()
	if err := s.dir.CheckSocketDir(true); err != nil {
		return err
	}
	// Refuse to start if another server is alive on the socket.
	if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		_ = c.Close()
		return errors.New("another git-mirror server is already running on " + sock)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(sock) }()
	_ = os.Chmod(sock, 0o660)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, api.Ping{OK: true, Version: s.opts.Version, PID: os.Getpid()})
	})
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("POST /v1/sync", s.handleSync)
	mux.HandleFunc("POST /v1/reload", func(w http.ResponseWriter, _ *http.Request) {
		if err := s.loadConfig(true); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		select {
		case s.reload <- struct{}{}:
		default:
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Status assembles the API status snapshot. The CLI reuses it in local mode.
func (s *Server) Status() (api.Status, error) {
	cfg := s.config()
	st, err := config.LoadState(s.dir)
	if err != nil {
		return api.Status{}, err
	}
	s.mu.Lock()
	inflight := make(map[string]bool, len(s.inflight))
	for n := range s.inflight {
		inflight[n] = true
	}
	queued := len(s.pending)
	s.mu.Unlock()

	out := api.Status{
		Server: api.ServerInfo{
			Running:  true,
			PID:      os.Getpid(),
			Version:  s.opts.Version,
			Started:  s.started,
			DataDir:  s.dir.String(),
			Queued:   queued,
			GitHTTP:  s.gitHTTP,
			GitHTTPS: s.gitHTTPS,
			GitSSH:   s.gitSSH,
		},
		Settings: cfg.Settings,
	}
	for _, r := range cfg.Repos {
		out.Repos = append(out.Repos, api.RepoStatus{
			Repo:     r,
			State:    st.Get(r.Name),
			InFlight: inflight[r.Name],
			NextDue:  nextDue(r, st.Get(r.Name), cfg.Settings),
		})
	}
	return out, nil
}

func nextDue(r config.Repo, rs config.RepoState, set config.Settings) time.Time {
	if !r.Enabled {
		return time.Time{}
	}
	if rs.LastAttempt.IsZero() {
		return time.Now()
	}
	return rs.LastAttempt.Add(r.EffectiveInterval(set))
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st, err := s.Status()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleSync force-enqueues the named repos (query ?name=a&name=b) or all.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if err := s.loadConfig(false); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cfg := s.config()
	names := r.URL.Query()["name"]
	var resp api.SyncResponse
	if len(names) == 0 {
		for _, repo := range cfg.Repos {
			if repo.Enabled {
				names = append(names, repo.Name)
			}
		}
	}
	for _, n := range names {
		repo := cfg.Find(n)
		if repo == nil {
			resp.Unknown = append(resp.Unknown, n)
			continue
		}
		if s.enqueue(job{repo: *repo, force: true}) {
			resp.Queued = append(resp.Queued, n)
		} else {
			resp.Skipped = append(resp.Skipped, n)
		}
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, api.Error{Error: err.Error()})
}
