// Package access implements repository scopes shared by HTTP tokens and SSH
// client keys: a list of mirror names or "*" patterns. An empty scope grants
// access to every mirror.
package access

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var segmentRe = regexp.MustCompile(`^[A-Za-z0-9._*-]+$`)

// Match reports whether name equals one of the patterns or fits a pattern
// with a single "*" wildcard. The wildcard stands for any (possibly empty,
// possibly slash-containing) substring.
func Match(name string, patterns []string) bool {
	for _, p := range patterns {
		pre, suf, ok := strings.Cut(p, "*")
		if !ok {
			if name == p {
				return true
			}
			continue
		}
		if len(name) >= len(pre)+len(suf) && strings.HasPrefix(name, pre) && strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

// Allowed reports whether scope grants access to the mirror. An empty scope
// grants access to everything.
func Allowed(scope []string, name string) bool {
	return len(scope) == 0 || Match(name, scope)
}

// ValidateRepoPatterns checks mirror names and patterns usable in a scope.
func ValidateRepoPatterns(ps []string) error {
	for _, p := range ps {
		if p == "" || strings.Contains(p, "..") || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") ||
			strings.Count(p, "*") > 1 || strings.HasSuffix(p, ".git") {
			return fmt.Errorf("invalid repository pattern %q", p)
		}
		for seg := range strings.SplitSeq(p, "/") {
			if !segmentRe.MatchString(seg) {
				return fmt.Errorf("invalid repository pattern %q: use letters, digits, '.', '_', '-', '*' separated by '/'", p)
			}
		}
	}
	return nil
}

// ParseRepoList splits a comma/space separated list of names and patterns.
// "", "all" and "*" mean every mirror (nil scope).
func ParseRepoList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "all" || s == "*" {
		return nil, nil
	}
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty repository list")
	}
	if err := ValidateRepoPatterns(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Label renders a scope for tables: "all" or the comma-joined list.
func Label(scope []string) string {
	if len(scope) == 0 {
		return "all"
	}
	return strings.Join(scope, ",")
}
