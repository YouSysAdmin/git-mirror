package config

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"git-mirror/internal/datadir"
)

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"a", "github.com/golang/go", "x/y_z-1.2"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/abs", "a//b", "a/../b", ".hidden", "a/.", "with space", "repo.git", "-dash"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestApplyNameStyle(t *testing.T) {
	cases := []struct{ full, style, want string }{
		{"github.com/golang/go", "", "go"},
		{"github.com/golang/go", "repo", "go"},
		{"github.com/golang/go", "owner", "golang/go"},
		{"github.com/golang/go", "full", "github.com/golang/go"},
		{"gitlab.com/group/sub/proj", "owner", "sub/proj"},
		{"gitlab.com/group/sub/proj", "repo", "proj"},
		{"local/srv/git/foo", "repo", "foo"},
		{"host/repo", "owner", "repo"},
		{"repo", "owner", "repo"},
	}
	for _, c := range cases {
		if got := ApplyNameStyle(c.full, c.style); got != c.want {
			t.Errorf("ApplyNameStyle(%q,%q)=%q want %q", c.full, c.style, got, c.want)
		}
	}
}

func TestDurationJSON(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"sync_interval":"1h30m","sync_timeout":60000000000}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.SyncInterval.Std() != 90*time.Minute || s.SyncTimeout.Std() != time.Minute {
		t.Fatalf("got %v %v", s.SyncInterval, s.SyncTimeout)
	}
	b, _ := json.Marshal(Duration(2 * time.Hour))
	if string(b) != `"2h0m0s"` {
		t.Fatalf("got %s", b)
	}
}

func TestUpdateConcurrent(t *testing.T) {
	dir := datadir.Dir(t.TempDir())
	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Update(dir, func(c *Config) error {
				return c.Add(Repo{Name: "r" + string(rune('a'+i)), URL: "https://x/y", Enabled: true})
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Repos) != n {
		t.Fatalf("expected %d repos, got %d (lost updates)", n, len(c.Repos))
	}
	if _, err := Update(dir, func(c *Config) error { return c.Add(Repo{Name: "ra", URL: "u"}) }); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if _, err := Update(dir, func(c *Config) error { return c.Remove("nope") }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// State prune drops unknown entries and keeps known ones.
	_, _ = UpdateState(dir, func(s *State) error {
		s.Repos["ra"] = RepoState{Status: StatusOK}
		s.Repos["gone"] = RepoState{Status: StatusOK}
		return nil
	})
	if err := PruneState(dir, c); err != nil {
		t.Fatal(err)
	}
	st, _ := LoadState(dir)
	if _, ok := st.Repos["gone"]; ok || st.Get("ra").Status != StatusOK {
		t.Fatalf("prune failed: %+v", st.Repos)
	}
}

func TestInit(t *testing.T) {
	dir := datadir.Dir(t.TempDir() + "/data")
	created, err := Init(dir)
	if err != nil || !created {
		t.Fatalf("first Init: created=%v err=%v", created, err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.Listen != DefaultListen || c.Settings.SSHListen != DefaultSSHListen {
		t.Fatalf("initial endpoints: %q %q", c.Settings.Listen, c.Settings.SSHListen)
	}

	// An existing config, including a disabled endpoint, is kept as is.
	if _, err := Update(dir, func(c *Config) error { c.Settings.Listen = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	if created, err := Init(dir); err != nil || created {
		t.Fatalf("second Init: created=%v err=%v", created, err)
	}
	if c, _ := Load(dir); c.Settings.Listen != "" {
		t.Fatalf("listen restored to %q", c.Settings.Listen)
	}
}
