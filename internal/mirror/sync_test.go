package mirror

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestSyncPrunesAndFiltersBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	up := filepath.Join(tmp, "upstream")
	run(t, tmp, "init", "-q", "-b", "main", up)
	run(t, up, "commit", "-q", "--allow-empty", "-m", "one")
	run(t, up, "branch", "feature")
	run(t, up, "branch", "release/1.0")
	run(t, up, "tag", "v1")

	mirror := filepath.Join(tmp, "repos", "local", "up.git")
	res, err := Sync(ctx, mirror, up, Options{})
	if err != nil || !res.Created {
		t.Fatalf("first sync: %v %+v", err, res)
	}
	got, _ := Branches(ctx, mirror)
	if !slices.Equal(got, []string{"feature", "main", "release/1.0"}) {
		t.Fatalf("branches after full mirror: %v", got)
	}

	// Deleted upstream branch and tag disappear from the mirror.
	run(t, up, "branch", "-D", "feature")
	run(t, up, "tag", "-d", "v1")
	if _, err := Sync(ctx, mirror, up, Options{}); err != nil {
		t.Fatal(err)
	}
	got, _ = Branches(ctx, mirror)
	if slices.Contains(got, "feature") {
		t.Fatalf("deleted branch still present: %v", got)
	}
	if tags := run(t, mirror, "tag"); tags != "" {
		t.Fatalf("deleted tag still present: %q", tags)
	}

	// Narrowing the filter drops branches that no longer match.
	run(t, up, "branch", "feature")
	run(t, up, "branch", "release/2.0")
	if _, err := Sync(ctx, mirror, up, Options{Branches: []string{"release/*"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = Branches(ctx, mirror)
	if !slices.Equal(got, []string{"release/1.0", "release/2.0"}) {
		t.Fatalf("branches after filter: %v", got)
	}
	// HEAD must point at an existing branch even though "main" is not mirrored.
	head := run(t, mirror, "symbolic-ref", "HEAD")
	if head != "refs/heads/release/1.0\n" {
		t.Fatalf("HEAD = %q", head)
	}

	// Widening back restores everything and HEAD follows upstream default.
	if _, err := Sync(ctx, mirror, up, Options{}); err != nil {
		t.Fatal(err)
	}
	got, _ = Branches(ctx, mirror)
	if len(got) != 4 {
		t.Fatalf("branches after widening: %v", got)
	}
	if head := run(t, mirror, "symbolic-ref", "HEAD"); head != "refs/heads/main\n" {
		t.Fatalf("HEAD = %q", head)
	}
	// The mirror is cloneable.
	run(t, tmp, "clone", "-q", mirror, filepath.Join(tmp, "clone"))
}
