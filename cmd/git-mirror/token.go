package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"git-mirror/internal/access"
	"git-mirror/internal/config"
)

// cmdToken manages the named access tokens of the git HTTP endpoint.
func cmdToken() *cobra.Command {
	c := &cobra.Command{
		Use:   "token",
		Short: "Manage access tokens of the git HTTP endpoint",
		Long: `The git HTTP endpoint (settings.listen) requires an access token: HTTP basic
auth with any user name and the token as password, or "Authorization: Bearer".
Tokens are named and may be limited to some mirrors (--repos, names or "*"
patterns, empty = every mirror). They live in <data-dir>/tokens.json (0600).
The server creates the token "default" on first start if none exists.

  git clone http://git:<token>@<host>:8080/<name>.git

Without arguments the value of the only token is printed (script friendly),
with several tokens a table is shown.`,
		RunE: func(*cobra.Command, []string) error {
			ts, err := config.LoadTokens(theApp.Dir)
			if err != nil {
				return err
			}
			switch len(ts.Tokens) {
			case 0:
				fmt.Println("no token yet: it is generated when the server starts with HTTP enabled, or run: git-mirror token add default")
				return nil
			case 1:
				fmt.Println(ts.Tokens[0].Value)
				return nil
			}
			return printTokens(ts, false)
		},
	}
	var long bool
	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List tokens with their scope",
		RunE: func(*cobra.Command, []string) error {
			ts, err := config.LoadTokens(theApp.Dir)
			if err != nil {
				return err
			}
			if len(ts.Tokens) == 0 {
				fmt.Println("no tokens. Add one with: git-mirror token add default")
				return nil
			}
			return printTokens(ts, long)
		},
	}
	ls.Flags().BoolVarP(&long, "long", "l", false, "expand scope patterns to the matching mirrors")

	show := &cobra.Command{
		Use:   "show [<name>]",
		Short: "Print the value of a token (default: the only token or \"default\")",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ts, err := config.LoadTokens(theApp.Dir)
			if err != nil {
				return err
			}
			t, err := pickNamed(ts, args)
			if err != nil {
				return err
			}
			fmt.Println(t.Value)
			return nil
		},
	}

	var repos, value string
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a token, random unless --value is given",
		Example: `  git-mirror token add ci                       # every mirror
  git-mirror token add team-a --repos team-a/*   # only mirrors under team-a/
  git-mirror token add deploy --repos go,tools --value my-own-secret`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := access.ParseRepoList(repos)
			if err != nil {
				return err
			}
			warnUnknownRepos(scope)
			t, err := config.AddToken(theApp.Dir, args[0], value, scope)
			if err != nil {
				return err
			}
			fmt.Printf("added token %s (repos: %s)\n%s\n", t.Name, t.Scope(), t.Value)
			return nil
		},
	}
	add.Flags().StringVar(&repos, "repos", "", `mirrors the token may access, comma separated, "*" globs allowed (default: all)`)
	add.Flags().StringVar(&value, "value", "", "explicit token value (default: random)")

	rm := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "del"},
		Short:   "Delete a token, clients using it lose access immediately",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := config.RemoveToken(theApp.Dir, args[0]); err != nil {
				return err
			}
			fmt.Println("removed token", args[0])
			return nil
		},
	}

	rotate := &cobra.Command{
		Use:   "rotate [<name>]",
		Short: "Replace the value of a token with a new random one",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ts, err := config.LoadTokens(theApp.Dir)
			if err != nil {
				return err
			}
			name := config.DefaultTokenName
			if len(ts.Tokens) > 0 {
				t, err := pickNamed(ts, args)
				if err != nil {
					return err
				}
				name = t.Name
			} else if len(args) == 1 {
				name = args[0]
			}
			if ts.Find(name) == nil {
				// Nothing to rotate yet: behave like the old single-token command.
				t, err := config.AddToken(theApp.Dir, name, "", nil)
				if err != nil {
					return err
				}
				fmt.Println(t.Value)
				return nil
			}
			t, err := config.RotateToken(theApp.Dir, name)
			if err != nil {
				return err
			}
			fmt.Println(t.Value)
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set [<name>] <value>",
		Short: "Set an explicit value on a token (default: \"default\", created if missing)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			name, value := config.DefaultTokenName, args[0]
			if len(args) == 2 {
				name, value = args[0], args[1]
			}
			ts, err := config.LoadTokens(theApp.Dir)
			if err != nil {
				return err
			}
			if ts.Find(name) == nil {
				if _, err := config.AddToken(theApp.Dir, name, value, nil); err != nil {
					return err
				}
				fmt.Printf("added token %s\n", name)
				return nil
			}
			if err := config.SetTokenValue(theApp.Dir, name, value); err != nil {
				return err
			}
			fmt.Printf("token %s updated\n", name)
			return nil
		},
	}

	var editRepos string
	edit := &cobra.Command{
		Use:   "edit <name> --repos <list|all>",
		Short: "Change the mirrors a token may access",
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
			t, err := config.SetTokenRepos(theApp.Dir, args[0], scope)
			if err != nil {
				return err
			}
			fmt.Printf("token %s: repos = %s\n", t.Name, t.Scope())
			return nil
		},
	}
	edit.Flags().StringVar(&editRepos, "repos", "", `mirrors the token may access, comma separated, "*" globs allowed, or "all"`)

	c.AddCommand(ls, show, add, rm, rotate, set, edit)
	return c
}

// pickNamed selects a token by the optional name argument. Without a name the
// only token wins, then "default", otherwise the choice is ambiguous.
func pickNamed(ts *config.Tokens, args []string) (config.Token, error) {
	if len(args) == 1 {
		t := ts.Find(args[0])
		if t == nil {
			return config.Token{}, fmt.Errorf("token not found: %s", args[0])
		}
		return *t, nil
	}
	if len(ts.Tokens) == 1 {
		return ts.Tokens[0], nil
	}
	if t := ts.Find(config.DefaultTokenName); t != nil {
		return *t, nil
	}
	if len(ts.Tokens) == 0 {
		return config.Token{}, errors.New("no tokens yet, add one with: git-mirror token add default")
	}
	names := make([]string, 0, len(ts.Tokens))
	for _, t := range ts.Tokens {
		names = append(names, t.Name)
	}
	return config.Token{}, fmt.Errorf("several tokens, pick one: %s", strings.Join(names, ", "))
}

func printTokens(ts *config.Tokens, long bool) error {
	var cfg *config.Config
	if long {
		var err error
		if cfg, err = config.Load(theApp.Dir); err != nil {
			return err
		}
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if long {
		_, _ = fmt.Fprintln(w, "NAME\tREPOS\tMIRRORS\tCREATED\tVALUE")
	} else {
		_, _ = fmt.Fprintln(w, "NAME\tREPOS\tCREATED\tVALUE")
	}
	for _, t := range ts.Tokens {
		created := "-"
		if !t.Created.IsZero() {
			created = t.Created.Local().Format("2006-01-02")
		}
		if long {
			var names []string
			for _, r := range cfg.ReposFor(t.Repos) {
				names = append(names, r.Name)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.Scope(), orDash(strings.Join(names, ",")), created, t.Value)
		} else {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, t.Scope(), created, t.Value)
		}
	}
	return w.Flush()
}

// warnUnknownRepos prints a hint for literal names that are not registered.
// Patterns are left alone, they may match mirrors added later.
func warnUnknownRepos(scope []string) {
	cfg, err := config.Load(theApp.Dir)
	if err != nil {
		return
	}
	for _, p := range scope {
		if !strings.Contains(p, "*") && cfg.Find(p) == nil {
			_, _ = fmt.Fprintf(os.Stderr, "note: no mirror named %q yet\n", p)
		}
	}
}
