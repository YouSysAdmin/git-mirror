package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"git-mirror/internal/access"
	"git-mirror/internal/config"
)

// confirmAction says what a confirmation menu is about.
type confirmAction int

const (
	actionRemove confirmAction = iota
	actionRotate
)

func (m tuiModel) selectedToken() (config.Token, bool) {
	if m.tokensCursor >= len(m.tokens) {
		return config.Token{}, false
	}
	return m.tokens[m.tokensCursor], true
}

func (m tuiModel) viewToken(w int) string {
	var b sb
	b.add(titleStyle.Render("HTTP access tokens"))
	if n := m.viewNotice(w); n != "" {
		b.add("  ", n)
	}
	b.line()
	b.line(mutedStyle.Render(truncateLeft(m.app.Dir.TokensPath(), w)), "\n")
	switch {
	case m.tokensErr != nil:
		b.line(errorTextStyle.Render(wrap(m.tokensErr.Error(), w)))
	case len(m.tokens) == 0:
		b.line(mutedStyle.Render(wrap("no tokens yet: \"default\" is generated when the server starts with HTTP enabled, or press a to create one now", w)))
	default:
		nameW := 8
		for _, t := range m.tokens {
			nameW = max(nameW, len(t.Name))
		}
		nameW = min(nameW, 24)
		createdW := 10
		reposW := max(8, w-2-nameW-1-createdW-1)
		b.line("  ", mutedStyle.Render(fmt.Sprintf("%-*s %-*s %s", nameW, "NAME", createdW, "CREATED", "REPOS")))
		for i, t := range m.tokens {
			created := "-"
			if !t.Created.IsZero() {
				created = t.Created.Local().Format("2006-01-02")
			}
			row := fmt.Sprintf("%-*s %-*s %s", nameW, truncate(t.Name, nameW), createdW, created, truncate(t.Scope(), reposW))
			if i == m.tokensCursor {
				b.line(cursorStyle.Render("> "), selectedItemStyle.Render(row))
			} else {
				b.line("  ", normalItemStyle.Render(row))
			}
		}
		if t, ok := m.selectedToken(); ok {
			b.line()
			b.line(detailLabelStyle.Render("value"), " ", selectedItemStyle.Render(t.Value))
			var names []string
			for _, r := range m.status.Repos {
				if t.Allows(r.Repo.Name) {
					names = append(names, r.Repo.Name)
				}
			}
			b.line(detailLabelStyle.Render("mirrors"), " ", detailValueStyle.Render(truncate(fmt.Sprintf("%d of %d: %s", len(names), len(m.status.Repos), strings.Join(names, ", ")), w-15)))
		}
	}
	b.line()
	b.line(textStyle.Render(wrap("Every request to the git HTTP endpoint must carry a token: HTTP basic auth with any user name and the token as password, or an \"Authorization: Bearer\" header. A token only sees the mirrors in its scope. SSH clones are not affected.", w)))
	endpoint := "-"
	if host := endpointHost(m.status, m.status.Server.GitHTTP, m.status.Settings.Listen); host != "" {
		endpoint = "http://" + host
		if httpsEnabled(m.status) {
			endpoint = "https://" + host + "  (tls " + m.status.Settings.TLS.Mode + ")"
		}
	}
	b.line(detailLabelStyle.Render("endpoint"), " ", detailValueStyle.Render(endpoint))
	b.line("", hints("a", "add", "e", "edit repos", "R", "rotate", "d", "remove", "y", "copy value", "esc", "back"))
	return b.String()
}

func (m tuiModel) updateToken(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "esc", "T":
		m.mode = modeList
	case "up", "k":
		if m.tokensCursor > 0 {
			m.tokensCursor--
		}
	case "down", "j":
		if m.tokensCursor < len(m.tokens)-1 {
			m.tokensCursor++
		}
	case "r":
		return m, m.fetchTokens()
	case "a":
		m.tokenAddName = ""
		m.input = newInputModel("Token name", "ci", "", config.ValidateTokenName).withNote(nameNote + " Shown in logs and clone commands, never sent by clients.")
		m.mode = modeTokenAdd
		return m, m.input.Init()
	case "e":
		if t, ok := m.selectedToken(); ok {
			m.input = newInputModel("Repositories for token "+t.Name, "go, team/*", strings.Join(t.Repos, ","), validateScope).withNote(scopeNote)
			m.mode = modeTokenField
			return m, m.input.Init()
		}
	case "R":
		if t, ok := m.selectedToken(); ok {
			m.tokenAction = actionRotate
			m.confirm = newChoiceModel("Rotate token "+t.Name+"?",
				"Clients using the old value lose access immediately.", []string{"Rotate", "Cancel"})
			m.mode = modeTokenConfirm
		}
	case "d", "delete":
		if t, ok := m.selectedToken(); ok {
			m.tokenAction = actionRemove
			m.confirm = newChoiceModel("Remove token "+t.Name+"?",
				"Clients using it lose access immediately.", []string{"Remove", "Cancel"})
			m.mode = modeTokenConfirm
		}
	case "y", "c":
		if t, ok := m.selectedToken(); ok {
			return m, copyCmd("token "+t.Name, t.Value)
		}
	}
	return m, nil
}

// viewTokenAddCrumbs shows the first wizard step once it is done.
func (m tuiModel) viewTokenAddCrumbs() string {
	if m.tokenAddName == "" {
		return ""
	}
	return detailLabelStyle.Render("name") + " " + detailValueStyle.Render(m.tokenAddName) + "\n\n"
}

// updateTokenAdd drives the add wizard (name, then repos) and the scope editor.
func (m tuiModel) updateTokenAdd(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeToken
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if !m.input.submitted {
		return m, cmd
	}
	val := m.input.Value()
	dir := m.app.Dir
	if m.mode == modeTokenField {
		m.mode = modeToken
		t, ok := m.selectedToken()
		if !ok {
			return m, nil
		}
		return m, func() tea.Msg {
			scope, err := access.ParseRepoList(val)
			if err != nil {
				return actionMsg{err: err}
			}
			t, err := config.SetTokenRepos(dir, t.Name, scope)
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "token " + t.Name + ": repos = " + t.Scope()}
		}
	}
	if m.tokenAddName == "" {
		m.tokenAddName = val
		m.input = newInputModel("Repositories the token may access", "go, team/*", "", validateScope).withNote(scopeNote)
		return m, m.input.Init()
	}
	m.mode = modeToken
	name := m.tokenAddName
	return m, func() tea.Msg {
		scope, err := access.ParseRepoList(val)
		if err != nil {
			return actionMsg{err: err}
		}
		t, err := config.AddToken(dir, name, "", scope)
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "added token " + t.Name + " (repos: " + t.Scope() + ")"}
	}
}

func (m tuiModel) updateTokenConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeToken
		return m, nil
	}
	m.confirm = m.confirm.Update(k)
	idx, ok := m.confirm.Chosen()
	if !ok {
		return m, nil
	}
	m.mode = modeToken
	t, sel := m.selectedToken()
	if idx != 0 || !sel {
		return m, nil
	}
	dir, action := m.app.Dir, m.tokenAction
	return m, func() tea.Msg {
		if action == actionRemove {
			if err := config.RemoveToken(dir, t.Name); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "removed token " + t.Name}
		}
		if _, err := config.RotateToken(dir, t.Name); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "rotated token " + t.Name}
	}
}
