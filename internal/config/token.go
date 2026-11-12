package config

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"git-mirror/internal/access"
	"git-mirror/internal/datadir"
)

// The git HTTP endpoint is protected by named access tokens stored in
// <data-dir>/tokens.json (mode 0600), outside config.json so the world
// readable config never contains a secret. Every token carries an optional
// repository scope. Older data dirs have a single <data-dir>/http_token,
// which is read as the token "default" until the first write migrates it.

// DefaultTokenName is the token created on first start and the target of the
// legacy migration.
const DefaultTokenName = "default"

// TokensVersion is the format version of tokens.json.
const TokensVersion = 1

// Token is one named access token.
type Token struct {
	Name    string    `json:"name"`
	Value   string    `json:"value"`
	Repos   []string  `json:"repos,omitempty"` // empty = every mirror
	Created time.Time `json:"created"`
}

// Tokens is the content of tokens.json.
type Tokens struct {
	Version int     `json:"version"`
	Tokens  []Token `json:"tokens"`
	legacy  bool    // loaded from http_token, not yet written as tokens.json
}

// Allows reports whether the token may access the mirror.
func (t Token) Allows(name string) bool { return access.Allowed(t.Repos, name) }

// Scope renders the repo list for tables ("all" when unrestricted).
func (t Token) Scope() string { return access.Label(t.Repos) }

// Find returns the token with the given name, or nil.
func (ts *Tokens) Find(name string) *Token {
	i := slices.IndexFunc(ts.Tokens, func(t Token) bool { return t.Name == name })
	if i < 0 {
		return nil
	}
	return &ts.Tokens[i]
}

// Lookup returns the token with the given value, or nil. Every entry is
// compared in constant time so the timing does not reveal a partial match.
func (ts *Tokens) Lookup(value string) *Token {
	if value == "" {
		return nil
	}
	var found *Token
	for i := range ts.Tokens {
		if subtle.ConstantTimeCompare([]byte(ts.Tokens[i].Value), []byte(value)) == 1 {
			found = &ts.Tokens[i]
		}
	}
	return found
}

// Legacy reports whether the tokens came from the old http_token file.
func (ts *Tokens) Legacy() bool { return ts.legacy }

// GenerateToken returns a random URL-safe token value.
func GenerateToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ValidateTokenName checks a token name (one path segment, safe in URLs).
func ValidateTokenName(name string) error {
	if name == "" {
		return errors.New("token name is required")
	}
	if !segmentRe.MatchString(name) {
		return fmt.Errorf("invalid token name %q: use letters, digits, '.', '_', '-'", name)
	}
	return nil
}

// ValidateTokenValue checks a token value can be embedded in a clone URL.
func ValidateTokenValue(v string) error {
	if v == "" {
		return errors.New("token must not be empty")
	}
	if strings.ContainsAny(v, " \t\r\n:@/") {
		return errors.New("token must not contain whitespace or : @ /")
	}
	return nil
}

// LoadTokens reads tokens.json. Without it the legacy http_token (if any) is
// returned as the token "default" with access to every mirror. Nothing is
// written, so this is safe on the request path.
func LoadTokens(d datadir.Dir) (*Tokens, error) {
	ts := &Tokens{Version: TokensVersion}
	err := readJSON(d.TokensPath(), ts)
	if err == nil {
		if ts.Version == 0 {
			ts.Version = TokensVersion
		}
		return ts, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load tokens: %w", err)
	}
	b, err := os.ReadFile(d.HTTPTokenPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ts, nil
		}
		return nil, fmt.Errorf("read http token: %w", err)
	}
	if v := strings.TrimSpace(string(b)); v != "" {
		fi, _ := os.Stat(d.HTTPTokenPath())
		var created time.Time
		if fi != nil {
			created = fi.ModTime().UTC()
		}
		ts.Tokens = []Token{{Name: DefaultTokenName, Value: v, Created: created}}
		ts.legacy = true
	}
	return ts, nil
}

// SaveTokens writes tokens.json atomically with mode 0600 and retires the
// legacy http_token file. Callers should hold the lock.
func SaveTokens(d datadir.Dir, ts *Tokens) error {
	ts.Version = TokensVersion
	if ts.Tokens == nil {
		ts.Tokens = []Token{}
	}
	if err := writeJSONMode(d.TokensPath(), ts, 0o600); err != nil {
		return fmt.Errorf("save tokens: %w", err)
	}
	ts.legacy = false
	if _, err := os.Stat(d.HTTPTokenPath()); err == nil {
		if err := os.Rename(d.HTTPTokenPath(), d.HTTPTokenPath()+".migrated"); err != nil {
			return fmt.Errorf("retire http_token: %w", err)
		}
	}
	return nil
}

// UpdateTokens performs a locked read-modify-write of tokens.json.
func UpdateTokens(d datadir.Dir, fn func(*Tokens) error) (*Tokens, error) {
	if err := d.Ensure(); err != nil {
		return nil, err
	}
	unlock, err := Lock(d)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ts, err := LoadTokens(d)
	if err != nil {
		return nil, err
	}
	if err := fn(ts); err != nil {
		return nil, err
	}
	if err := SaveTokens(d, ts); err != nil {
		return nil, err
	}
	return ts, nil
}

// EnsureTokens makes sure at least one token exists: it migrates a legacy
// http_token and otherwise generates "default". The result reports whether a
// new token was created.
func EnsureTokens(d datadir.Dir) (created bool, err error) {
	_, err = UpdateTokens(d, func(ts *Tokens) error {
		if len(ts.Tokens) > 0 {
			return nil
		}
		v, err := GenerateToken()
		if err != nil {
			return err
		}
		ts.Tokens = append(ts.Tokens, Token{Name: DefaultTokenName, Value: v, Created: time.Now().UTC()})
		created = true
		return nil
	})
	return created, err
}

// AddToken creates a token. An empty value generates a random one.
func AddToken(d datadir.Dir, name, value string, repos []string) (Token, error) {
	if err := ValidateTokenName(name); err != nil {
		return Token{}, err
	}
	if err := access.ValidateRepoPatterns(repos); err != nil {
		return Token{}, err
	}
	if value == "" {
		var err error
		if value, err = GenerateToken(); err != nil {
			return Token{}, err
		}
	} else if err := ValidateTokenValue(value); err != nil {
		return Token{}, err
	}
	tok := Token{Name: name, Value: value, Repos: repos, Created: time.Now().UTC()}
	_, err := UpdateTokens(d, func(ts *Tokens) error {
		if ts.Find(name) != nil {
			return fmt.Errorf("token %q already exists", name)
		}
		if ts.Lookup(value) != nil {
			return errors.New("another token already has this value")
		}
		ts.Tokens = append(ts.Tokens, tok)
		return nil
	})
	return tok, err
}

// RemoveToken deletes a token by name.
func RemoveToken(d datadir.Dir, name string) error {
	_, err := UpdateTokens(d, func(ts *Tokens) error {
		i := slices.IndexFunc(ts.Tokens, func(t Token) bool { return t.Name == name })
		if i < 0 {
			return fmt.Errorf("token not found: %s", name)
		}
		ts.Tokens = slices.Delete(ts.Tokens, i, i+1)
		return nil
	})
	return err
}

// RotateToken replaces the value of a token, keeping its scope.
func RotateToken(d datadir.Dir, name string) (Token, error) {
	var out Token
	_, err := UpdateTokens(d, func(ts *Tokens) error {
		t := ts.Find(name)
		if t == nil {
			return fmt.Errorf("token not found: %s", name)
		}
		v, err := GenerateToken()
		if err != nil {
			return err
		}
		t.Value = v
		out = *t
		return nil
	})
	return out, err
}

// SetTokenValue sets an explicit value on a token.
func SetTokenValue(d datadir.Dir, name, value string) error {
	if err := ValidateTokenValue(value); err != nil {
		return err
	}
	_, err := UpdateTokens(d, func(ts *Tokens) error {
		t := ts.Find(name)
		if t == nil {
			return fmt.Errorf("token not found: %s", name)
		}
		if other := ts.Lookup(value); other != nil && other.Name != name {
			return errors.New("another token already has this value")
		}
		t.Value = value
		return nil
	})
	return err
}

// SetTokenRepos changes the scope of a token (nil = every mirror).
func SetTokenRepos(d datadir.Dir, name string, repos []string) (Token, error) {
	if err := access.ValidateRepoPatterns(repos); err != nil {
		return Token{}, err
	}
	var out Token
	_, err := UpdateTokens(d, func(ts *Tokens) error {
		t := ts.Find(name)
		if t == nil {
			return fmt.Errorf("token not found: %s", name)
		}
		t.Repos = repos
		out = *t
		return nil
	})
	return out, err
}
