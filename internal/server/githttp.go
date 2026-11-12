package server

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/cgi"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yousysadmin/go-tlsutils"

	"git-mirror/internal/api"
	"git-mirror/internal/config"
	"git-mirror/internal/tlsconf"
)

// tokenKey carries the authenticated token through the request context.
type tokenKey struct{}

// repoPathRe matches the request paths git http-backend serves. The mirror
// name is the leading part, ".git" is optional (like http-backend itself).
var repoPathRe = regexp.MustCompile(`^/(.+?)(?:\.git)?(/HEAD|/info/refs|/objects/info/(?:alternates|http-alternates|packs)|/objects/[0-9a-f]{2}/[0-9a-f]{38,62}|/objects/pack/pack-[0-9a-f]{40,64}\.(?:pack|idx)|/git-upload-pack|/git-receive-pack)$`)

// serveGitHTTP exposes repos/ read-only through `git http-backend`, so hosts
// in the private network can `git clone http://git:<token>@<server>/<name>.git`.
// Every request must carry one of the tokens from <data-dir>/tokens.json,
// either as the HTTP basic-auth password (any username) or as a bearer
// token. The token scope decides which mirrors are visible.
func (s *Server) serveGitHTTP(ctx context.Context, addr string) error {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	backend := &cgi.Handler{
		Path: gitBin,
		Args: []string{"http-backend"},
		Env: []string{
			"GIT_PROJECT_ROOT=" + s.dir.ReposDir(),
			"GIT_HTTP_EXPORT_ALL=1",
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			s.serveIndex(w, r)
			return
		}
		// Keep the token out of the CGI child environment.
		r.Header.Del("Authorization")
		backend.ServeHTTP(w, r)
	})

	set := s.config().Settings
	tlsCfg, stopACME, err := tlsutils.BuildWithShutdown(tlsconf.Map(set, s.dir))
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}

	root := http.NewServeMux()
	// The serving certificate is public: clients fetch it to pin a self-signed
	// endpoint (curl -k https://host/-/cert.pem), so it needs no token.
	root.HandleFunc("GET /-/cert.pem", func(w http.ResponseWriter, _ *http.Request) {
		pemData, err := tlsconf.ServingCertPEM(set, s.dir)
		if err != nil {
			s.log.Warn("serving certificate unavailable", "err", err)
			http.Error(w, "no certificate available", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(pemData)
	})
	root.Handle("/", s.authorize(mux))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	// IdleTimeout bounds keep-alive connections, WriteTimeout is left unset so
	// large clones are not cut off.
	srv := &http.Server{Handler: root, TLSConfig: tlsCfg, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
		_ = stopACME(shctx)
	}()
	if tlsCfg != nil {
		s.log.Info("git https endpoint listening", "addr", ln.Addr().String(), "tls_mode", set.TLS.Mode)
		err = srv.ServeTLS(ln, "", "")
	} else {
		s.log.Info("git http endpoint listening", "addr", ln.Addr().String())
		err = srv.Serve(ln)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// authorize authenticates the request with a token and checks that the
// addressed mirror exists in config.json and is within the token scope. The
// tokens file is re-read on every request so changes apply immediately.
func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens, err := config.LoadTokens(s.dir)
		if err != nil || len(tokens.Tokens) == 0 {
			s.log.Error("http tokens unavailable", "err", err)
			http.Error(w, "access token not configured", http.StatusServiceUnavailable)
			return
		}
		tok := tokens.Lookup(credential(r))
		if tok == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="git-mirror"`)
			http.Error(w, "authentication required.", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), tokenKey{}, *tok))
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			next.ServeHTTP(w, r)
			return
		}
		// Out-of-scope and unknown mirrors get the same answer, so a scoped
		// token cannot probe which other mirrors exist.
		name, ok := repoFromPath(r.URL.Path)
		if !ok || config.ValidateName(name) != nil || !tok.Allows(name) || s.config().Find(name) == nil {
			if ok && !tok.Allows(name) {
				s.log.Warn("http access denied", "repo", name, "token", tok.Name, "remote", r.RemoteAddr)
			}
			http.Error(w, "repository not found", http.StatusNotFound)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/git-receive-pack") || r.URL.Query().Get("service") == "git-receive-pack" {
			http.Error(w, "read-only mirror, push is not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// credential extracts the presented token: basic-auth password or bearer.
func credential(r *http.Request) string {
	if _, pw, ok := r.BasicAuth(); ok {
		return pw
	}
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// tokenOf returns the token stored by authorize, or a zero token.
func tokenOf(r *http.Request) config.Token {
	t, _ := r.Context().Value(tokenKey{}).(config.Token)
	return t
}

// repoFromPath derives the mirror name from a smart or dumb HTTP request path.
func repoFromPath(p string) (string, bool) {
	m := repoPathRe.FindStringSubmatch(p)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// serveIndex renders a plain list of clone URLs within the token scope.
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	st, err := s.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tok := tokenOf(r)
	st.Repos = slices.DeleteFunc(st.Repos, func(rs api.RepoStatus) bool { return !tok.Allows(rs.Repo.Name) })
	base := "http://" + r.Host
	if r.TLS != nil {
		base = "https://" + r.Host
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, "<!doctype html><title>git-mirror</title><h1>git-mirror</h1><table><tr><th align=left>repository</th><th align=left>status</th><th align=left>last success</th><th align=left>clone</th></tr>")
		for _, rs := range st.Repos {
			url := fmt.Sprintf("%s/%s.git", base, rs.Repo.Name)
			last := "-"
			if !rs.State.LastSuccess.IsZero() {
				last = rs.State.LastSuccess.Local().Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(w, "<tr><td>%s</td><td>%s</td><td>%s</td><td><code>git clone %s</code></td></tr>",
				html.EscapeString(rs.Repo.Name), rs.State.Status, last, html.EscapeString(url))
		}
		_, _ = fmt.Fprint(w, "</table>")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, rs := range st.Repos {
		_, _ = fmt.Fprintf(w, "%s/%s.git\n", base, rs.Repo.Name)
	}
}
