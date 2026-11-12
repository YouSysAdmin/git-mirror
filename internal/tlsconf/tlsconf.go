// Package tlsconf maps the git-mirror TLS settings onto go-tlsutils.
package tlsconf

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yousysadmin/go-tlsutils"

	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

// Modes accepted in settings.tls.mode.
var Modes = []string{tlsutils.ModeNone, tlsutils.ModeManual, tlsutils.ModeSelf, tlsutils.ModeACME}

// Algs accepted in settings.tls.alg.
var Algs = []string{tlsutils.AlgRSA, tlsutils.AlgECDSA, tlsutils.AlgEd25519}

// Map converts settings into a tlsutils.Config. FQDN for self-signed mode
// falls back to public_host and then to the machine host name.
func Map(set config.Settings, d datadir.Dir) tlsutils.Config {
	t := set.TLS
	fqdn := strings.TrimSpace(t.FQDN)
	if fqdn == "" {
		fqdn = set.PublicHost
	}
	if fqdn == "" {
		fqdn, _ = os.Hostname()
	}
	alg := t.Alg
	if alg == "" {
		alg = tlsutils.AlgECDSA // small certs, universally supported by git/curl
	}
	return tlsutils.Config{
		Mode:     t.Mode,
		Cert:     t.Cert,
		Key:      t.Key,
		Watch:    t.Watch,
		FQDN:     fqdn,
		Alg:      alg,
		CacheDir: d.CertsDir(),
		ACME: tlsutils.ACMEBlock{
			Email:    t.ACMEEmail,
			CacheDir: filepath.Join(d.CertsDir(), "acme"),
			HTTPAddr: t.ACMEHTTPAddr,
			Hosts:    t.ACMEHosts,
		},
	}
}

// Validate checks the settings without touching the network or disk.
func Validate(set config.Settings, d datadir.Dir) error {
	return Map(set, d).Validate()
}

// SelfSignedCertPath is where go-tlsutils caches the self-signed certificate.
func SelfSignedCertPath(d datadir.Dir) string {
	return filepath.Join(d.CertsDir(), "selfsigned-cert.pem")
}

// ServingCertPEM returns the PEM of the certificate the HTTP endpoint presents
// (self-signed or manual mode), so clients can pin it. Empty in other modes.
// Only CERTIFICATE blocks are returned, the output is served without a token.
func ServingCertPEM(set config.Settings, d datadir.Dir) ([]byte, error) {
	var path string
	switch set.TLS.Mode {
	case tlsutils.ModeSelf:
		path = SelfSignedCertPath(d)
	case tlsutils.ModeManual:
		path = set.TLS.Cert
	default:
		return nil, errors.New("no static certificate in tls mode " + orNone(set.TLS.Mode))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return CertsOnly(data)
}

// CertsOnly keeps the CERTIFICATE blocks of a PEM bundle and drops everything
// else (private keys, parameters).
func CertsOnly(bundle []byte) ([]byte, error) {
	var out []byte
	for rest := bundle; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			out = append(out, pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: block.Bytes})...)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no certificate found in pem data")
	}
	return out, nil
}

// LeafPEM extracts the first certificate of a PEM bundle (the leaf).
func LeafPEM(bundle []byte) ([]byte, error) {
	for rest := bundle; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, errors.New("no certificate found in pem data")
		}
		if block.Type == "CERTIFICATE" {
			return pem.EncodeToMemory(block), nil
		}
	}
}

// Fingerprint returns the SHA-256 fingerprint of the first certificate in the
// PEM data, formatted like OpenSSL ("AB:CD:...").
func Fingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("invalid pem")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c.Raw)
	return strings.ReplaceAll(fmt.Sprintf("% X", sum[:]), " ", ":"), nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
