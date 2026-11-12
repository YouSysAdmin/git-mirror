package datadir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketFallbackDirIsPrivate(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	d := Dir("/" + strings.Repeat("d", 120))
	sock := d.SocketPath()
	if filepath.Dir(sock) != socketFallbackDir() {
		t.Fatalf("long data dir must use the fallback dir: %s", sock)
	}
	if err := d.CheckSocketDir(false); err == nil {
		t.Fatal("missing fallback dir must fail without create")
	}
	if err := d.CheckSocketDir(true); err != nil {
		t.Fatal(err)
	}
	// A directory another user could write into is refused.
	if err := os.Chmod(socketFallbackDir(), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := d.CheckSocketDir(false); err == nil {
		t.Fatal("group or world accessible socket dir must be refused")
	}
	short := Dir(t.TempDir())
	if err := short.CheckSocketDir(false); err != nil || filepath.Dir(short.SocketPath()) != string(short) {
		t.Fatalf("socket in the data dir needs no check: %v", err)
	}
}
