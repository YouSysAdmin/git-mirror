package tlsconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/go-tlsutils"

	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

func TestMapAndValidate(t *testing.T) {
	d := datadir.Dir(t.TempDir())
	var set config.Settings
	if err := Validate(set, d); err != nil {
		t.Fatalf("mode none must validate: %v", err)
	}
	set.TLS.Mode = "self"
	set.PublicHost = "mirror.internal"
	c := Map(set, d)
	if c.FQDN != "mirror.internal" || c.Alg != tlsutils.AlgECDSA || !strings.HasPrefix(c.CacheDir, string(d)) {
		t.Fatalf("unexpected mapping: %+v", c)
	}
	if err := Validate(set, d); err != nil {
		t.Fatal(err)
	}
	set.TLS.Mode = "manual"
	if err := Validate(set, d); err == nil {
		t.Fatal("manual without cert/key must fail")
	}
	set.TLS.Mode = "bogus"
	if err := Validate(set, d); err == nil {
		t.Fatal("unknown mode must fail")
	}
}

func TestSelfSignedCertRoundTrip(t *testing.T) {
	d := datadir.Dir(t.TempDir())
	set := config.Settings{PublicHost: "m.local", TLS: config.TLSSettings{Mode: "self"}}
	if _, err := tlsutils.Build(Map(set, d)); err != nil {
		t.Fatal(err)
	}
	pemData, err := ServingCertPEM(set, d)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := LeafPEM(pemData)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := Fingerprint(leaf)
	if err != nil || len(fp) != 95 {
		t.Fatalf("fingerprint %q %v", fp, err)
	}
}

func TestServingCertPEMDropsPrivateKey(t *testing.T) {
	d := datadir.Dir(t.TempDir())
	set := config.Settings{PublicHost: "m.local", TLS: config.TLSSettings{Mode: "self"}}
	if _, err := tlsutils.Build(Map(set, d)); err != nil {
		t.Fatal(err)
	}
	certPEM, err := os.ReadFile(SelfSignedCertPath(d))
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(d.CertsDir(), "selfsigned-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	// A combined cert+key file used in manual mode.
	combined := filepath.Join(t.TempDir(), "combined.pem")
	if err := os.WriteFile(combined, append(append([]byte{}, certPEM...), keyPEM...), 0o600); err != nil {
		t.Fatal(err)
	}
	set.TLS = config.TLSSettings{Mode: "manual", Cert: combined, Key: combined}
	out, err := ServingCertPEM(set, d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "PRIVATE KEY") || !strings.Contains(string(out), "BEGIN CERTIFICATE") {
		t.Fatalf("served pem:\n%s", out)
	}
}
