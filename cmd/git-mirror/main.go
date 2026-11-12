// git-mirror is the CLI/TUI for managing mirrored repositories.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"git-mirror/internal/app"
	"git-mirror/internal/config"
	"git-mirror/internal/datadir"
	"git-mirror/internal/mirror"
	"git-mirror/pkg/version"
)

var (
	flagDataDir string
	theApp      *app.App
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	root := &cobra.Command{
		Use:   "git-mirror",
		Short: "Mirror git repositories into a private network",
		Long: `git-mirror mirrors remote git repositories into <data-dir>/repos and keeps
them in sync.

  git-mirror server   runs the sync server (systemd service, container, ...)
  git-mirror tui      opens the interactive UI (default when run in a terminal)
  git-mirror add|rm|ls|sync|...   manage the list from scripts

Server and CLI/TUI share <data-dir>/config.json and the server picks up changes
automatically. When the server is not running, "sync" runs git in this process.`,
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := datadir.Resolve(flagDataDir)
			if err != nil {
				return err
			}
			theApp = app.New(dir)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isatty.IsTerminal(os.Stdout.Fd()) {
				return cmd.Help()
			}
			return runTUI(cmd.Context(), theApp)
		},
	}
	root.PersistentFlags().StringVarP(&flagDataDir, "data-dir", "d", "",
		"data directory (default $"+datadir.EnvVar+" or ~/.local/share/git-mirror)")

	root.AddCommand(
		cmdInit(), cmdServer(), cmdTUI(),
		cmdAdd(), cmdRemove(), cmdList(), cmdSync(), cmdStatus(),
		cmdEnable(true), cmdEnable(false), cmdEdit(), cmdConfig(), cmdKeys(), cmdToken(), cmdIdentity(), cmdSSHConfig(),
		cmdCert(), cmdURL(), cmdPath(), cmdSSHProxy(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return root.ExecuteContext(ctx)
}

// ---------------------------------------------------------------------------
// add / rm
// ---------------------------------------------------------------------------

func cmdAdd() *cobra.Command {
	var (
		name     string
		interval time.Duration
		branches string
		disabled bool
		noSync   bool
		sshKey   string
		proxy    string
	)
	c := &cobra.Command{
		Use:   "add <url> [<url>...]",
		Short: "Register repositories to mirror and sync them",
		Long: `Register repositories and run the first sync. By default every branch and
tag is mirrored and anything deleted upstream is deleted from the mirror on
the next sync. Use --branches to mirror only some branches (tags are always
mirrored). Branches that stop matching are removed from the mirror.`,
		Example: `  git-mirror add https://github.com/golang/go.git                 # mirrored as "go" (see name_style)
  git-mirror add git@github.com:org/private.git --name org/private --interval 1h
  git-mirror add git@github.com:org/private.git --ssh-key deploy       # identity from: git-mirror identity gen deploy
  git-mirror add https://github.com/kubernetes/kubernetes.git --branches master,release-1.*
  git-mirror add https://gitlab.com/a/b https://gitlab.com/a/c --no-sync`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name != "" && len(args) > 1 {
				return errors.New("--name can only be used with a single url")
			}
			bs, err := config.ParseBranches(branches)
			if err != nil {
				return err
			}
			var added []string
			for _, u := range args {
				r, err := theApp.Add(cmd.Context(), u, app.AddOptions{Name: name, Interval: interval, Branches: bs, Disabled: disabled, SSHKey: sshKey, Proxy: proxy})
				if err != nil {
					return err
				}
				fmt.Printf("added %s  <- %s\n", r.Name, r.URL)
				added = append(added, r.Name)
			}
			if noSync || disabled {
				return nil
			}
			return doSync(cmd.Context(), added, false)
		},
	}
	c.Flags().StringVarP(&name, "name", "n", "", `mirror name (default derived from url per the name_style setting: "go", "golang/go" or "github.com/golang/go")`)
	c.Flags().DurationVarP(&interval, "interval", "i", 0, "per-repo sync interval (default: global setting)")
	c.Flags().StringVarP(&branches, "branches", "b", "", `branches to mirror, comma separated, "*" glob allowed (default: all)`)
	c.Flags().BoolVar(&disabled, "disabled", false, "register without periodic sync")
	c.Flags().BoolVar(&noSync, "no-sync", false, "do not trigger the initial sync")
	c.Flags().StringVar(&sshKey, "ssh-key", "", "identity name (git-mirror identity ls) or key path for ssh:// upstreams (default: ssh_key setting)")
	c.Flags().StringVar(&proxy, "proxy", "", `proxy url for fetching this upstream (http://, https://, socks5://, socks5h://) or "none" (default: proxy setting)`)
	return c
}

func cmdRemove() *cobra.Command {
	var purge bool
	c := &cobra.Command{
		Use:     "rm <name> [<name>...]",
		Aliases: []string{"remove", "del"},
		Short:   "Unregister repositories (use --purge to also delete mirror files)",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, n := range args {
				if err := theApp.Remove(cmd.Context(), n, purge); err != nil {
					return err
				}
				if purge {
					fmt.Printf("removed %s (mirror deleted)\n", n)
				} else {
					fmt.Printf("removed %s (files kept in %s)\n", n, theApp.Dir.RepoPath(n))
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&purge, "purge", false, "delete the mirror directory as well")
	return c
}

// ---------------------------------------------------------------------------
// ls / status
// ---------------------------------------------------------------------------

func cmdList() *cobra.Command {
	var long bool
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List repositories and their sync state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := theApp.Status(cmd.Context())
			if err != nil {
				return err
			}
			if len(st.Repos) == 0 {
				fmt.Println("no repositories. Add one with: git-mirror add <url>")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			if long {
				_, _ = fmt.Fprintln(w, "NAME\tENABLED\tINTERVAL\tBRANCHES\tKEY\tSTATUS\tLAST SUCCESS\tNEXT\tSIZE\tHEAD\tURL\tERROR")
			} else {
				_, _ = fmt.Fprintln(w, "NAME\tENABLED\tINTERVAL\tSTATUS\tLAST SUCCESS\tURL")
			}
			for _, r := range st.Repos {
				status := string(r.State.Status)
				if r.InFlight {
					status = "syncing"
				}
				if long {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						r.Repo.Name, yesNo(r.Repo.Enabled), r.Repo.EffectiveInterval(st.Settings), branchesLabel(r.Repo.Branches), orDash(r.Repo.EffectiveSSHKey(st.Settings)), status,
						ago(r.State.LastSuccess), in(r.NextDue), humanSize(r.State.SizeBytes), shortSHA(r.State.Head),
						r.Repo.URL, mirror.StripControl(r.State.LastError))
				} else {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
						r.Repo.Name, yesNo(r.Repo.Enabled), r.Repo.EffectiveInterval(st.Settings), status,
						ago(r.State.LastSuccess), r.Repo.URL)
				}
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVarP(&long, "long", "l", false, "show more columns")
	return c
}

func cmdStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show server status and settings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := theApp.Status(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Printf("data dir:  %s\n", st.Server.DataDir)
			if st.Server.Running {
				fmt.Printf("server:    running (pid %d, version %s, up %s, %d queued)\n",
					st.Server.PID, st.Server.Version, time.Since(st.Server.Started).Round(time.Second), st.Server.Queued)
			} else {
				fmt.Println("server:    not running (start with: git-mirror server)")
			}
			fmt.Printf("interval:  %s\ntimeout:   %s\nworkers:   %d\n",
				st.Settings.SyncInterval, st.Settings.SyncTimeout, st.Settings.Workers)
			switch {
			case st.Server.Running && st.Server.GitHTTP != "":
				scheme := "http"
				if st.Server.GitHTTPS {
					scheme = "https, tls " + st.Settings.TLS.Mode
				}
				fmt.Printf("git http:  %s (%s, token auth, list with: git-mirror token ls)\n", st.Server.GitHTTP, scheme)
			case !st.Server.Running && st.Settings.Listen != "":
				fmt.Printf("git http:  %s (when server runs)\n", st.Settings.Listen)
			default:
				fmt.Println("git http:  disabled (git-mirror config set listen :8080)")
			}
			switch {
			case st.Server.Running && st.Server.GitSSH != "":
				fmt.Printf("git ssh:   %s\n", st.Server.GitSSH)
			case !st.Server.Running && st.Settings.SSHListen != "":
				fmt.Printf("git ssh:   %s (when server runs)\n", st.Settings.SSHListen)
			default:
				fmt.Println("git ssh:   disabled (git-mirror config set ssh_listen :2222)")
			}
			var ok, failed, never, syncing int
			for _, r := range st.Repos {
				switch {
				case r.InFlight || r.State.Status == config.StatusSyncing:
					syncing++
				case r.State.Status == config.StatusOK:
					ok++
				case r.State.Status == config.StatusError:
					failed++
				default:
					never++
				}
			}
			fmt.Printf("repos:     %d total, %d ok, %d failed, %d syncing, %d never synced\n",
				len(st.Repos), ok, failed, syncing, never)
			return nil
		},
	}
}

func cmdInit() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the data directory and the initial config.json",
		Long: `Create the data directory and write the initial config.json (HTTP on
:8080, SSH on :2222) unless it exists already. An existing config is left
untouched, so it is safe to run before every server start.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			created, err := theApp.Init()
			if err != nil {
				return err
			}
			if created {
				fmt.Println("created", theApp.Dir.ConfigPath())
			} else {
				fmt.Println("exists", theApp.Dir.ConfigPath())
			}
			return nil
		},
	}
}

func cmdPath() *cobra.Command {
	return &cobra.Command{
		Use:   "path [<name>]",
		Short: "Print the data directory, or the mirror path of a repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Println(theApp.Dir)
				return nil
			}
			fmt.Println(theApp.Dir.RepoPath(args[0]))
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// sync
// ---------------------------------------------------------------------------

func cmdSync() *cobra.Command {
	var local bool
	c := &cobra.Command{
		Use:   "sync [<name>...]",
		Short: "Synchronize repositories now (all enabled when no names given)",
		Long: `Synchronize repositories now. If the server is running the request is
queued there and this command returns immediately. Otherwise (or with --local)
git runs in this process and the command waits for completion.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSync(cmd.Context(), args, local)
		},
	}
	c.Flags().BoolVar(&local, "local", false, "run git here even if the server is running")
	return c
}

func doSync(ctx context.Context, names []string, local bool) error {
	viaServer, err := theApp.Sync(ctx, names, app.SyncOptions{
		Local: local,
		Progress: func(e app.SyncEvent) {
			switch e.Phase {
			case app.PhaseQueued:
				if e.Err != nil {
					fmt.Printf("  %-50s %s\n", e.Name, e.Err)
				} else {
					fmt.Printf("  %-50s queued\n", e.Name)
				}
			case app.PhaseStart:
				fmt.Printf("  %-50s syncing...", e.Name)
			case app.PhaseDone:
				what := "updated"
				if e.Result.Created {
					what = "cloned"
				}
				fmt.Printf(" %s %s in %s\n", what, shortSHA(e.Result.Head), e.Result.Duration.Round(time.Millisecond))
			case app.PhaseError:
				fmt.Printf(" FAILED: %v\n", e.Err)
			}
		},
	})
	if viaServer && err == nil {
		fmt.Println("queued on the server. Follow progress with: git-mirror ls")
	}
	return err
}

// ---------------------------------------------------------------------------
// enable / disable / edit / config
// ---------------------------------------------------------------------------

func cmdEnable(enable bool) *cobra.Command {
	use, short := "enable", "Enable periodic sync for repositories"
	if !enable {
		use, short = "disable", "Disable periodic sync for repositories (mirror is kept)"
	}
	return &cobra.Command{
		Use:   use + " <name> [<name>...]",
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, n := range args {
				if err := theApp.SetEnabled(cmd.Context(), n, enable); err != nil {
					return err
				}
				fmt.Printf("%s %s\n", use+"d", n)
			}
			return nil
		},
	}
}

func cmdEdit() *cobra.Command {
	var (
		url      string
		interval string
		branches string
		sshKey   string
		proxy    string
	)
	c := &cobra.Command{
		Use:   "edit <name>",
		Short: "Change the url, interval, branch filter, ssh key or proxy of a repository",
		Example: `  git-mirror edit github.com/golang/go --interval 1h
  git-mirror edit github.com/golang/go --interval default
  git-mirror edit github.com/golang/go --branches master,release-branch.*
  git-mirror edit github.com/golang/go --branches all
  git-mirror edit github.com/golang/go --url https://mirror.example/golang/go.git
  git-mirror edit org/private --ssh-key deploy          # identity from the data dir, "default" resets
  git-mirror edit org/private --proxy socks5://proxy:1080       # or "none" / "default"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var o app.EditOptions
			if cmd.Flags().Changed("url") {
				o.URL = &url
			}
			if cmd.Flags().Changed("interval") {
				var d time.Duration
				if interval != "default" && interval != "0" {
					var err error
					if d, err = time.ParseDuration(interval); err != nil {
						return fmt.Errorf("invalid interval %q", interval)
					}
				}
				o.Interval = &d
			}
			if cmd.Flags().Changed("branches") {
				bs, err := config.ParseBranches(branches)
				if err != nil {
					return err
				}
				if bs == nil {
					bs = []string{}
				}
				o.Branches = &bs
			}
			if cmd.Flags().Changed("ssh-key") {
				o.SSHKey = &sshKey
			}
			if cmd.Flags().Changed("proxy") {
				o.Proxy = &proxy
			}
			if o.URL == nil && o.Interval == nil && o.Branches == nil && o.SSHKey == nil && o.Proxy == nil {
				return errors.New("nothing to change: pass --url, --interval, --branches, --ssh-key and/or --proxy")
			}
			if err := theApp.Edit(cmd.Context(), args[0], o); err != nil {
				return err
			}
			fmt.Println("updated", args[0])
			if o.Branches != nil {
				fmt.Println("the branch filter is applied on the next sync: git-mirror sync", args[0])
			}
			return nil
		},
	}
	c.Flags().StringVar(&url, "url", "", "new upstream url")
	c.Flags().StringVar(&interval, "interval", "", `new sync interval (e.g. 30m, 2h) or "default"`)
	c.Flags().StringVar(&branches, "branches", "", `branches to mirror, comma separated with "*" globs, or "all"`)
	c.Flags().StringVar(&sshKey, "ssh-key", "", `identity name or key path for ssh:// upstreams, "default" uses the global setting`)
	c.Flags().StringVar(&proxy, "proxy", "", `proxy url for fetching this upstream, "none" disables, "default" uses the global setting`)
	return c
}

func cmdConfig() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Show or change global settings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(theApp.Dir)
			if err != nil {
				return err
			}
			fmt.Printf("file:      %s\n", theApp.Dir.ConfigPath())
			fmt.Printf("interval:           %s\ntimeout:            %s\nworkers:            %d\nname_style:         %s\n",
				cfg.Settings.SyncInterval, cfg.Settings.SyncTimeout, cfg.Settings.Workers, cmp.Or(cfg.Settings.NameStyle, config.NameStyleRepo))
			fmt.Printf("listen (http):      %s\npublic_host:        %s\n", orDash(cfg.Settings.Listen), orDash(cfg.Settings.PublicHost))
			t := cfg.Settings.TLS
			fmt.Printf("tls_mode:           %s\n", orDash(t.Mode))
			switch t.Mode {
			case "manual":
				fmt.Printf("tls_cert / tls_key: %s / %s (watch: %v)\n", t.Cert, t.Key, t.Watch)
			case "self":
				fmt.Printf("tls_fqdn / tls_alg: %s / %s (cache: %s)\n", orDash(t.FQDN), orDash(t.Alg), theApp.Dir.CertsDir())
			case "acme":
				fmt.Printf("tls_acme:           %s hosts=%s http=%s\n", orDash(t.ACMEEmail), strings.Join(t.ACMEHosts, ","), orDash(t.ACMEHTTPAddr))
			}
			fmt.Printf("ssh_listen:         %s\nssh_allow_any_key:  %v\n", orDash(cfg.Settings.SSHListen), cfg.Settings.SSHAllowAnyKey)
			fmt.Printf("ssh_key (upstream): %s\n", sshKeyLabel(cfg.Settings.SSHKey))
			fmt.Printf("proxy (upstream):   %s (insecure: %v, auth file: %s)\n", orDash(cfg.Settings.Proxy), cfg.Settings.ProxyInsecure, presentLabel(theApp.HasProxyAuth()))
			fmt.Printf("ssh_config:         %s (%s)\n", presentLabel(theApp.HasSSHConfig()), theApp.Dir.SSHConfigPath())
			return nil
		},
	}
	c.AddCommand(&cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a setting: interval, timeout, workers, listen, public_host, tls_*, ssh_listen, ssh_allow_any_key, ssh_key, proxy*",
		Example: `  git-mirror config set interval 30m
  git-mirror config set workers 4
  git-mirror config set name_style owner       # derive names as owner/repo instead of repo
  git-mirror config set listen :8080          # serve mirrors over git http (token: git-mirror token)
  git-mirror config set public_host mirror.internal   # host name shown in clone urls
  git-mirror config set tls_mode self         # https with a self-signed cert (git-mirror cert)
  git-mirror config set tls_mode manual && git-mirror config set tls_cert /etc/ssl/m.crt && git-mirror config set tls_key /etc/ssl/m.key
  git-mirror config set tls_mode acme && git-mirror config set tls_acme_hosts mirror.example.com
  git-mirror config set ssh_listen :2222      # serve mirrors over ssh (keys: git-mirror keys add)
  git-mirror config set ssh_allow_any_key true
  git-mirror config set ssh_key deploy        # identity for ssh:// upstreams (git-mirror identity gen deploy), or a key path
  git-mirror config set proxy http://proxy.internal:8080        # for http(s) and ssh upstreams, or socks5://host:1080, "none" clears
  git-mirror config set proxy_auth user:password                # stored in <data-dir>/proxy_auth (0600)
  git-mirror config set listen ""             # disable`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := theApp.SetSetting(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			if args[0] == "proxy_auth" {
				fmt.Println("proxy_auth =", presentLabel(args[1] != ""))
				return nil
			}
			fmt.Printf("%s = %s\n", args[0], orDash(args[1]))
			if args[0] == "proxy" {
				if warn := app.ProxyWarning(args[1]); warn != "" {
					fmt.Println("note:", warn)
				}
			}
			if args[0] == "workers" || args[0] == "listen" || args[0] == "ssh_listen" || strings.HasPrefix(args[0], "tls_") {
				fmt.Println("note: restart the server for this setting to take effect")
			}
			return nil
		},
	})
	return c
}

func cmdTUI() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive terminal UI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd.Context(), theApp)
		},
	}
}
