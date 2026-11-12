package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Help screen: a scrollable page with keys, legend and operating notes
// ---------------------------------------------------------------------------

// helpTextWidth caps the wrapped paragraphs so long lines stay readable on
// wide terminals.
const helpTextWidth = 90

// helpKeyStyle renders the key column of the shortcut tables.
var helpKeyStyle = lipgloss.NewStyle().Foreground(colorCyan)

// openHelp switches to the help screen and fills the viewport for the
// current terminal size.
func (m tuiModel) openHelp() tuiModel {
	m.mode = modeHelp
	m.help = viewport.New(m.helpSize())
	m.help.SetContent(m.helpContent())
	return m
}

// helpSize is the inner area available to the help viewport: the content
// pane minus its padding, the title block and the footer line.
func (m tuiModel) helpSize() (w, h int) {
	contentWidth := max(m.width-sidebarWidth-4, 30)
	w = contentWidth - 4
	// pane padding (2) + title and blank line (2) + blank line and footer (2)
	h = max(3, m.height-2-2-2-2)
	return w, h
}

// resizeHelp refits the viewport after a terminal resize while keeping the
// scroll position as far as the new content allows.
func (m tuiModel) resizeHelp() tuiModel {
	if m.mode != modeHelp {
		return m
	}
	off := m.help.YOffset
	m.help.Width, m.help.Height = m.helpSize()
	m.help.SetContent(m.helpContent())
	m.help.SetYOffset(off)
	return m
}

func (m tuiModel) updateHelp(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "esc", "?":
		m.mode = modeList
		return m, nil
	case "home", "g":
		m.help.GotoTop()
		return m, nil
	case "end", "G":
		m.help.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	m.help, cmd = m.help.Update(k)
	return m, cmd
}

func (m tuiModel) viewHelp(w int) string {
	var b sb
	b.add(titleStyle.Render("Help"))
	if pos := m.helpPosition(); pos != "" {
		b.add("  ", mutedStyle.Render(pos))
	}
	if n := m.viewNotice(w); n != "" {
		b.add("  ", n)
	}
	b.line("\n")
	b.line(m.help.View())
	// The footer must stay on one line, or it pushes the viewport off the pane.
	b.add(truncate(hints("j/k", "scroll", "pgup/pgdn", "page", "g/G", "top/end", "esc", "back"), w))
	return b.String()
}

// helpPosition describes the scroll position, empty when everything fits.
func (m tuiModel) helpPosition() string {
	switch {
	case m.help.TotalLineCount() <= m.help.Height:
		return ""
	case m.help.AtTop():
		return "top"
	case m.help.AtBottom():
		return "end"
	default:
		return fmt.Sprintf("%d%%", int(m.help.ScrollPercent()*100))
	}
}

// helpContent renders the whole help text for the current settings.
func (m tuiModel) helpContent() string {
	w, _ := m.helpSize()
	tw := min(w, helpTextWidth)
	var b sb
	para := func(s string) { b.line(textStyle.Render(wrap(s, tw)), "\n") }
	section := func(s string) { b.line(sectionStyle.Render(s)) }
	// table renders two columns. The first one is at least keyW cells wide
	// and grows to the widest key of the table, so a long key never wraps
	// and the rows stay aligned. The second column is cut to the viewport.
	table := func(keyStyle lipgloss.Style, keyW int, rows [][2]string) {
		for _, r := range rows {
			keyW = max(keyW, lipgloss.Width(r[0])+2) // never let the two columns touch
		}
		for _, r := range rows {
			b.line("  ", keyStyle.Width(keyW).Render(r[0]), textStyle.Render(truncate(r[1], w-2-keyW)))
		}
		b.line()
	}
	keys := func(rows ...[2]string) { table(helpKeyStyle, 16, rows) }
	kv := func(rows ...[2]string) { table(mutedStyle, 22, rows) }

	section("About")
	para("git-mirror keeps read-only copies of remote git repositories inside a private network. " +
		"Every mirror is a bare repository under <data dir>/repos/<name>.git with all branches and tags, " +
		"pruned to match the upstream on each sync (like git clone --mirror). Clients clone from " +
		"the built-in HTTP(S) and SSH endpoints of the server. Push is always rejected.")
	para("The server (git-mirror server) syncs enabled repositories every interval and serves them. " +
		"When it is not running, this UI runs git itself for add and sync actions and writes the same state file. " +
		"The left pane is refreshed every " + refreshEvery.String() + ".")

	section("Repository list")
	keys(
		[2]string{"up/down, j/k", "move the cursor"},
		[2]string{"pgup/pgdn", "move by a page"},
		[2]string{"home/end, g/G", "first / last repository"},
		[2]string{"/", "filter by name or url, esc clears the filter"},
		[2]string{"a", "add a repository (wizard: url, name, interval, branches, ssh key, proxy, access for tokens and keys)"},
		[2]string{"d, delete", "remove the selected repository, with or without its files"},
		[2]string{"s, enter", "sync the selected repository now"},
		[2]string{"S", "sync all enabled repositories"},
		[2]string{"e", "edit menu: sync interval, branch filter, SSH identity or proxy"},
		[2]string{"i", "edit the sync interval directly"},
		[2]string{"b", "edit the branch filter directly"},
		[2]string{"t", "enable / disable the selected repository"},
		[2]string{"c", "clone urls of the selected repository"},
		[2]string{"T", "HTTP access tokens"},
		[2]string{"K, tab", "SSH client keys"},
		[2]string{"I", "SSH identities for upstream fetches"},
		[2]string{"r", "refresh now"},
		[2]string{"?", "this help"},
		[2]string{"q, ctrl+c", "quit"},
	)

	section("Clone screen (c)")
	keys(
		[2]string{"1-9", "copy the numbered line to the clipboard (OSC 52)"},
		[2]string{"T", "HTTP access tokens"},
		[2]string{"K", "SSH client keys"},
		[2]string{"any other key", "back to the list"},
	)
	para("Clone urls are grouped into an HTTP and an SSH section. The HTTP section has one url per token " +
		"that may access the repository: \"default\" first, then unrestricted tokens, then scoped ones (at most " +
		fmt.Sprint(maxCloneTokens) + ", the rest are counted). Full git clone commands: git-mirror url --command.")

	section("Tokens screen (T)")
	keys(
		[2]string{"up/down, j/k", "move the cursor"},
		[2]string{"a", "add a token: name, then the mirrors it may access (empty = all)"},
		[2]string{"e", "edit the mirrors of the selected token"},
		[2]string{"R", "rotate the selected token, the old value stops working immediately"},
		[2]string{"d, delete", "remove the selected token"},
		[2]string{"y, c", "copy the value of the selected token to the clipboard"},
		[2]string{"r", "re-read tokens.json"},
		[2]string{"esc, q, T", "back to the list"},
	)

	section("SSH keys screen (K)")
	keys(
		[2]string{"up/down, j/k", "move the cursor"},
		[2]string{"a", "add a key: paste a public key line or a path to a .pub file, then its mirrors"},
		[2]string{"e", "edit the mirrors of the selected key"},
		[2]string{"d, delete", "remove the selected key"},
		[2]string{"r", "re-read authorized_keys"},
		[2]string{"esc, q, K, tab", "back to the list"},
	)

	section("Identities screen (I)")
	keys(
		[2]string{"up/down, j/k", "move the cursor"},
		[2]string{"g", "generate a new ed25519 identity"},
		[2]string{"a", "import a private key file (without passphrase)"},
		[2]string{"D", "make the selected identity the global default (again clears it)"},
		[2]string{"y, c", "copy the public key, paste it as a deploy key at the upstream"},
		[2]string{"d, delete", "remove the selected identity"},
		[2]string{"r", "re-read the identities directory"},
		[2]string{"esc, q, I", "back to the list"},
	)

	section("Text fields and menus")
	keys(
		[2]string{"enter", "accept / next step"},
		[2]string{"esc", "cancel"},
		[2]string{"up/down, j/k", "choose a menu item"},
	)

	section("Status icons")
	keys(
		[2]string{okStyle.Render("●"), "last sync succeeded"},
		[2]string{errorStyle.Render("●"), "last sync failed, the details show the error"},
		[2]string{mutedStyle.Render("○"), "never synced yet"},
		[2]string{mutedStyle.Render("◌"), "disabled, skipped by the server"},
		[2]string{spinnerStyle.Render("⠋"), "syncing right now"},
	)

	section("Intervals and branch filter")
	para("The sync interval is a Go duration such as 30m, 2h or 1h30m, at least 1m. An empty value " +
		"means the global default (" + m.status.Settings.SyncInterval.String() + "). Each sync is bounded by the " +
		"global timeout (" + m.status.Settings.SyncTimeout.String() + ") and " + fmt.Sprint(m.status.Settings.Workers) +
		" repositories sync in parallel.")
	para("The branch filter is a comma separated list, \"*\" globs are allowed (main,release/*). " +
		"Only matching branches plus all tags are fetched, branches that stop matching are removed from " +
		"the mirror. An empty filter mirrors every branch. Saving a new filter starts a sync right away.")

	section("Serving mirrors and access scopes")
	para("HTTP: every request must carry an access token, either as HTTP basic auth with any user name " +
		"and the token as password, or as an \"Authorization: Bearer\" header. The token \"default\" is generated " +
		"on the first server start with HTTP enabled. HTTPS is controlled by tls_mode: none, self (self-signed, " +
		"certificate at /-/cert.pem), manual (your own PEM files) or acme.")
	para("SSH: the built-in server only runs git-upload-pack for registered mirrors. Client keys are " +
		"kept in authorized_keys and applied immediately, the user name in the url does not matter. " +
		"Set ssh_allow_any_key true to accept every key in a fully trusted network.")
	para("Scopes: every token and every client key has a list of mirrors it may access, names or patterns " +
		"with one \"*\" (team/*, matching across /). An empty list means every mirror. A scoped credential gets " +
		"403 (HTTP) or \"key has no access\" (SSH) for other mirrors and only sees its own in the index. " +
		"The details of a repository show which tokens and keys can reach it.")

	section("Fetching upstreams over SSH")
	para("Identities are private keys stored in <data dir>/identities and used instead of the ~/.ssh of the " +
		"service user. The global default is the ssh_key setting, a repository may override it (edit menu). " +
		"Host keys are collected in <data dir>/known_hosts. An ssh_config file in the data dir, when present, " +
		"replaces the user and system ssh config (ssh -F). The proxy setting (or a per-repo proxy) routes " +
		"fetches through an HTTP CONNECT or SOCKS5 proxy: http(s) upstreams get it as git http.proxy, ssh upstreams " +
		"use the built-in git-mirror ssh-proxy command.")

	section("Current setup")
	httpEP := "off"
	if host := endpointHost(m.status, m.status.Server.GitHTTP, m.status.Settings.Listen); host != "" {
		httpEP = "http://" + host
		if httpsEnabled(m.status) {
			httpEP = "https://" + host
		}
	}
	sshEP := "off"
	if host := endpointHost(m.status, m.status.Server.GitSSH, m.status.Settings.SSHListen); host != "" {
		sshEP = "ssh://git@" + host
	}
	tls := m.status.Settings.TLS.Mode
	if tls == "" {
		tls = "none"
	}
	nameStyle := m.status.Settings.NameStyle
	if nameStyle == "" {
		nameStyle = "repo"
	}
	kv(
		[2]string{"data dir", m.app.Dir.String()},
		[2]string{"http endpoint", httpEP},
		[2]string{"tls mode", tls},
		[2]string{"ssh endpoint", sshEP},
		[2]string{"ssh client keys", fmt.Sprint(len(m.keys))},
		[2]string{"http tokens", fmt.Sprint(len(m.tokens))},
		[2]string{"identities", fmt.Sprint(len(m.identities))},
		[2]string{"global ssh key", sshKeyLabel(m.status.Settings.SSHKey)},
		[2]string{"proxy", orDash(m.status.Settings.Proxy)},
		[2]string{"ssh_config", presentLabel(m.app.HasSSHConfig())},
		[2]string{"name style", nameStyle},
		[2]string{"public host", orDash(m.status.Settings.PublicHost)},
	)

	section("Settings (git-mirror config set <key> <value>)")
	kv(
		[2]string{"interval", "default sync interval, e.g. 30m"},
		[2]string{"timeout", "time limit of one sync, e.g. 1h"},
		[2]string{"workers", "parallel syncs, needs a server restart"},
		[2]string{"name_style", "repo | owner | full, how names derive from urls"},
		[2]string{"listen", "git HTTP address, e.g. :8080, needs a restart"},
		[2]string{"public_host", "host name used in clone urls"},
		[2]string{"tls_mode", "none | self | manual | acme, needs a restart"},
		[2]string{"tls_cert, tls_key", "PEM files for tls_mode manual"},
		[2]string{"tls_fqdn, tls_alg", "certificate name and algorithm for tls_mode self"},
		[2]string{"tls_acme_hosts", "host names for tls_mode acme"},
		[2]string{"ssh_listen", "git SSH address, e.g. :2222, needs a restart"},
		[2]string{"ssh_allow_any_key", "true accepts every client key"},
		[2]string{"ssh_key", "identity name or key path for fetching ssh:// upstreams"},
		[2]string{"proxy", "proxy url for fetching upstreams (http, https, socks5, socks5h)"},
		[2]string{"proxy_auth", "user:password for the proxy, stored in proxy_auth (0600)"},
		[2]string{"proxy_insecure", "true skips certificate checks of an https proxy"},
	)
	para("The server picks up config.json changes automatically. workers, listen, ssh_listen and tls_* " +
		"take effect after a server restart.")

	section("Command line")
	kv(
		[2]string{"git-mirror init", "create the data dir and the initial config.json"},
		[2]string{"git-mirror server", "run the sync server, --listen and --ssh-listen override config"},
		[2]string{"git-mirror add <url>", "register a repository, --name --interval --branches --ssh-key --proxy"},
		[2]string{"git-mirror rm <name>", "unregister, --purge deletes the files"},
		[2]string{"git-mirror ls -l", "list repositories with their state"},
		[2]string{"git-mirror sync [name]", "sync now, --local runs git in-process"},
		[2]string{"git-mirror edit <name>", "change url, interval, branch filter, ssh key or proxy"},
		[2]string{"git-mirror enable|disable", "toggle repositories"},
		[2]string{"git-mirror url <name>", "clone url for scripts, --ssh --token --command"},
		[2]string{"git-mirror token", "ls, add, rm, rotate, edit HTTP tokens (--repos)"},
		[2]string{"git-mirror cert", "certificate of the HTTPS endpoint"},
		[2]string{"git-mirror keys", "ls, add, edit, rm client keys of the SSH server (--repos)"},
		[2]string{"git-mirror identity", "ls, gen, add, pub, rm private keys for upstream fetches"},
		[2]string{"git-mirror sshconfig", "show, edit, path, rm the OpenSSH client config"},
		[2]string{"git-mirror status", "server status and settings"},
		[2]string{"git-mirror path [name]", "data dir or mirror path"},
	)
	para("The data dir comes from --data-dir / -d or $GIT_MIRROR_DIR. When git-mirror is in PATH " +
		"it also works as a git subcommand: git mirror ls.")

	section("Data dir layout")
	kv(
		[2]string{"config.json", "repositories and settings"},
		[2]string{"state.json", "sync status of every mirror"},
		[2]string{"lock", "flock protecting config and state"},
		[2]string{"server.sock", "unix socket of the control API"},
		[2]string{"tokens.json", "named access tokens of the HTTP endpoint (0600)"},
		[2]string{"authorized_keys", "client keys of the built-in SSH server, repos= option"},
		[2]string{"ssh_host_ed25519_key", "host key of the built-in SSH server"},
		[2]string{"identities/", "private keys for fetching ssh:// upstreams"},
		[2]string{"known_hosts", "host keys learned while fetching"},
		[2]string{"ssh_config", "optional OpenSSH client config (ssh -F)"},
		[2]string{"proxy_auth", "optional user:password for the proxy (0600)"},
		[2]string{"certs/", "self-signed certificate and ACME cache"},
		[2]string{"repos/<name>.git", "the bare mirrors"},
	)

	return strings.TrimRight(b.String(), "\n")
}
