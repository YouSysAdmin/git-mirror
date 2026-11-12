package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"git-mirror/internal/server"
	"git-mirror/pkg/version"
)

func cmdServer() *cobra.Command {
	var (
		listen    string
		sshListen string
		tick      time.Duration
		jsonLog   bool
		debug     bool
	)
	c := &cobra.Command{
		Use:     "server",
		Aliases: []string{"serve", "daemon"},
		Short:   "Run the synchronization server",
		Long: `Run the server that periodically mirrors the repositories listed in
<data-dir>/config.json into <data-dir>/repos and exposes a control API on
<data-dir>/server.sock for the CLI/TUI. Config changes made by the CLI/TUI are
picked up automatically.

Serving mirrors to the private network (both read-only):
  HTTP  git-mirror config set listen :8080      git clone http://<host>:8080/<name>.git
  SSH   git-mirror config set ssh_listen :2222  git clone ssh://git@<host>:2222/<name>.git
        client keys: git-mirror keys add ~/.ssh/id_ed25519.pub`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			level := slog.LevelInfo
			if debug {
				level = slog.LevelDebug
			}
			var h slog.Handler
			if jsonLog {
				h = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
			} else {
				h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
			}
			log := slog.New(h)
			srv := server.New(theApp.Dir, server.Options{Listen: listen, SSHListen: sshListen, Tick: tick, Version: version.Version}, log)
			err := srv.Run(cmd.Context())
			log.Info("git-mirror server stopped")
			return err
		},
	}
	f := c.Flags()
	f.StringVar(&listen, "listen", "", `git HTTP listen address, e.g. ":8080". Overrides the "listen" setting, "-" disables`)
	f.StringVar(&sshListen, "ssh-listen", "", `git SSH listen address, e.g. ":2222". Overrides the "ssh_listen" setting, "-" disables`)
	f.DurationVar(&tick, "tick", 15*time.Second, "scheduler check interval")
	f.BoolVar(&jsonLog, "json", false, "log in JSON")
	f.BoolVar(&debug, "debug", false, "debug logging")
	return c
}
