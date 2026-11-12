// Package authkeys manages <data-dir>/authorized_keys, the OpenSSH-format list
// of client public keys accepted by the built-in SSH server. A key may carry
// a repository scope in the options field: repos="go,team/*" ssh-ed25519 ...
package authkeys

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"

	"git-mirror/internal/access"
	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
)

const reposOption = "repos="

// Key is one parsed line of authorized_keys.
type Key struct {
	Key     ssh.PublicKey
	Comment string
	Repos   []string // repository scope, empty = every mirror
	Options []string // other authorized_keys options, kept verbatim
	Line    string   // normalized '[options] type base64 comment' line
}

// enforcedOptions are OpenSSH options the built-in server already satisfies:
// it never grants a pty, forwarding or a user rc.
var enforcedOptions = map[string]bool{
	"restrict":            true,
	"no-pty":              true,
	"no-port-forwarding":  true,
	"no-agent-forwarding": true,
	"no-x11-forwarding":   true,
	"no-user-rc":          true,
}

// Unenforced returns the options the built-in server cannot honour, such as
// from=, expiry-time= or command=. A key carrying any of them is refused.
func (k Key) Unenforced() []string {
	var out []string
	for _, o := range k.Options {
		if !enforcedOptions[strings.ToLower(o)] {
			out = append(out, o)
		}
	}
	return out
}

// Fingerprint returns the SHA256 fingerprint ("SHA256:...").
func (k Key) Fingerprint() string { return ssh.FingerprintSHA256(k.Key) }

// Allows reports whether the key may access the mirror.
func (k Key) Allows(name string) bool { return access.Allowed(k.Repos, name) }

// Scope renders the repo list for tables ("all" when unrestricted).
func (k Key) Scope() string { return access.Label(k.Repos) }

// WithRepos returns a copy of the key with a new scope and re-rendered Line.
func (k Key) WithRepos(repos []string) Key {
	k.Repos = repos
	k.Line = k.format()
	return k
}

// format renders the authorized_keys line.
func (k Key) format() string {
	opts := append([]string(nil), k.Options...)
	if len(k.Repos) > 0 {
		opts = append(opts, reposOption+`"`+strings.Join(k.Repos, ",")+`"`)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.Key)))
	if len(opts) > 0 {
		line = strings.Join(opts, ",") + " " + line
	}
	if k.Comment != "" {
		line += " " + k.Comment
	}
	return line
}

// Matches reports whether s equals the fingerprint (with or without the
// "SHA256:" prefix) or the comment.
func (k Key) Matches(s string) bool {
	fp := k.Fingerprint()
	return s == fp || s == strings.TrimPrefix(fp, "SHA256:") || (k.Comment != "" && s == k.Comment)
}

// Parse parses one public key line, including an optional repos="..." scope
// in the options field.
func Parse(line string) (Key, error) {
	key, comment, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return Key{}, fmt.Errorf("invalid public key: %w", err)
	}
	k := Key{Key: key, Comment: comment}
	for _, o := range options {
		v, ok := strings.CutPrefix(o, reposOption)
		if !ok {
			k.Options = append(k.Options, o)
			continue
		}
		repos, err := access.ParseRepoList(strings.Trim(v, `"`))
		if err != nil {
			return Key{}, fmt.Errorf("invalid repos option: %w", err)
		}
		k.Repos = repos
	}
	k.Line = k.format()
	return k, nil
}

// Load parses the authorized_keys file. A missing file yields no keys.
func Load(d datadir.Dir) ([]Key, error) {
	path := d.AuthorizedKeysPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Key
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, err := Parse(line)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, k)
	}
	return out, sc.Err()
}

// Save writes the file atomically with mode 0600.
func Save(d datadir.Dir, keys []Key) error {
	var b strings.Builder
	b.WriteString("# managed by `git-mirror keys`, one OpenSSH public key per line\n")
	b.WriteString("# an optional repos=\"name,team/*\" option limits the key to those mirrors\n")
	for _, k := range keys {
		b.WriteString(k.Line)
		b.WriteByte('\n')
	}
	path := d.AuthorizedKeysPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Update performs a locked read-modify-write of the file.
func Update(d datadir.Dir, fn func([]Key) ([]Key, error)) error {
	if err := d.Ensure(); err != nil {
		return err
	}
	unlock, err := config.Lock(d)
	if err != nil {
		return err
	}
	defer unlock()
	keys, err := Load(d)
	if err != nil {
		return err
	}
	keys, err = fn(keys)
	if err != nil {
		return err
	}
	return Save(d, keys)
}

// Find returns the listed entry for key, or nil.
func Find(keys []Key, key ssh.PublicKey) *Key {
	want := key.Marshal()
	i := slices.IndexFunc(keys, func(k Key) bool { return bytes.Equal(k.Key.Marshal(), want) })
	if i < 0 {
		return nil
	}
	return &keys[i]
}

// Allowed reports whether key is listed.
func Allowed(keys []Key, key ssh.PublicKey) bool { return Find(keys, key) != nil }

// Add parses lines and appends the keys not yet present. A non-nil repos
// argument sets the scope of the added keys and re-scopes keys that are
// already present (reported as updated). With repos == nil a pasted line
// keeps the scope it carries.
func Add(d datadir.Dir, lines []string, repos []string) (added, updated, skipped []Key, err error) {
	if err := access.ValidateRepoPatterns(repos); err != nil {
		return nil, nil, nil, err
	}
	err = Update(d, func(keys []Key) ([]Key, error) {
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			k, err := Parse(l)
			if err != nil {
				return nil, err
			}
			if u := k.Unenforced(); len(u) > 0 {
				return nil, fmt.Errorf("unsupported option(s) %s, the built-in ssh server only supports repos= and no-* restrictions", strings.Join(u, ","))
			}
			if repos != nil {
				k = k.WithRepos(scopeOrNil(repos))
			}
			if cur := Find(keys, k.Key); cur != nil {
				if repos != nil && access.Label(cur.Repos) != access.Label(k.Repos) {
					*cur = cur.WithRepos(k.Repos)
					updated = append(updated, *cur)
				} else {
					skipped = append(skipped, *cur)
				}
				continue
			}
			keys = append(keys, k)
			added = append(added, k)
		}
		return keys, nil
	})
	return added, updated, skipped, err
}

// scopeOrNil turns an empty non-nil slice into nil so "all" is stored as no
// option at all.
func scopeOrNil(repos []string) []string {
	if len(repos) == 0 {
		return nil
	}
	return repos
}

// SetRepos changes the scope of the key matching selector (fingerprint or
// comment). Exactly one key must match.
func SetRepos(d datadir.Dir, selector string, repos []string) (Key, error) {
	if err := access.ValidateRepoPatterns(repos); err != nil {
		return Key{}, err
	}
	var out Key
	err := Update(d, func(keys []Key) ([]Key, error) {
		idx := -1
		for i, k := range keys {
			if k.Matches(selector) {
				if idx >= 0 {
					return nil, fmt.Errorf("%q matches more than one key, use the fingerprint", selector)
				}
				idx = i
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("no key matches %q", selector)
		}
		keys[idx] = keys[idx].WithRepos(scopeOrNil(repos))
		out = keys[idx]
		return keys, nil
	})
	return out, err
}

// Remove deletes every key matching one of the selectors (fingerprint or
// comment) and returns them. A selector matching nothing is an error.
func Remove(d datadir.Dir, selectors []string) (removed []Key, err error) {
	err = Update(d, func(keys []Key) ([]Key, error) {
		for _, sel := range selectors {
			kept := keys[:0:0]
			n := 0
			for _, k := range keys {
				if k.Matches(sel) {
					removed = append(removed, k)
					n++
					continue
				}
				kept = append(kept, k)
			}
			if n == 0 {
				return nil, fmt.Errorf("no key matches %q", sel)
			}
			keys = kept
		}
		return keys, nil
	})
	return removed, err
}

// LinesFromInput accepts a literal public key line or a path to a file
// (~ expanded) and returns its non-empty, non-comment lines.
func LinesFromInput(in string) ([]string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return nil, errors.New("public key or file path is required")
	}
	if IsKeyLine(in) {
		return []string{in}, nil
	}
	path := in
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + path[1:]
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("not a public key and cannot read file: %w", err)
	}
	var lines []string
	for l := range strings.SplitSeq(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%s contains no keys", path)
	}
	return lines, nil
}

// IsKeyLine reports whether s looks like an OpenSSH public key line.
func IsKeyLine(s string) bool {
	return strings.HasPrefix(s, "ssh-") || strings.HasPrefix(s, "ecdsa-") || strings.HasPrefix(s, "sk-")
}
