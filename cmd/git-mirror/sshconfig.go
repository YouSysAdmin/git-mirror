package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

const sshConfigHeader = `# OpenSSH client config used by git-mirror when fetching ssh:// upstreams.
# While this file exists it is passed as "ssh -F", so ~/.ssh/config and
# /etc/ssh/ssh_config of the service user are ignored. Example:
#
# Host github.com
#     ProxyCommand corkscrew-go proxy http://proxy.internal:8080 %h %p
#
# Keys and known_hosts are managed by git-mirror (identities/, known_hosts).
`

// cmdSSHConfig manages <data-dir>/ssh_config.
func cmdSSHConfig() *cobra.Command {
	c := &cobra.Command{
		Use:   "sshconfig",
		Short: "Show or edit the OpenSSH client config used for upstream fetches",
		Long: `<data-dir>/ssh_config is an ordinary OpenSSH client config. When it exists the
server runs "ssh -F <data-dir>/ssh_config" for ssh:// upstreams, so proxies
(ProxyCommand), ciphers or per-host options live in the data dir instead of
~/.ssh/config. A ProxyCommand from the proxy setting overrides the file.`,
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Print the file",
			RunE: func(*cobra.Command, []string) error {
				b, err := os.ReadFile(theApp.Dir.SSHConfigPath())
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						fmt.Printf("no ssh_config (%s). Create it with: git-mirror sshconfig edit\n", theApp.Dir.SSHConfigPath())
						return nil
					}
					return err
				}
				_, err = os.Stdout.Write(b)
				return err
			},
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Open the file in $EDITOR (created with a commented header on first use)",
			RunE: func(cmd *cobra.Command, _ []string) error {
				path := theApp.Dir.SSHConfigPath()
				if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
					if err := theApp.Dir.Ensure(); err != nil {
						return err
					}
					if err := os.WriteFile(path, []byte(sshConfigHeader), 0o600); err != nil {
						return err
					}
				}
				editor := cmp.Or(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
				ed := exec.CommandContext(cmd.Context(), "sh", "-c", editor+` "$0"`, path)
				ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := ed.Run(); err != nil {
					return fmt.Errorf("%s: %w", editor, err)
				}
				fmt.Println("saved", path, "(used on the next sync)")
				return nil
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Print the file path",
			RunE: func(*cobra.Command, []string) error {
				fmt.Println(theApp.Dir.SSHConfigPath())
				return nil
			},
		},
		&cobra.Command{
			Use:   "rm",
			Short: "Delete the file (ssh falls back to the user config)",
			RunE: func(*cobra.Command, []string) error {
				if err := os.Remove(theApp.Dir.SSHConfigPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				fmt.Println("removed", theApp.Dir.SSHConfigPath())
				return nil
			},
		},
	)
	return c
}
