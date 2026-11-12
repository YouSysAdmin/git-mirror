package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

func TestGenerateListRemove(t *testing.T) {
	d := datadir.Dir(t.TempDir())
	ids, err := List(d)
	if err != nil || len(ids) != 0 {
		t.Fatalf("empty list: %v %v", ids, err)
	}
	id, err := Generate(d, "deploy", "")
	if err != nil {
		t.Fatal(err)
	}
	if id.Type != "ssh-ed25519" || !strings.HasPrefix(id.Fingerprint, "SHA256:") || !strings.Contains(id.PublicLine, "git-mirror-deploy@") {
		t.Fatalf("identity: %+v", id)
	}
	fi, _ := os.Stat(d.IdentityPath("deploy"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode %v", fi.Mode())
	}
	di, _ := os.Stat(d.IdentitiesDir())
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode())
	}
	if _, err := Generate(d, "deploy", ""); err == nil {
		t.Fatal("second generate must fail")
	}
	for _, bad := range []string{"", "default", "x.pub", "a/b", ".hidden"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
	// The .pub file is regenerated when missing.
	_ = os.Remove(d.IdentityPubPath("deploy"))
	got, err := Get(d, "deploy")
	if err != nil || got.Fingerprint != id.Fingerprint {
		t.Fatalf("regenerate pub: %+v %v", got, err)
	}
	ids, _ = List(d)
	if len(ids) != 1 || ids[0].Name != "deploy" {
		t.Fatalf("list: %+v", ids)
	}

	cfg := &config.Config{Settings: config.Settings{SSHKey: "deploy"}, Repos: []config.Repo{{Name: "x", SSHKey: "deploy"}}}
	if used := UsedBy(cfg, "deploy"); len(used) != 2 {
		t.Fatalf("used by: %v", used)
	}
	if err := Remove(d, cfg, "deploy", false); err == nil {
		t.Fatal("remove while used must fail")
	}
	if err := Remove(d, cfg, "deploy", true); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(d, "deploy"); err == nil {
		t.Fatal("removed identity still there")
	}
	if err := Remove(d, cfg, "deploy", true); err == nil {
		t.Fatal("removing twice must fail")
	}
}

func TestImport(t *testing.T) {
	d := datadir.Dir(t.TempDir())
	pemBytes, pub, err := GenerateED25519PEM("laptop")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(src, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(src+".pub", ssh.MarshalAuthorizedKey(pub)[:len(ssh.MarshalAuthorizedKey(pub))-1], 0o644)
	id, err := Import(d, "laptop", src)
	if err != nil || id.Fingerprint != ssh.FingerprintSHA256(pub) {
		t.Fatalf("import: %+v %v", id, err)
	}
	// Bytes are copied verbatim.
	got, _ := os.ReadFile(d.IdentityPath("laptop"))
	if string(got) != string(pemBytes) {
		t.Fatal("private key altered")
	}
	// Encrypted keys are rejected.
	encPEM := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAACmFlczI1Ni1jdHIAAAAGYmNyeXB0AAAAGAAAABBhYWFhYWFh\nYWFhYWFhYWFhAAAAEAAAAAEAAAAzAAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAA\nAAAAAAAAAAAAAAAAAAAAAAAAAAAAoAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n-----END OPENSSH PRIVATE KEY-----\n")
	enc := filepath.Join(t.TempDir(), "enc")
	_ = os.WriteFile(enc, encPEM, 0o600)
	if _, err := Import(d, "enc", enc); err == nil {
		t.Fatal("encrypted or invalid key must be rejected")
	}
	if _, err := Import(d, "nokey", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file must fail")
	}
}

func TestResolveAndSyncOptions(t *testing.T) {
	d := datadir.Dir(t.TempDir())
	if _, err := Generate(d, "deploy", ""); err != nil {
		t.Fatal(err)
	}
	executable = func() (string, error) { return "/opt/git mirror/git-mirror", nil }
	defer func() { executable = os.Executable }()

	key, kh, err := Resolve(d, "")
	if err != nil || key != "" || kh != "" {
		t.Fatalf("empty: %q %q %v", key, kh, err)
	}
	key, kh, err = Resolve(d, "deploy")
	if err != nil || key != d.IdentityPath("deploy") || kh != d.KnownHostsPath() {
		t.Fatalf("name: %q %q %v", key, kh, err)
	}
	if _, _, err := Resolve(d, "missing"); err == nil {
		t.Fatal("unknown identity must fail")
	}
	legacy := filepath.Join(t.TempDir(), "key")
	_ = os.WriteFile(legacy, []byte("x"), 0o600)
	key, kh, err = Resolve(d, legacy)
	if err != nil || key != legacy || kh != "" {
		t.Fatalf("path: %q %q %v", key, kh, err)
	}
	if _, _, err := Resolve(d, filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing path must fail")
	}

	set := config.Settings{SSHKey: "deploy", Proxy: "http://proxy:8080"}
	o, err := SyncOptions(d, config.Repo{Name: "a", Branches: []string{"main"}}, set)
	if err != nil {
		t.Fatal(err)
	}
	if o.SSHKey != d.IdentityPath("deploy") || o.KnownHosts != d.KnownHostsPath() || o.ConfigFile != "" || len(o.Branches) != 1 {
		t.Fatalf("options: %+v", o)
	}
	want := "'/opt/git mirror/git-mirror' ssh-proxy 'http://proxy:8080' %h %p"
	if o.ProxyCommand != want {
		t.Fatalf("proxy command %q", o.ProxyCommand)
	}
	if o.HTTPProxy != "http://proxy:8080" || o.HTTPProxyInsecure {
		t.Fatalf("http proxy %q insecure %v", o.HTTPProxy, o.HTTPProxyInsecure)
	}
	// Repo overrides: proxy off, legacy key path, and insecure flag plus auth file.
	o, err = SyncOptions(d, config.Repo{Name: "a", Proxy: config.ProxyNone, SSHKey: legacy}, set)
	if err != nil || o.ProxyCommand != "" || o.SSHKey != legacy || o.KnownHosts != "" {
		t.Fatalf("repo overrides: %+v %v", o, err)
	}
	_ = os.WriteFile(d.ProxyAuthPath(), []byte("u:p"), 0o600)
	_ = os.WriteFile(d.SSHConfigPath(), []byte("Host *\n"), 0o600)
	set.ProxyInsecure = true
	o, err = SyncOptions(d, config.Repo{Name: "a", Proxy: "socks5://p:1080"}, config.Settings{ProxyInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if o.ConfigFile != d.SSHConfigPath() || o.KnownHosts != d.KnownHostsPath() || o.SSHKey != "" {
		t.Fatalf("ssh_config: %+v", o)
	}
	if !strings.Contains(o.ProxyCommand, "--insecure 'socks5://p:1080' %h %p '"+d.ProxyAuthPath()+"'") {
		t.Fatalf("proxy command %q", o.ProxyCommand)
	}
	// The auth file is merged into the url git receives as http.proxy.
	if o.HTTPProxy != "socks5://u:p@p:1080" || !o.HTTPProxyInsecure {
		t.Fatalf("http proxy %q insecure %v", o.HTTPProxy, o.HTTPProxyInsecure)
	}
	if _, err := SyncOptions(d, config.Repo{Name: "a", SSHKey: "ghost"}, config.Settings{}); err == nil {
		t.Fatal("unknown identity must fail")
	}
}
