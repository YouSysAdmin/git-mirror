package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"git-mirror/internal/config"
	"git-mirror/internal/identity"
)

func (m tuiModel) selectedIdentity() (identity.Identity, bool) {
	if m.identitiesCursor >= len(m.identities) {
		return identity.Identity{}, false
	}
	return m.identities[m.identitiesCursor], true
}

func (m tuiModel) viewIdentities(w int) string {
	var b sb
	b.add(titleStyle.Render("SSH identities for upstream fetches"))
	if n := m.viewNotice(w); n != "" {
		b.add("  ", n)
	}
	b.line()
	b.line(mutedStyle.Render(truncateLeft(m.app.Dir.IdentitiesDir(), w)), "\n")

	cfg := m.status.Settings
	switch {
	case m.identitiesErr != nil:
		b.line(errorTextStyle.Render(wrap(m.identitiesErr.Error(), w)))
	case len(m.identities) == 0:
		b.line(mutedStyle.Render("No identities yet. Press "), hintKeyStyle.Render("g"), mutedStyle.Render(" to generate an ed25519 key or "),
			hintKeyStyle.Render("a"), mutedStyle.Render(" to import a private key file."))
	default:
		nameW := 8
		for _, id := range m.identities {
			nameW = max(nameW, len(id.Name))
		}
		nameW = min(nameW, 24)
		typeW, fpW := 12, 51
		usedW := max(8, w-2-nameW-1-typeW-1-fpW-1)
		b.line("  ", mutedStyle.Render(fmt.Sprintf("%-*s %-*s %-*s %s", nameW, "NAME", typeW, "TYPE", fpW, "FINGERPRINT", "USED BY")))
		for i, id := range m.identities {
			used := m.identityUsers(id.Name)
			row := fmt.Sprintf("%-*s %-*s %-*s %s", nameW, truncate(id.Name, nameW), typeW, truncate(id.Type, typeW), fpW, id.Fingerprint, truncate(used, usedW))
			if i == m.identitiesCursor {
				b.line(cursorStyle.Render("> "), selectedItemStyle.Render(row))
			} else {
				b.line("  ", normalItemStyle.Render(row))
			}
		}
		if id, ok := m.selectedIdentity(); ok {
			b.line()
			b.line(detailLabelStyle.Render("public key"), " ", detailValueStyle.Render(truncate(id.PublicLine, w-15)))
		}
	}
	b.line()
	b.line(textStyle.Render(wrap("Identities replace the ~/.ssh of the service user when fetching ssh:// upstreams. "+
		"Set the global default with D (config set ssh_key) or pick one per repository in its edit menu. "+
		"Paste the public key as a deploy key at the upstream. Host keys are collected in <data-dir>/known_hosts.", w)))
	b.line(detailLabelStyle.Render("global key"), " ", detailValueStyle.Render(sshKeyLabel(cfg.SSHKey)))
	if cfg.Proxy != "" {
		b.line(detailLabelStyle.Render("proxy"), " ", detailValueStyle.Render(cfg.Proxy))
	}
	if m.app.HasSSHConfig() {
		b.line(detailLabelStyle.Render("ssh_config"), " ", detailValueStyle.Render(truncateLeft(m.app.Dir.SSHConfigPath(), w-15)))
	}
	b.line("", hints("g", "generate", "a", "import", "D", "set global default", "y", "copy public key", "d", "remove", "esc", "back"))
	return b.String()
}

// identityUsers renders who reference an identity.
func (m tuiModel) identityUsers(name string) string {
	var used []string
	if m.status.Settings.SSHKey == name {
		used = append(used, "global")
	}
	for _, r := range m.status.Repos {
		if r.Repo.SSHKey == name {
			used = append(used, r.Repo.Name)
		}
	}
	return orDash(strings.Join(used, ", "))
}

func (m tuiModel) updateIdentities(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "esc", "I":
		m.mode = modeList
	case "up", "k":
		if m.identitiesCursor > 0 {
			m.identitiesCursor--
		}
	case "down", "j":
		if m.identitiesCursor < len(m.identities)-1 {
			m.identitiesCursor++
		}
	case "r":
		return m, m.fetchIdentities()
	case "g", "a":
		m.identityGen = k.String() == "g"
		m.identityAddName = ""
		m.input = newInputModel("Identity name", "deploy", "", identity.ValidateName).withNote(nameNote + " Used as the file name under identities/ and in --ssh-key.")
		m.mode = modeIdentityAdd
		return m, m.input.Init()
	case "y", "c":
		if id, ok := m.selectedIdentity(); ok {
			return m, copyCmd("public key of "+id.Name, id.PublicLine)
		}
	case "D":
		if id, ok := m.selectedIdentity(); ok {
			a, ctx := m.app, m.ctx
			value := id.Name
			if m.status.Settings.SSHKey == id.Name {
				value = "" // toggle off
			}
			return m, func() tea.Msg {
				if err := a.SetSetting(ctx, "ssh_key", value); err != nil {
					return actionMsg{err: err}
				}
				if value == "" {
					return actionMsg{text: "global ssh key cleared"}
				}
				return actionMsg{text: "global ssh key = " + value}
			}
		}
	case "d", "delete":
		if id, ok := m.selectedIdentity(); ok {
			body := id.Fingerprint
			if used := m.identityUsers(id.Name); used != "-" {
				body += "\nused by: " + used + " (syncs of those repositories will fail)"
			}
			m.confirm = newChoiceModel("Remove identity "+id.Name+"?", body, []string{"Remove", "Cancel"})
			m.mode = modeIdentityConfirm
		}
	}
	return m, nil
}

// viewIdentityAddCrumbs shows the first wizard step once it is done.
func (m tuiModel) viewIdentityAddCrumbs() string {
	if m.identityAddName == "" {
		return ""
	}
	return detailLabelStyle.Render("name") + " " + detailValueStyle.Render(m.identityAddName) + "\n\n"
}

func (m tuiModel) updateIdentityAdd(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeIdentities
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if !m.input.submitted {
		return m, cmd
	}
	val := m.input.Value()
	dir := m.app.Dir
	if m.identityGen {
		m.mode = modeIdentities
		return m, func() tea.Msg {
			id, err := identity.Generate(dir, val, "")
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "generated " + id.Name + ", press y to copy the public key"}
		}
	}
	if m.identityAddName == "" {
		m.identityAddName = val
		m.input = newInputModel("Private key file", "~/.ssh/id_ed25519", "", nil).
			withNote("Path of an OpenSSH private key without passphrase. It is copied into the data dir, a .pub next to it supplies the comment.")
		m.input.input.Width = 80
		return m, m.input.Init()
	}
	m.mode = modeIdentities
	name := m.identityAddName
	return m, func() tea.Msg {
		id, err := identity.Import(dir, name, val)
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "imported " + id.Name + " (" + id.Fingerprint + ")"}
	}
}

func (m tuiModel) updateIdentityConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeIdentities
		return m, nil
	}
	m.confirm = m.confirm.Update(k)
	idx, ok := m.confirm.Chosen()
	if !ok {
		return m, nil
	}
	m.mode = modeIdentities
	id, sel := m.selectedIdentity()
	if idx != 0 || !sel {
		return m, nil
	}
	dir := m.app.Dir
	return m, func() tea.Msg {
		cfg, err := config.Load(dir)
		if err != nil {
			return actionMsg{err: err}
		}
		if err := identity.Remove(dir, cfg, id.Name, true); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "removed identity " + id.Name}
	}
}
