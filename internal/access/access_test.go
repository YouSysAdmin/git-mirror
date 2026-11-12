package access

import (
	"slices"
	"testing"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		name string
		pats []string
		want bool
	}{
		{"main", []string{"main"}, true},
		{"main2", []string{"main"}, false},
		{"release/1.0", []string{"release/*"}, true},
		{"release", []string{"release/*"}, false},
		{"feature/x/y", []string{"feature/*"}, true},
		{"v1-rc", []string{"v*-rc"}, true},
		{"anything", []string{"*"}, true},
		{"anything", nil, false},
	}
	for _, c := range cases {
		if got := Match(c.name, c.pats); got != c.want {
			t.Errorf("Match(%q,%v)=%v", c.name, c.pats, got)
		}
	}
}

func TestAllowed(t *testing.T) {
	if !Allowed(nil, "x") || !Allowed([]string{}, "x") {
		t.Fatal("empty scope must allow everything")
	}
	if !Allowed([]string{"team/*"}, "team/a/b") || Allowed([]string{"team/*"}, "other") {
		t.Fatal("pattern scope")
	}
	if !Allowed([]string{"go"}, "go") || Allowed([]string{"go"}, "gopher") {
		t.Fatal("exact scope")
	}
}

func TestValidateRepoPatterns(t *testing.T) {
	for _, ok := range [][]string{{"go"}, {"team/*"}, {"github.com/golang/*", "x_y-1.2"}, {"*"}} {
		if err := ValidateRepoPatterns(ok); err != nil {
			t.Errorf("%v should be valid: %v", ok, err)
		}
	}
	for _, bad := range [][]string{{""}, {"a/**"}, {"a/../b"}, {"/abs"}, {"trail/"}, {"with space"}, {"x.git"}} {
		if err := ValidateRepoPatterns(bad); err == nil {
			t.Errorf("%v should be invalid", bad)
		}
	}
}

func TestParseRepoList(t *testing.T) {
	for _, s := range []string{"", "all", "*", "  "} {
		got, err := ParseRepoList(s)
		if err != nil || got != nil {
			t.Errorf("ParseRepoList(%q)=%v %v, want nil", s, got, err)
		}
	}
	got, err := ParseRepoList("go, team/* x")
	if err != nil || !slices.Equal(got, []string{"go", "team/*", "x"}) {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := ParseRepoList("a,b/../c"); err == nil {
		t.Fatal("expected error")
	}
	if Label(nil) != "all" || Label(got) != "go,team/*,x" {
		t.Fatalf("label: %q %q", Label(nil), Label(got))
	}
}
