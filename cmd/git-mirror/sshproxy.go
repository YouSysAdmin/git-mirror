package main

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	"git-mirror/internal/sshproxy"
)

// cmdSSHProxy is the built-in ProxyCommand used for the proxy setting:
//
//	ssh -o ProxyCommand='git-mirror ssh-proxy <url> %h %p [authfile]' ...
//
// It connects to the HTTP CONNECT or SOCKS5 proxy and pipes stdin/stdout.
func cmdSSHProxy() *cobra.Command {
	var (
		insecure bool
		timeout  time.Duration
	)
	c := &cobra.Command{
		Use:    "ssh-proxy <proxy-url> <host> <port> [<auth-file>]",
		Short:  "Connect to host:port through an HTTP CONNECT or SOCKS5 proxy (ssh ProxyCommand)",
		Hidden: true,
		Args:   cobra.RangeArgs(3, 4),
		// No data dir is needed, so skip the shared PersistentPreRunE work.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := sshproxy.ParseURL(args[0])
			if err != nil {
				return err
			}
			var auth *sshproxy.Auth
			if len(args) == 4 {
				if auth, err = sshproxy.ReadAuthFile(args[3]); err != nil {
					return err
				}
			} else if v := os.Getenv("GIT_MIRROR_PROXY_AUTH"); v != "" {
				if auth, err = sshproxy.ParseAuth(v); err != nil {
					return err
				}
			}
			dctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			conn, err := sshproxy.Dial(dctx, p, args[1], args[2], auth, insecure)
			cancel()
			if err != nil {
				return err
			}
			return sshproxy.Run(cmd.Context(), os.Stdin, os.Stdout, conn)
		},
	}
	c.Flags().BoolVar(&insecure, "insecure", false, "skip certificate verification for https:// proxies")
	c.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "connection timeout")
	// Errors go to stderr, which ssh shows to git, so they end up in state.json.
	c.SilenceUsage = true
	return c
}
