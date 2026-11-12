package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var segmentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var branchRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/@+*-]*$`)

// ValidateName checks that a repo name is a safe relative path
// ("host/owner/repo" style, no "..", no absolute components).
func ValidateName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if strings.HasSuffix(name, ".git") {
		return errors.New("name must not end with .git (it is appended automatically)")
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == "" || seg == "." || seg == ".." || !segmentRe.MatchString(seg) {
			return fmt.Errorf("invalid name %q: use letters, digits, '.', '_', '-' separated by '/'", name)
		}
	}
	return nil
}

// ValidateBranches checks branch names / patterns usable in a git refspec.
// A single "*" wildcard is allowed, e.g. "release/*".
func ValidateBranches(bs []string) error {
	for _, b := range bs {
		if b == "" || strings.Contains(b, "..") || strings.HasSuffix(b, "/") ||
			strings.Count(b, "*") > 1 || !branchRe.MatchString(b) {
			return fmt.Errorf("invalid branch pattern %q", b)
		}
	}
	return nil
}

// ParseBranches splits a comma/space separated list. "all" or "" means all.
func ParseBranches(s string) ([]string, error) {
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
	if err := ValidateBranches(out); err != nil {
		return nil, err
	}
	return out, nil
}
