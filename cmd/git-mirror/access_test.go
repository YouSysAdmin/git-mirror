package main

import (
	"testing"

	"git-mirror/internal/config"
)

func TestTokensFor(t *testing.T) {
	tokens := []config.Token{
		{Name: "ci", Value: "c", Repos: []string{"go"}},
		{Name: "team", Value: "t", Repos: []string{"team/*"}},
		{Name: "all", Value: "a"},
		{Name: config.DefaultTokenName, Value: "d"},
	}
	got := tokensFor(tokens, "go")
	if len(got) != 3 || got[0].Name != "default" || got[1].Name != "all" || got[2].Name != "ci" {
		t.Fatalf("order for go: %+v", got)
	}
	if got := tokensFor(tokens, "team/x"); len(got) != 3 || got[2].Name != "team" {
		t.Fatalf("order for team/x: %+v", got)
	}
	if p := pickToken(tokens, "other"); p == nil || p.Name != "default" {
		t.Fatalf("pick: %+v", p)
	}
	if p := pickToken([]config.Token{{Name: "ci", Value: "c", Repos: []string{"go"}}}, "other"); p != nil {
		t.Fatalf("no access expected, got %+v", p)
	}
	l := cloneLinks{HTTP: "https://mirror:8443/team/x.git"}
	if got := l.withToken("s3cret"); got != "https://git:s3cret@mirror:8443/team/x.git" {
		t.Fatalf("withToken: %q", got)
	}
	if (cloneLinks{}).withToken("x") != "" || l.withToken("") != "" {
		t.Fatal("empty inputs must yield an empty url")
	}
}
