package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"git-mirror/internal/config"
	"git-mirror/internal/identity"
)

// cmdIdentity manages the private keys used to fetch ssh:// upstreams.
func cmdIdentity() *cobra.Command {
	c := &cobra.Command{
		Use:   "identity",
		Short: "Manage private SSH keys for fetching upstreams (stored in the data dir)",
		Long: `Identities are private keys kept in <data-dir>/identities, used instead of
the ~/.ssh of the user running the server when fetching ssh:// upstreams.
Set a global default with "config set ssh_key <name>" or pick one per
repository with "add/edit --ssh-key <name>". Host keys are collected in
<data-dir>/known_hosts. Keys must not have a passphrase.`,
		Example: `  git-mirror identity gen deploy                # prints the public key, paste it as a deploy key
  git-mirror edit org/private --ssh-key deploy
  git-mirror identity add laptop ~/.ssh/id_ed25519
  git-mirror config set ssh_key laptop           # default for every ssh:// upstream`,
	}
	var comment string
	gen := &cobra.Command{
		Use:   "gen <name>",
		Short: "Generate a new ed25519 identity and print its public key",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := identity.Generate(theApp.Dir, args[0], comment)
			if err != nil {
				return err
			}
			fmt.Printf("generated identity %s (%s)\n\n%s\n\n", id.Name, id.Fingerprint, id.PublicLine)
			fmt.Printf("add this public key to the upstream (GitHub: repository settings, Deploy keys), then:\n  git-mirror edit <name> --ssh-key %s      or      git-mirror config set ssh_key %s\n", id.Name, id.Name)
			return nil
		},
	}
	gen.Flags().StringVar(&comment, "comment", "", "public key comment (default: git-mirror-<name>@<host>)")

	var fingerprint bool
	pub := &cobra.Command{
		Use:   "pub <name>",
		Short: "Print the public key of an identity",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, err := identity.Get(theApp.Dir, args[0])
			if err != nil {
				return err
			}
			if fingerprint {
				fmt.Println(id.Fingerprint)
			} else {
				fmt.Println(id.PublicLine)
			}
			return nil
		},
	}
	pub.Flags().BoolVar(&fingerprint, "fingerprint", false, "print the SHA256 fingerprint instead")

	var force bool
	rm := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "del"},
		Short:   "Delete an identity (refused while a repo or the settings use it)",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := config.Load(theApp.Dir)
			if err != nil {
				return err
			}
			if err := identity.Remove(theApp.Dir, cfg, args[0], force); err != nil {
				return err
			}
			fmt.Println("removed identity", args[0])
			return nil
		},
	}
	rm.Flags().BoolVar(&force, "force", false, "remove even when referenced")

	c.AddCommand(
		&cobra.Command{
			Use:     "ls",
			Aliases: []string{"list"},
			Short:   "List identities",
			RunE: func(*cobra.Command, []string) error {
				ids, err := identity.List(theApp.Dir)
				if err != nil {
					return err
				}
				if len(ids) == 0 {
					fmt.Println("no identities. Create one with: git-mirror identity gen <name>")
					return nil
				}
				cfg, err := config.Load(theApp.Dir)
				if err != nil {
					return err
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "NAME\tTYPE\tFINGERPRINT\tCOMMENT\tUSED BY")
				for _, id := range ids {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id.Name, id.Type, id.Fingerprint, id.Comment, orDash(strings.Join(identity.UsedBy(cfg, id.Name), ",")))
				}
				return w.Flush()
			},
		},
		gen,
		&cobra.Command{
			Use:   "add <name> <private-key-file>",
			Short: "Import an existing private key (without passphrase)",
			Args:  cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, args []string) error {
				id, err := identity.Import(theApp.Dir, args[0], args[1])
				if err != nil {
					return err
				}
				fmt.Printf("imported identity %s (%s)\n%s\n", id.Name, id.Fingerprint, id.PublicLine)
				return nil
			},
		},
		pub,
		rm,
		&cobra.Command{
			Use:   "default [<name>|none]",
			Short: "Show or set the global default identity (same as: config set ssh_key)",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					cfg, err := config.Load(theApp.Dir)
					if err != nil {
						return err
					}
					fmt.Println(orDash(cfg.Settings.SSHKey))
					return nil
				}
				v := args[0]
				if v == "none" {
					v = ""
				}
				if err := theApp.SetSetting(cmd.Context(), "ssh_key", v); err != nil {
					return err
				}
				fmt.Println("ssh_key =", orDash(v))
				return nil
			},
		},
	)
	return c
}
