package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"git-mirror/internal/config"
)

// cmdURL prints clone URLs, handy for scripts: git clone $(git-mirror url --ssh a/b).
func cmdURL() *cobra.Command {
	var (
		useSSH    bool
		withToken bool
		command   bool
	)
	c := &cobra.Command{
		Use:   "url <name>",
		Short: "Print the clone URL of a mirror (HTTP by default, --ssh for SSH)",
		Example: `  git clone $(git-mirror url --ssh github.com/golang/go)
  git-mirror url --token github.com/golang/go        # http url with the token embedded
  git-mirror url --command github.com/golang/go      # full git clone command`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := theApp.Status(cmd.Context())
			if err != nil {
				return err
			}
			found := false
			for _, r := range st.Repos {
				if r.Repo.Name == args[0] {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%w: %s", config.ErrNotFound, args[0])
			}
			token := ""
			if withToken {
				ts, err := config.LoadTokens(theApp.Dir)
				if err != nil {
					return err
				}
				if len(ts.Tokens) == 0 {
					return errors.New("no http token yet, start the server or run: git-mirror token add default")
				}
				t := pickToken(ts.Tokens, args[0])
				if t == nil {
					return fmt.Errorf("no token has access to %s (git-mirror token ls)", args[0])
				}
				token = t.Value
			}
			l := buildCloneLinks(st, token, args[0])
			url := l.HTTP
			switch {
			case useSSH:
				url = l.SSH
			case withToken:
				url = l.HTTPWithToken
			}
			if url == "" {
				return errors.New("that endpoint is not enabled (see: git-mirror config)")
			}
			if command {
				fmt.Println(l.gitClone(url))
			} else {
				fmt.Println(url)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&useSSH, "ssh", false, "print the SSH url")
	c.Flags().BoolVar(&withToken, "token", false, "embed the HTTP token in the url")
	c.Flags().BoolVar(&command, "command", false, "print a full git clone command")
	return c
}
