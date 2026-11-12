package mirror

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var scpLike = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)

// IsSSHURL reports whether git would reach the upstream over ssh: an
// ssh:// url or the scp-like git@host:path form. Everything else (http(s),
// git://, file://, local paths) does not use ssh keys.
func IsSSHURL(raw string) bool {
	u := strings.TrimSpace(raw)
	if before, _, ok := strings.Cut(u, "://"); ok {
		return strings.EqualFold(before, "ssh")
	}
	if filepath.IsAbs(u) || strings.HasPrefix(u, ".") {
		return false
	}
	return scpLike.MatchString(u)
}

// NameFromURL derives a mirror name like "github.com/owner/repo" from a URL.
// Supports https://, ssh://, git://, file paths and scp-like git@host:path.
func NameFromURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", errors.New("empty url")
	}
	var host, path string
	if before, after, ok := strings.Cut(u, "://"); ok {
		rest := after
		if strings.EqualFold(before, "file") {
			return "local/" + strings.Trim(strings.TrimSuffix(strings.Trim(rest, "/"), ".git"), "/"), nil
		}
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if slash := strings.Index(rest, "/"); slash >= 0 {
			host, path = rest[:slash], rest[slash+1:]
		} else {
			host = rest
		}
		if c := strings.Index(host, ":"); c >= 0 {
			host = host[:c] // strip port
		}
	} else if m := scpLike.FindStringSubmatch(u); m != nil {
		host, path = m[1], m[2]
	} else {
		// Local path.
		path = strings.Trim(filepath.ToSlash(u), "/")
		host = "local"
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return "", fmt.Errorf("cannot derive repository name from %q", raw)
	}
	if host == "" {
		return path, nil
	}
	return strings.ToLower(host) + "/" + path, nil
}
