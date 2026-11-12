// Package mirror wraps the git binary to create and refresh bare mirrors.
package mirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"git-mirror/internal/access"
)

// Result summarizes a successful sync.
type Result struct {
	Created   bool          // true when the mirror was cloned for the first time
	Head      string        // HEAD commit, empty for an empty repository
	Duration  time.Duration // wall time of the sync
	SizeBytes int64         // on-disk size of the mirror
}

// Options tune how git talks to the upstream.
type Options struct {
	// SSHKey, when set, is the private key used for ssh:// and scp-like
	// upstream URLs (passed as "ssh -i <key> -o IdentitiesOnly=yes").
	SSHKey string
	// KnownHosts, when set, replaces ~/.ssh/known_hosts (UserKnownHostsFile).
	KnownHosts string
	// ConfigFile, when set, replaces the user and system ssh_config (ssh -F).
	ConfigFile string
	// ProxyCommand, when set, is passed as "-o ProxyCommand=<value>". It is
	// executed by ssh through /bin/sh, so it must already be shell-quoted.
	ProxyCommand string
	// HTTPProxy, when set, is the proxy url (credentials included) handed to
	// git as http.proxy for http(s) upstreams.
	HTTPProxy string
	// HTTPProxyInsecure disables certificate checks of a https:// proxy.
	HTTPProxyInsecure bool
	// Branches limits mirrored branches (names or "prefix/*" patterns).
	// Empty mirrors every branch. Tags are always mirrored.
	Branches []string
}

// env returns the environment for upstream git commands.
func (o Options) env() []string {
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never block on a password prompt
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"LC_ALL=C",
	)
	if cmd := o.SSHCommand(); cmd != "" {
		env = append(env, "GIT_SSH_COMMAND="+cmd)
	}
	// Proxy settings go through the GIT_CONFIG_* environment instead of
	// "-c" arguments so credentials never show up in the process list.
	var cfg [][2]string
	if o.HTTPProxy != "" {
		cfg = append(cfg, [2]string{"http.proxy", o.HTTPProxy})
		if o.HTTPProxyInsecure {
			cfg = append(cfg, [2]string{"http.proxySSLVerify", "false"})
		}
	}
	if len(cfg) > 0 {
		env = append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(cfg)))
		for i, kv := range cfg {
			env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
		}
	}
	return env
}

// SSHCommand renders the GIT_SSH_COMMAND value for the options, or "" when
// nothing is configured and the plain ssh of the user should be used.
func (o Options) SSHCommand() string {
	if o.SSHKey == "" && o.KnownHosts == "" && o.ConfigFile == "" && o.ProxyCommand == "" {
		return ""
	}
	parts := []string{"ssh"}
	if o.ConfigFile != "" {
		parts = append(parts, "-F", shellQuote(o.ConfigFile))
	}
	if o.SSHKey != "" {
		parts = append(parts, "-i", shellQuote(o.SSHKey), "-o", "IdentitiesOnly=yes")
	}
	if o.KnownHosts != "" {
		parts = append(parts, "-o", shellQuote("UserKnownHostsFile="+o.KnownHosts))
	}
	if o.ProxyCommand != "" {
		parts = append(parts, "-o", shellQuote("ProxyCommand="+o.ProxyCommand))
	}
	parts = append(parts, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new")
	return strings.Join(parts, " ")
}

// refspecs returns the fetch refspecs implementing o.Branches.
func (o Options) refspecs() []string {
	if len(o.Branches) == 0 {
		return []string{"+refs/*:refs/*"} // everything, like clone --mirror
	}
	specs := make([]string, 0, len(o.Branches)+1)
	for _, b := range o.Branches {
		specs = append(specs, "+refs/heads/"+b+":refs/heads/"+b)
	}
	return append(specs, "+refs/tags/*:refs/tags/*")
}

// Sync creates dir as a bare mirror on first run and fetches with pruning
// afterward, so branches and tags deleted upstream disappear from the mirror
// too. With Options.Branches set only matching branches (plus all tags) are
// kept and branches that no longer match are removed. HEAD follows the upstream
// default branch when it is mirrored.
//
// It never prompts for credentials: configure a credential helper, SSH keys,
// or embed a token in the URL.
func Sync(ctx context.Context, dir, url string, o Options) (Result, error) {
	start := time.Now()
	var res Result

	target := dir
	if !exists(dir) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return res, err
		}
		// Populate a temp sibling so a failed first fetch never leaves a half mirror.
		target = dir + ".tmp-" + fmt.Sprint(start.UnixNano())
		defer func() { _ = os.RemoveAll(target) }()
		if _, err := git(ctx, nil, "", "init", "--bare", "--quiet", target); err != nil {
			return res, err
		}
		if _, err := git(ctx, nil, target, "remote", "add", "--mirror=fetch", "origin", url); err != nil {
			return res, err
		}
		res.Created = true
	}

	// Keep remote URL and refspecs in sync with the configuration.
	if _, err := git(ctx, nil, target, "remote", "set-url", "origin", url); err != nil {
		return res, err
	}
	for i, spec := range o.refspecs() {
		args := []string{"config", "--add", "remote.origin.fetch", spec}
		if i == 0 {
			args = []string{"config", "--replace-all", "remote.origin.fetch", spec}
		}
		if _, err := git(ctx, nil, target, args...); err != nil {
			return res, err
		}
	}
	if _, err := git(ctx, o.env(), target, "fetch", "--prune", "--prune-tags", "origin"); err != nil {
		return res, err
	}
	if len(o.Branches) > 0 {
		if err := pruneUnlistedBranches(ctx, target, o.Branches); err != nil {
			return res, err
		}
	}
	if err := updateHead(ctx, target, o); err != nil {
		return res, err
	}

	if target != dir {
		if err := os.Rename(target, dir); err != nil {
			return res, fmt.Errorf("move mirror into place: %w", err)
		}
	}
	if out, err := git(ctx, nil, dir, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		res.Head = strings.TrimSpace(out)
	}
	res.SizeBytes, _ = dirSize(dir)
	res.Duration = time.Since(start)
	return res, nil
}

// Branches lists the branch names present in the mirror.
func Branches(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, nil, dir, "for-each-ref", "--format=%(refname:strip=2)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// pruneUnlistedBranches deletes local branches not matching any pattern,
// e.g. after the branch filter of a repo was narrowed.
func pruneUnlistedBranches(ctx context.Context, dir string, patterns []string) error {
	branches, err := Branches(ctx, dir)
	if err != nil {
		return err
	}
	for _, b := range branches {
		if !access.Match(b, patterns) {
			if _, err := git(ctx, nil, dir, "update-ref", "-d", "refs/heads/"+b); err != nil {
				return err
			}
		}
	}
	return nil
}

// updateHead points HEAD at the upstream default branch when it is mirrored,
// otherwise at the first mirrored branch, so clones from the mirror work.
func updateHead(ctx context.Context, dir string, o Options) error {
	branches, err := Branches(ctx, dir)
	if err != nil {
		return err
	}
	if len(branches) == 0 {
		return nil // empty repository
	}
	want := ""
	if out, err := git(ctx, o.env(), dir, "ls-remote", "--symref", "origin", "HEAD"); err == nil {
		for line := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(line, "ref: refs/heads/") {
				want = strings.TrimPrefix(strings.Fields(line)[1], "refs/heads/")
				break
			}
		}
	}
	if want == "" || !slices.Contains(branches, want) {
		cur, _ := git(ctx, nil, dir, "symbolic-ref", "-q", "HEAD")
		cur = strings.TrimPrefix(strings.TrimSpace(cur), "refs/heads/")
		if slices.Contains(branches, cur) {
			return nil // current HEAD is still valid
		}
		want = branches[0]
	}
	_, err = git(ctx, nil, dir, "symbolic-ref", "HEAD", "refs/heads/"+want)
	return err
}

// Remove deletes a mirror directory and any empty parents up to root.
func Remove(root, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	PruneEmptyParents(root, dir)
	return nil
}

// PruneEmptyParents removes empty directories between dir (exclusive) and
// root (exclusive), e.g. "repos/github.com/owner" after its last mirror went.
func PruneEmptyParents(root, dir string) {
	for p := filepath.Dir(dir); p != root && strings.HasPrefix(p, root); p = filepath.Dir(p) {
		if os.Remove(p) != nil {
			return // not empty or gone
		}
	}
}
