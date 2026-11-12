package mirror

import (
	"strings"
	"testing"
)

func TestNameFromURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/golang/go.git":         "github.com/golang/go",
		"https://github.com/golang/go":             "github.com/golang/go",
		"https://user:tok@GitHub.com:443/a/b.git/": "github.com/a/b",
		"git@github.com:golang/go.git":             "github.com/golang/go",
		"ssh://git@gitlab.com/group/sub/proj.git":  "gitlab.com/group/sub/proj",
		"git://git.kernel.org/pub/scm/git/git.git": "git.kernel.org/pub/scm/git/git",
		"/srv/git/foo.git":                         "local/srv/git/foo",
	}
	for in, want := range cases {
		got, err := NameFromURL(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
	if _, err := NameFromURL("https://github.com"); err == nil {
		t.Error("expected error for host-only url")
	}
}

func TestIsSSHURL(t *testing.T) {
	for _, u := range []string{"ssh://git@gitlab.com/a/b.git", "SSH://host/x", "git@github.com:golang/go.git", "host:path/repo"} {
		if !IsSSHURL(u) {
			t.Errorf("%q should be ssh", u)
		}
	}
	for _, u := range []string{"https://github.com/golang/go.git", "http://user:tok@host/x", "git://host/x", "file:///srv/x", "/srv/git/x.git", "./x"} {
		if IsSSHURL(u) {
			t.Errorf("%q should not be ssh", u)
		}
	}
}

func TestLastLinesStripsControl(t *testing.T) {
	got := lastLines("fatal: \x1b]0;TITLE\a\x1b[2Kforged\x1b]52;c;QQ==\a\n", 3)
	if strings.ContainsAny(got, "\x1b\a") || !strings.Contains(got, "forged") {
		t.Fatalf("control bytes kept: %q", got)
	}
}

func TestRedactURL(t *testing.T) {
	if got := RedactURL("https://user:tok@example.com/o/r.git"); strings.Contains(got, "tok") {
		t.Fatalf("password not redacted: %q", got)
	}
	if got := RedactURL("git@example.com:o/r.git"); got != "git@example.com:o/r.git" {
		t.Fatalf("scp url changed: %q", got)
	}
}
