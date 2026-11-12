package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

func TestRepoFromPath(t *testing.T) {
	cases := map[string]string{
		"/go.git/info/refs":                            "go",
		"/go/info/refs":                                "go",
		"/team/go.git/git-upload-pack":                 "team/go",
		"/team/go/git-upload-pack":                     "team/go",
		"/a/b/c.git/HEAD":                              "a/b/c",
		"/x.git/objects/info/packs":                    "x",
		"/x.git/objects/ab/" + strings.Repeat("c", 38): "x",
		"/x.git/objects/pack/pack-" + strings.Repeat("d", 40) + ".idx": "x",
		"/x.git/git-receive-pack":                                      "x",
		"/x.git.git/info/refs":                                         "x.git",
		"/":                                                            "",
		"/x.git":                                                       "",
		"/x.git/":                                                      "",
		"/x.git/objects/zz/aa":                                         "",
		"/x.git/info/refs/extra":                                       "",
		"/info/refs":                                                   "",
	}
	for in, want := range cases {
		got, ok := repoFromPath(in)
		if want == "" {
			if ok {
				t.Errorf("%q: expected no match, got %q", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%q: got %q %v want %q", in, got, ok, want)
		}
	}
}

func TestAuthorize(t *testing.T) {
	dir := datadir.Dir(t.TempDir())
	if _, err := config.Update(dir, func(c *config.Config) error {
		if err := c.Add(config.Repo{Name: "go", URL: "u", Enabled: true}); err != nil {
			return err
		}
		return c.Add(config.Repo{Name: "team/x", URL: "u", Enabled: true})
	}); err != nil {
		t.Fatal(err)
	}
	// A bare repo left on disk that is not in config.json must stay hidden.
	if err := os.MkdirAll(dir.RepoPath("ghost"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := New(dir, Options{}, nil)
	if err := s.loadConfig(true); err != nil {
		t.Fatal(err)
	}
	var seen config.Token
	h := s.authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = tokenOf(r)
		w.WriteHeader(http.StatusOK)
	}))

	code := func(path string, mod func(*http.Request)) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		mod(req)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	none := func(*http.Request) {}
	basic := func(pw string) func(*http.Request) {
		return func(r *http.Request) { r.SetBasicAuth("anyone", pw) }
	}

	if c := code("/go.git/info/refs", none); c != http.StatusServiceUnavailable {
		t.Fatalf("no token configured: got %d", c)
	}
	if _, err := config.AddToken(dir, "all", "s3cret", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := config.AddToken(dir, "ci", "ci-secret", []string{"team/*"}); err != nil {
		t.Fatal(err)
	}
	if c := code("/go.git/info/refs", none); c != http.StatusUnauthorized {
		t.Fatalf("anonymous: got %d", c)
	}
	if c := code("/go.git/info/refs", basic("wrong")); c != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d", c)
	}
	if c := code("/go.git/info/refs", basic("s3cret")); c != http.StatusOK || seen.Name != "all" {
		t.Fatalf("basic auth: got %d token %q", c, seen.Name)
	}
	if c := code("/go/info/refs", func(r *http.Request) { r.Header.Set("Authorization", "Bearer s3cret") }); c != http.StatusOK {
		t.Fatalf("bearer without .git: got %d", c)
	}
	if c := code("/team/x.git/git-upload-pack", basic("ci-secret")); c != http.StatusOK || seen.Name != "ci" {
		t.Fatalf("scoped token in scope: got %d token %q", c, seen.Name)
	}
	// Out of scope must look exactly like a missing mirror.
	if c := code("/go.git/info/refs", basic("ci-secret")); c != http.StatusNotFound {
		t.Fatalf("scoped token out of scope: got %d", c)
	}
	if c := code("/team/missing.git/info/refs", basic("ci-secret")); c != http.StatusNotFound {
		t.Fatalf("scoped token unknown repo: got %d", c)
	}
	if c := code("/nope.git/info/refs", basic("s3cret")); c != http.StatusNotFound {
		t.Fatalf("unknown repo: got %d", c)
	}
	if c := code("/ghost.git/info/refs", basic("s3cret")); c != http.StatusNotFound {
		t.Fatalf("repo on disk but not in config: got %d", c)
	}
	if c := code("/go.git/../x/info/refs", basic("s3cret")); c != http.StatusNotFound {
		t.Fatalf("traversal: got %d", c)
	}
	if c := code("/go.git/git-receive-pack", basic("s3cret")); c != http.StatusForbidden {
		t.Fatalf("push: got %d", c)
	}
	if c := code("/go.git/info/refs?service=git-receive-pack", basic("s3cret")); c != http.StatusForbidden {
		t.Fatalf("push advertisement: got %d", c)
	}
	if c := code("/", basic("ci-secret")); c != http.StatusOK || seen.Name != "ci" {
		t.Fatalf("index: got %d token %q", c, seen.Name)
	}

	// The index only lists mirrors within the token scope.
	idx := s.authorize(http.HandlerFunc(s.serveIndex))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("git", "ci-secret")
	rr := httptest.NewRecorder()
	idx.ServeHTTP(rr, req)
	if body := rr.Body.String(); !strings.Contains(body, "/team/x.git") || strings.Contains(body, "/go.git") {
		t.Fatalf("index body:\n%s", body)
	}
}
