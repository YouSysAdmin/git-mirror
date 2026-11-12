package server

import (
	"context"
	"testing"

	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

func TestEnqueueDedupes(t *testing.T) {
	s := New(datadir.Dir(t.TempDir()), Options{}, nil)
	r := config.Repo{Name: "a/b", URL: "x", Enabled: true}

	if !s.enqueue(job{repo: r}) {
		t.Fatal("first enqueue should succeed")
	}
	if s.enqueue(job{repo: r}) {
		t.Fatal("second enqueue of a pending repo must be a no-op")
	}
	if len(s.queue) != 1 {
		t.Fatalf("queue len = %d, want 1", len(s.queue))
	}

	// Dequeue clears pending. A running repo is still not re-queued.
	j := <-s.queue
	s.mu.Lock()
	delete(s.pending, j.repo.Name)
	s.inflight[j.repo.Name] = func() {}
	s.mu.Unlock()
	if s.enqueue(job{repo: r}) {
		t.Fatal("in-flight repo must not be re-queued")
	}
	s.mu.Lock()
	delete(s.inflight, j.repo.Name)
	s.mu.Unlock()
	if !s.enqueue(job{repo: r}) {
		t.Fatal("idle repo should be queued again")
	}
}

func TestEffectiveAddr(t *testing.T) {
	cases := []struct{ flag, setting, want string }{
		{"", "", ""},
		{"", ":8080", ":8080"},
		{":9090", ":8080", ":9090"},
		{"-", ":8080", ""},
	}
	for _, c := range cases {
		if got := effectiveAddr(c.flag, c.setting); got != c.want {
			t.Errorf("effectiveAddr(%q,%q)=%q want %q", c.flag, c.setting, got, c.want)
		}
	}
}

func TestRunRefusesWithoutGitDir(t *testing.T) {
	// Sanity: Run must fail fast when the data dir is unusable.
	s := New("/proc/definitely/not/writable", Options{}, nil)
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
