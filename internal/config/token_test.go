package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"git-mirror/internal/datadir"
)

func TestTokensLegacyMigration(t *testing.T) {
	d := datadir.Dir(t.TempDir())
	if err := os.WriteFile(d.HTTPTokenPath(), []byte("old-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, err := LoadTokens(d)
	if err != nil || len(ts.Tokens) != 1 || ts.Tokens[0].Name != DefaultTokenName || ts.Tokens[0].Value != "old-secret" || !ts.Legacy() {
		t.Fatalf("legacy load: %+v %v", ts, err)
	}
	if _, err := os.Stat(d.TokensPath()); err == nil {
		t.Fatal("LoadTokens must not write tokens.json")
	}
	if len(ts.Tokens[0].Repos) != 0 || !ts.Tokens[0].Allows("anything") {
		t.Fatal("legacy token must be global")
	}
	created, err := EnsureTokens(d)
	if err != nil || created {
		t.Fatalf("ensure with legacy: created=%v %v", created, err)
	}
	if _, err := os.Stat(d.HTTPTokenPath()); !os.IsNotExist(err) {
		t.Fatal("http_token must be renamed after migration")
	}
	if _, err := os.Stat(d.HTTPTokenPath() + ".migrated"); err != nil {
		t.Fatal("http_token.migrated missing")
	}
	fi, err := os.Stat(d.TokensPath())
	if err != nil {
		t.Fatalf("tokens.json: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("tokens.json mode %v", fi.Mode())
	}
	ts, _ = LoadTokens(d)
	if ts.Legacy() || ts.Lookup("old-secret") == nil {
		t.Fatalf("after migration: %+v", ts)
	}
}

func TestTokensCRUD(t *testing.T) {
	d := datadir.Dir(t.TempDir())
	ts, err := LoadTokens(d)
	if err != nil || len(ts.Tokens) != 0 {
		t.Fatalf("empty: %+v %v", ts, err)
	}
	created, err := EnsureTokens(d)
	if err != nil || !created {
		t.Fatalf("ensure: %v %v", created, err)
	}
	if created, err = EnsureTokens(d); err != nil || created {
		t.Fatalf("second ensure: %v %v", created, err)
	}
	ci, err := AddToken(d, "ci", "", []string{"go", "team/*"})
	if err != nil || len(ci.Value) < 20 {
		t.Fatalf("add: %+v %v", ci, err)
	}
	if _, err := AddToken(d, "ci", "", nil); err == nil {
		t.Fatal("duplicate name must fail")
	}
	if _, err := AddToken(d, "dup", ci.Value, nil); err == nil {
		t.Fatal("duplicate value must fail")
	}
	if _, err := AddToken(d, "bad name", "", nil); err == nil {
		t.Fatal("bad name must fail")
	}
	if _, err := AddToken(d, "x", "has space", nil); err == nil {
		t.Fatal("bad value must fail")
	}
	if _, err := AddToken(d, "x", "", []string{"a/../b"}); err == nil {
		t.Fatal("bad scope must fail")
	}
	ts, _ = LoadTokens(d)
	got := ts.Lookup(ci.Value)
	if got == nil || got.Name != "ci" || !got.Allows("team/x") || got.Allows("other") || !got.Allows("go") {
		t.Fatalf("lookup: %+v", got)
	}
	if ts.Lookup("") != nil || ts.Lookup("nope") != nil {
		t.Fatal("lookup of unknown value")
	}
	def := ts.Find(DefaultTokenName)
	if def == nil || !def.Allows("other") {
		t.Fatal("default must be global")
	}
	rot, err := RotateToken(d, "ci")
	if err != nil || rot.Value == ci.Value || !slices.Equal(rot.Repos, []string{"go", "team/*"}) {
		t.Fatalf("rotate: %+v %v", rot, err)
	}
	if err := SetTokenValue(d, "ci", "custom-secret"); err != nil {
		t.Fatal(err)
	}
	if err := SetTokenValue(d, DefaultTokenName, "custom-secret"); err == nil {
		t.Fatal("value collision must fail")
	}
	if _, err := SetTokenRepos(d, "ci", nil); err != nil {
		t.Fatal(err)
	}
	ts, _ = LoadTokens(d)
	if !ts.Find("ci").Allows("other") || ts.Find("ci").Scope() != "all" {
		t.Fatal("scope reset")
	}
	if err := RemoveToken(d, "ci"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveToken(d, "ci"); err == nil {
		t.Fatal("removing twice must fail")
	}
	ts, _ = LoadTokens(d)
	if len(ts.Tokens) != 1 {
		t.Fatalf("tokens left: %d", len(ts.Tokens))
	}
	if _, err := os.Stat(filepath.Join(string(d), "http_token")); !os.IsNotExist(err) {
		t.Fatal("no legacy file expected")
	}
}
