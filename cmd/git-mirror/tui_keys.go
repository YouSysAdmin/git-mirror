package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"git-mirror/internal/access"
	"git-mirror/internal/authkeys"
)

func (m tuiModel) updateKeys(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "esc", "K", "tab":
		m.mode = modeList
	case "up", "k":
		if m.keysCursor > 0 {
			m.keysCursor--
		}
	case "down", "j":
		if m.keysCursor < len(m.keys)-1 {
			m.keysCursor++
		}
	case "r":
		return m, m.fetchKeys()
	case "a":
		m.keysAddLine = ""
		m.input = newInputModel("Public key", "ssh-ed25519 AAAA... alice@laptop", "", func(s string) error {
			_, err := authkeys.LinesFromInput(s)
			return err
		}).withNote("An OpenSSH public key line or the path of a .pub file (~ allowed). Every key in the file is added.")
		m.input.input.Width = 80
		m.mode = modeKeysAdd
		return m, m.input.Init()
	case "e":
		if m.keysCursor < len(m.keys) {
			key := m.keys[m.keysCursor]
			m.input = newInputModel("Repositories for "+keyLabel(key), "go, team/*", strings.Join(key.Repos, ","), validateScope).withNote(scopeNote)
			m.mode = modeKeysField
			return m, m.input.Init()
		}
	case "d", "delete":
		if m.keysCursor < len(m.keys) {
			key := m.keys[m.keysCursor]
			m.confirm = newChoiceModel("Remove key?", key.Fingerprint()+"  "+key.Comment, []string{"Remove", "Cancel"})
			m.mode = modeKeysConfirm
		}
	}
	return m, nil
}

// keyLabel names a client key by comment or fingerprint.
func keyLabel(k authkeys.Key) string {
	if k.Comment != "" {
		return k.Comment
	}
	return k.Fingerprint()
}

// viewKeysAddCrumbs shows the first wizard step once it is done.
func (m tuiModel) viewKeysAddCrumbs() string {
	if m.keysAddLine == "" {
		return ""
	}
	return detailLabelStyle.Render("key") + " " + detailValueStyle.Render(truncate(m.keysAddLine, 60)) + "\n\n"
}

// updateKeysAdd drives both the add wizard (key, then repos) and the scope
// editor of an existing key.
func (m tuiModel) updateKeysAdd(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeKeys
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if !m.input.submitted {
		return m, cmd
	}
	val := m.input.Value()
	dir := m.app.Dir
	if m.mode == modeKeysField {
		m.mode = modeKeys
		if m.keysCursor >= len(m.keys) {
			return m, nil
		}
		fp, label := m.keys[m.keysCursor].Fingerprint(), keyLabel(m.keys[m.keysCursor])
		return m, func() tea.Msg {
			scope, err := access.ParseRepoList(val)
			if err != nil {
				return actionMsg{err: err}
			}
			k, err := authkeys.SetRepos(dir, fp, scope)
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: label + ": repos = " + k.Scope()}
		}
	}
	if m.keysAddLine == "" {
		m.keysAddLine = val
		m.input = newInputModel("Repositories the key may access", "go, team/*", "", validateScope).withNote(scopeNote)
		return m, m.input.Init()
	}
	m.mode = modeKeys
	in := m.keysAddLine
	return m, func() tea.Msg {
		lines, err := authkeys.LinesFromInput(in)
		if err != nil {
			return actionMsg{err: err}
		}
		scope, err := access.ParseRepoList(val)
		if err != nil {
			return actionMsg{err: err}
		}
		if scope == nil {
			scope = []string{}
		}
		added, updated, _, err := authkeys.Add(dir, lines, scope)
		if err != nil {
			return actionMsg{err: err}
		}
		switch {
		case len(added) == 0 && len(updated) == 0:
			return actionMsg{text: "key already present"}
		case len(added) == 0:
			return actionMsg{text: fmt.Sprintf("updated scope of %d key(s)", len(updated))}
		default:
			return actionMsg{text: fmt.Sprintf("added %d key(s) (repos: %s)", len(added), access.Label(scope))}
		}
	}
}

func (m tuiModel) updateKeysConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeKeys
		return m, nil
	}
	m.confirm = m.confirm.Update(k)
	idx, ok := m.confirm.Chosen()
	if !ok {
		return m, nil
	}
	m.mode = modeKeys
	if idx != 0 || m.keysCursor >= len(m.keys) {
		return m, nil
	}
	dir, fp, label := m.app.Dir, m.keys[m.keysCursor].Fingerprint(), keyLabel(m.keys[m.keysCursor])
	return m, func() tea.Msg {
		if _, err := authkeys.Remove(dir, []string{fp}); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "removed key " + label}
	}
}

func (m tuiModel) viewKeys(w int) string {
	var b sb
	b.WriteString(titleStyle.Render("SSH client keys"))
	if right := m.viewNotice(w); right != "" {
		b.add("  ", right)
	}
	b.WriteString("\n")
	b.line(mutedStyle.Render(truncateLeft(m.app.Dir.AuthorizedKeysPath(), w)), "\n")

	switch {
	case m.status.Settings.SSHAllowAnyKey:
		b.line(warnStyle.Render("ssh_allow_any_key is on: every key is accepted for every mirror, this list is ignored"), "\n")
	case m.status.Server.Running && m.status.Server.GitSSH == "":
		b.line(warnStyle.Render("SSH endpoint is disabled: git-mirror config set ssh_listen :2222, then restart the server"), "\n")
	}

	if m.keysErr != nil {
		b.line(errorTextStyle.Render(wrap(m.keysErr.Error(), w)))
	} else if len(m.keys) == 0 {
		b.line(mutedStyle.Render("No keys yet. Press "), hintKeyStyle.Render("a"), mutedStyle.Render(" to add a public key or a .pub file path."))
	} else {
		typeW, fpW := 12, 51
		rest := max(16, w-2-typeW-1-fpW-1)
		reposW := max(8, rest/2)
		commentW := max(8, rest-reposW-1)
		b.line("  ", mutedStyle.Render(fmt.Sprintf("%-*s %-*s %-*s %s", typeW, "TYPE", fpW, "FINGERPRINT", reposW, "REPOS", "COMMENT")))
		for i, k := range m.keys {
			row := fmt.Sprintf("%-*s %-*s %-*s %s", typeW, truncate(k.Key.Type(), typeW), fpW, k.Fingerprint(),
				reposW, truncate(k.Scope(), reposW), truncate(k.Comment, commentW))
			if i == m.keysCursor {
				b.line(cursorStyle.Render("> "), selectedItemStyle.Render(row))
			} else {
				b.line("  ", normalItemStyle.Render(row))
			}
		}
	}
	b.add("\n", hints("a", "add", "e", "edit repos", "d", "remove", "r", "refresh", "esc", "back"))
	return b.String()
}
