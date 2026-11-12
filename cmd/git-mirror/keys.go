package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"git-mirror/internal/access"
	"git-mirror/internal/authkeys"
)

// cmdKeys manages <data-dir>/authorized_keys for the built-in SSH server.
func cmdKeys() *cobra.Command {
	c := &cobra.Command{
		Use:   "keys",
		Short: "Manage client public keys allowed to clone over the built-in SSH server",
		Long: `Manage <data-dir>/authorized_keys, the list of public keys accepted by
"git-mirror server" on its SSH endpoint (settings.ssh_listen). A key may be
limited to some mirrors with --repos (names or "*" patterns), stored as an
OpenSSH option: repos="go,team/*" ssh-ed25519 AAAA... comment
Changes take effect immediately, no restart needed.`,
	}
	var repos string
	add := &cobra.Command{
		Use:   "add <public-key-file | 'ssh-ed25519 AAAA... comment' | ->...",
		Short: "Add public keys (from files, literal lines, or stdin with -)",
		Example: `  git-mirror keys add ~/.ssh/id_ed25519.pub
  git-mirror keys add "ssh-ed25519 AAAAC3Nza... alice@laptop" --repos team-a/*
  curl -s https://github.com/alice.keys | git-mirror keys add -`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var lines []string
			for _, a := range args {
				if a == "-" {
					b, err := io.ReadAll(os.Stdin)
					if err != nil {
						return err
					}
					lines = append(lines, strings.Split(string(b), "\n")...)
					continue
				}
				ls, err := authkeys.LinesFromInput(a)
				if err != nil {
					return err
				}
				lines = append(lines, ls...)
			}
			var scope []string
			if cmd.Flags().Changed("repos") {
				var err error
				if scope, err = access.ParseRepoList(repos); err != nil {
					return err
				}
				if scope == nil {
					scope = []string{} // explicit "all" re-scopes existing keys
				}
				warnUnknownRepos(scope)
			}
			added, updated, skipped, err := authkeys.Add(theApp.Dir, lines, scope)
			if err != nil {
				return err
			}
			for _, k := range added {
				fmt.Printf("added %s %s (repos: %s)\n", k.Fingerprint(), k.Comment, k.Scope())
			}
			for _, k := range updated {
				fmt.Printf("updated %s %s (repos: %s)\n", k.Fingerprint(), k.Comment, k.Scope())
			}
			for _, k := range skipped {
				fmt.Printf("already present %s %s\n", k.Fingerprint(), k.Comment)
			}
			return nil
		},
	}
	add.Flags().StringVar(&repos, "repos", "", `mirrors the keys may access, comma separated, "*" globs allowed (default: all)`)

	var editRepos string
	edit := &cobra.Command{
		Use:   "edit <fingerprint | comment> --repos <list|all>",
		Short: "Change the mirrors a key may access",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("repos") {
				return errors.New("pass --repos <list> or --repos all")
			}
			scope, err := access.ParseRepoList(editRepos)
			if err != nil {
				return err
			}
			warnUnknownRepos(scope)
			k, err := authkeys.SetRepos(theApp.Dir, args[0], scope)
			if err != nil {
				return err
			}
			fmt.Printf("%s %s: repos = %s\n", k.Fingerprint(), k.Comment, k.Scope())
			return nil
		},
	}
	edit.Flags().StringVar(&editRepos, "repos", "", `mirrors the key may access, comma separated, "*" globs allowed, or "all"`)

	c.AddCommand(
		&cobra.Command{
			Use:     "ls",
			Aliases: []string{"list"},
			Short:   "List authorized keys",
			RunE: func(*cobra.Command, []string) error {
				keys, err := authkeys.Load(theApp.Dir)
				if err != nil {
					return err
				}
				if len(keys) == 0 {
					fmt.Printf("no keys in %s. Add one with: git-mirror keys add ~/.ssh/id_ed25519.pub\n", theApp.Dir.AuthorizedKeysPath())
					return nil
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "TYPE\tFINGERPRINT\tREPOS\tCOMMENT")
				for _, k := range keys {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.Key.Type(), k.Fingerprint(), k.Scope(), k.Comment)
				}
				return w.Flush()
			},
		},
		add,
		edit,
		&cobra.Command{
			Use:   "rm <fingerprint | comment>...",
			Short: "Remove keys by SHA256 fingerprint or comment",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				removed, err := authkeys.Remove(theApp.Dir, args)
				if err != nil {
					return err
				}
				for _, k := range removed {
					fmt.Printf("removed %s %s\n", k.Fingerprint(), k.Comment)
				}
				return nil
			},
		},
	)
	return c
}
