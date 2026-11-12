package authkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"git-mirror/internal/datadir"
)

func genKey(t *testing.T, comment string) (string, ssh.PublicKey) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(spub))) + " " + comment, spub
}

func TestAddRemove(t *testing.T) {
	dir := datadir.Dir(t.TempDir())
	a, apub := genKey(t, "alice@laptop")
	b, bpub := genKey(t, "bob@pc")

	added, updated, skipped, err := Add(dir, []string{a, "", "# comment", b, a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || len(updated) != 0 || len(skipped) != 1 {
		t.Fatalf("added=%d updated=%d skipped=%d", len(added), len(updated), len(skipped))
	}
	keys, err := Load(dir)
	if err != nil || len(keys) != 2 {
		t.Fatalf("load: %v, %d keys", err, len(keys))
	}
	if !Allowed(keys, apub) || !Allowed(keys, bpub) {
		t.Fatal("keys should be allowed")
	}
	if k := Find(keys, apub); k == nil || !k.Allows("anything") || k.Scope() != "all" {
		t.Fatalf("legacy line must be global: %+v", k)
	}
	_, other := genKey(t, "eve")
	if Allowed(keys, other) {
		t.Fatal("unknown key must not be allowed")
	}

	removed, err := Remove(dir, []string{"bob@pc"})
	if err != nil || len(removed) != 1 || removed[0].Comment != "bob@pc" {
		t.Fatalf("remove by comment: %v %+v", err, removed)
	}
	keys, _ = Load(dir)
	if Allowed(keys, bpub) || !Allowed(keys, apub) {
		t.Fatal("bob should be gone, alice kept")
	}
	fp := strings.TrimPrefix(keys[0].Fingerprint(), "SHA256:")
	if _, err := Remove(dir, []string{fp}); err != nil {
		t.Fatalf("remove by bare fingerprint: %v", err)
	}
	if _, err := Remove(dir, []string{"nobody"}); err == nil {
		t.Fatal("expected error for unknown selector")
	}
	if _, _, _, err := Add(dir, []string{"not a key"}, nil); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestScopes(t *testing.T) {
	dir := datadir.Dir(t.TempDir())
	a, apub := genKey(t, "alice@laptop")
	b, bpub := genKey(t, "bob@pc")

	// Scope from the option in the line itself, with another option kept.
	k, err := Parse(`no-pty,repos="go,team/*" ` + a)
	if err != nil {
		t.Fatal(err)
	}
	if k.Scope() != "go,team/*" || len(k.Options) != 1 || k.Options[0] != "no-pty" {
		t.Fatalf("parse: %+v", k)
	}
	if !strings.HasPrefix(k.Line, `no-pty,repos="go,team/*" ssh-ed25519 `) || !strings.HasSuffix(k.Line, " alice@laptop") {
		t.Fatalf("line: %q", k.Line)
	}
	if _, err := Parse(`repos="a/../b" ` + a); err == nil {
		t.Fatal("bad scope must fail")
	}

	// Add with an explicit scope, then round-trip through the file.
	added, _, _, err := Add(dir, []string{a}, []string{"team/*"})
	if err != nil || len(added) != 1 || added[0].Scope() != "team/*" {
		t.Fatalf("add scoped: %v %+v", err, added)
	}
	raw, _ := os.ReadFile(dir.AuthorizedKeysPath())
	if !strings.Contains(string(raw), `repos="team/*" ssh-ed25519`) {
		t.Fatalf("file: %s", raw)
	}
	keys, _ := Load(dir)
	got := Find(keys, apub)
	if got == nil || !got.Allows("team/x") || got.Allows("go") {
		t.Fatalf("after load: %+v", got)
	}

	// Re-adding with a different scope updates, with nil scope skips.
	_, updated, skipped, err := Add(dir, []string{a}, []string{"go"})
	if err != nil || len(updated) != 1 || len(skipped) != 0 || updated[0].Scope() != "go" {
		t.Fatalf("rescope: %v %+v %+v", err, updated, skipped)
	}
	_, updated, skipped, err = Add(dir, []string{a}, nil)
	if err != nil || len(updated) != 0 || len(skipped) != 1 {
		t.Fatalf("nil scope keeps: %v %+v %+v", err, updated, skipped)
	}
	// Empty non-nil scope means all.
	_, updated, _, err = Add(dir, []string{a}, []string{})
	if err != nil || len(updated) != 1 || updated[0].Scope() != "all" {
		t.Fatalf("reset to all: %v %+v", err, updated)
	}

	// SetRepos by comment.
	if _, _, _, err := Add(dir, []string{b}, nil); err != nil {
		t.Fatal(err)
	}
	set, err := SetRepos(dir, "bob@pc", []string{"go"})
	if err != nil || set.Scope() != "go" {
		t.Fatalf("set: %v %+v", err, set)
	}
	keys, _ = Load(dir)
	if k := Find(keys, bpub); k == nil || k.Allows("team/x") || !k.Allows("go") {
		t.Fatalf("bob after set: %+v", k)
	}
	if _, err := SetRepos(dir, "nobody", nil); err == nil {
		t.Fatal("unknown selector must fail")
	}
}

func TestUnenforcedOptionsRefused(t *testing.T) {
	dir := datadir.Dir(t.TempDir())
	line, _ := genKey(t, "carol@dummy")
	if _, _, _, err := Add(dir, []string{`from="192.0.2.1",expiry-time="200001010000" ` + line}, nil); err == nil {
		t.Fatal("from= and expiry-time= must be refused")
	}
	k, err := Parse(`restrict,no-pty,repos="go" ` + line)
	if err != nil || len(k.Unenforced()) != 0 {
		t.Fatalf("restrict and no-pty are enforced by the server: %v %v", err, k.Unenforced())
	}
	k, _ = Parse(`command="/bin/true" ` + line)
	if u := k.Unenforced(); len(u) != 1 {
		t.Fatalf("command= must be reported: %v", u)
	}
}
