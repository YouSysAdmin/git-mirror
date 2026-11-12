package mirror

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// ShellQuote wraps s in single quotes for /bin/sh.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellQuote(s string) string { return ShellQuote(s) }

// exists reports whether dir already contains a git repository.
func exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "HEAD"))
	return err == nil
}

// Version returns the git version string, or an error if git is unavailable.
func Version(ctx context.Context) (string, error) {
	out, err := git(ctx, nil, "", "version")
	return strings.TrimSpace(out), err
}

func git(ctx context.Context, env []string, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env == nil {
		env = Options{}.env()
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return stdout.String(), fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		msg := lastLines(stderr.String(), 3)
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var keep, fatal []string
	for _, l := range lines {
		l = strings.TrimSpace(StripControl(l))
		switch {
		case l == "",
			strings.HasPrefix(l, "Cloning into"),
			strings.HasPrefix(l, "Fetching origin"),
			strings.Contains(l, "%"): // progress noise
			continue
		}
		// Keep git errors and the messages of a git-mirror server or proxy on
		// the other end, which explain why the fetch failed.
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "git-mirror") {
			fatal = append(fatal, l)
		}
		keep = append(keep, l)
	}
	if len(fatal) > 0 {
		keep = fatal
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, " | ")
}

// StripControl removes control characters (terminal escapes included), except
// tab, from text that comes from an upstream, so it is safe to print.
func StripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// RedactURL hides the password of a URL with userinfo, for logs.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	return u.Redacted()
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort size, skip unreadable entries
		}
		if !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total, err
}
