// Package identity manages <data-dir>/identities, the private keys used to
// fetch ssh:// upstreams, and turns repo settings into mirror.Options.
package identity

import (
	"cmp"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"

	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

// DefaultKeyword is the CLI value meaning "use the global setting".
const DefaultKeyword = "default"

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Identity is one stored private key.
type Identity struct {
	Name        string
	Type        string // ssh key type, e.g. ssh-ed25519
	Fingerprint string // SHA256:...
	PublicLine  string // "type base64 comment"
	Comment     string
}

// ValidateName checks an identity name (one file name under identities/).
func ValidateName(name string) error {
	if name == "" {
		return errors.New("identity name is required")
	}
	if name == DefaultKeyword {
		return fmt.Errorf("%q is reserved", name)
	}
	if strings.HasSuffix(name, ".pub") || !nameRe.MatchString(name) {
		return fmt.Errorf("invalid identity name %q: use letters, digits, '.', '_', '-'", name)
	}
	return nil
}

// List returns the stored identities sorted by name. A missing .pub file is
// regenerated from the private key.
func List(d datadir.Dir) ([]Identity, error) {
	entries, err := os.ReadDir(d.IdentitiesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Identity
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".pub") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id, err := Get(d, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b Identity) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// Get loads one identity by name.
func Get(d datadir.Dir, name string) (Identity, error) {
	if err := ValidateName(name); err != nil {
		return Identity{}, err
	}
	priv, err := os.ReadFile(d.IdentityPath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Identity{}, fmt.Errorf("unknown ssh identity %q", name)
		}
		return Identity{}, err
	}
	pubLine, err := os.ReadFile(d.IdentityPubPath(name))
	if err != nil {
		signer, perr := ssh.ParsePrivateKey(priv)
		if perr != nil {
			return Identity{}, fmt.Errorf("identity %s: %w", name, perr)
		}
		pubLine = ssh.MarshalAuthorizedKey(signer.PublicKey())
		_ = os.WriteFile(d.IdentityPubPath(name), pubLine, 0o644)
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey(pubLine)
	if err != nil {
		return Identity{}, fmt.Errorf("identity %s: bad public key: %w", name, err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		line += " " + comment
	}
	return Identity{Name: name, Type: pub.Type(), Fingerprint: ssh.FingerprintSHA256(pub), PublicLine: line, Comment: comment}, nil
}

// GenerateED25519PEM creates a new ed25519 key pair in OpenSSH PEM format.
func GenerateED25519PEM(comment string) (pemBytes []byte, pub ssh.PublicKey, err error) {
	pubRaw, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, nil, err
	}
	pub, err = ssh.NewPublicKey(pubRaw)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(block), pub, nil
}

// Generate creates a new ed25519 identity. An empty comment defaults to
// "git-mirror-<name>@<hostname>".
func Generate(d datadir.Dir, name, comment string) (Identity, error) {
	if err := ValidateName(name); err != nil {
		return Identity{}, err
	}
	if comment == "" {
		host, _ := os.Hostname()
		comment = "git-mirror-" + name + "@" + cmp.Or(host, "git-mirror")
	}
	pemBytes, pub, err := GenerateED25519PEM(comment)
	if err != nil {
		return Identity{}, err
	}
	return store(d, name, pemBytes, pub, comment)
}

// Import copies an existing private key file into the store. Keys with a
// passphrase are rejected because ssh runs with BatchMode=yes.
func Import(d datadir.Dir, name, path string) (Identity, error) {
	if err := ValidateName(name); err != nil {
		return Identity{}, err
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, rest)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, err
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); ok {
			return Identity{}, errors.New("identity must not have a passphrase (ssh runs with BatchMode=yes)")
		}
		return Identity{}, fmt.Errorf("not a private key: %w", err)
	}
	comment := ""
	if pubRaw, err := os.ReadFile(path + ".pub"); err == nil {
		if _, c, _, _, err := ssh.ParseAuthorizedKey(pubRaw); err == nil {
			comment = c
		}
	}
	return store(d, name, raw, signer.PublicKey(), comment)
}

// store writes the private and public key files, refusing to overwrite.
func store(d datadir.Dir, name string, priv []byte, pub ssh.PublicKey, comment string) (Identity, error) {
	if err := d.Ensure(); err != nil {
		return Identity{}, err
	}
	unlock, err := config.Lock(d)
	if err != nil {
		return Identity{}, err
	}
	defer unlock()
	if err := os.MkdirAll(d.IdentitiesDir(), 0o700); err != nil {
		return Identity{}, err
	}
	f, err := os.OpenFile(d.IdentityPath(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return Identity{}, fmt.Errorf("identity %q already exists", name)
		}
		return Identity{}, err
	}
	if _, err := f.Write(priv); err != nil {
		_ = f.Close()
		_ = os.Remove(d.IdentityPath(name))
		return Identity{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(d.IdentityPath(name))
		return Identity{}, err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		line += " " + comment
	}
	if err := os.WriteFile(d.IdentityPubPath(name), []byte(line+"\n"), 0o644); err != nil {
		_ = os.Remove(d.IdentityPath(name))
		return Identity{}, err
	}
	return Identity{Name: name, Type: pub.Type(), Fingerprint: ssh.FingerprintSHA256(pub), PublicLine: line, Comment: comment}, nil
}

// UsedBy lists what references the identity: repo names and "settings" for
// the global default.
func UsedBy(cfg *config.Config, name string) []string {
	var out []string
	if cfg.Settings.SSHKey == name {
		out = append(out, "settings")
	}
	for _, r := range cfg.Repos {
		if r.SSHKey == name {
			out = append(out, r.Name)
		}
	}
	return out
}

// Remove deletes an identity. Unless force is set it refuses while the
// identity is referenced by the settings or a repo.
func Remove(d datadir.Dir, cfg *config.Config, name string, force bool) error {
	if _, err := Get(d, name); err != nil {
		return err
	}
	if used := UsedBy(cfg, name); len(used) > 0 && !force {
		return fmt.Errorf("identity %q is used by %s (pass --force to remove anyway)", name, strings.Join(used, ", "))
	}
	unlock, err := config.Lock(d)
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.Remove(d.IdentityPath(name)); err != nil {
		return err
	}
	_ = os.Remove(d.IdentityPubPath(name))
	return nil
}
