package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"git-mirror/internal/api"
	"git-mirror/internal/app"
	"git-mirror/internal/config"
	"git-mirror/internal/mirror"
)

func (m tuiModel) updateList(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "esc":
		if m.filter != "" && k.String() == "esc" {
			m.filter = ""
			m = m.applyFilter()
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(0, len(m.filtered)-1)
	case "pgup":
		m.cursor = max(0, m.cursor-m.listHeight())
	case "pgdown":
		m.cursor = min(max(0, len(m.filtered)-1), m.cursor+m.listHeight())
	case "/":
		m.mode = modeFilter
	case "r":
		return m, m.fetchStatus()
	case "?":
		return m.openHelp(), nil
	case "K", "tab":
		m.mode = modeKeys
		return m, m.fetchKeys()
	case "T":
		m.mode = modeToken
		return m, m.fetchTokens()
	case "I":
		m.mode = modeIdentities
		return m, m.fetchIdentities()
	case "c":
		if _, ok := m.selected(); ok {
			m.mode = modeClone
		}
	case "a":
		m = m.startAdd()
		return m, m.input.Init()
	case "d", "delete":
		if r, ok := m.selected(); ok {
			m.confirm = newChoiceModel(
				"Remove "+r.Repo.Name+"?",
				"",
				[]string{"Remove from list (keep files)", "Remove and delete mirror files", "Cancel"})
			m.mode = modeConfirmRemove
		}
	case "t":
		if r, ok := m.selected(); ok {
			return m, m.toggleEnabled(r.Repo)
		}
	case "e":
		if r, ok := m.selected(); ok {
			m.editFields = editFieldsFor(r.Repo)
			names := make([]string, 0, len(m.editFields)+1)
			for _, f := range m.editFields {
				names = append(names, editFieldNames[f])
			}
			m.confirm = newChoiceModel("Edit "+r.Repo.Name, "", append(names, "Cancel"))
			m.mode = modeEditMenu
		}
	case "i":
		return m.startEditField(editInterval)
	case "b":
		return m.startEditField(editBranches)
	case "s", "enter":
		if r, ok := m.selected(); ok {
			return m, m.syncRepos([]string{r.Repo.Name})
		}
	case "S":
		return m, m.syncRepos(nil)
	}
	return m, nil
}

func (m tuiModel) updateFilter(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.filter = ""
		m = m.applyFilter()
		m.mode = modeList
	case "enter":
		m.mode = modeList
	case "backspace":
		if len(m.filter) > 0 {
			r := []rune(m.filter)
			m.filter = string(r[:len(r)-1])
			m = m.applyFilter()
		}
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
	default:
		if k.Type == tea.KeyRunes {
			m.filter += string(k.Runes)
			m = m.applyFilter()
		}
	}
	return m, nil
}

// startAdd opens the add wizard on its first step. The token and client key

func (m tuiModel) updateConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeList
		return m, nil
	}
	m.confirm = m.confirm.Update(k)
	idx, ok := m.confirm.Chosen()
	if !ok {
		return m, nil
	}
	m.mode = modeList
	r, sel := m.selected()
	if !sel || idx == 2 {
		return m, nil
	}
	return m, m.doRemove(r.Repo.Name, idx == 1)
}

func (m tuiModel) doRemove(name string, purge bool) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.Remove(ctx, name, purge); err != nil {
			return actionMsg{err: err}
		}
		if purge {
			return actionMsg{text: "removed " + name + " and deleted files"}
		}
		return actionMsg{text: "removed " + name + " (files kept)"}
	}
}

func (m tuiModel) toggleEnabled(r config.Repo) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		if err := a.SetEnabled(ctx, r.Name, !r.Enabled); err != nil {
			return actionMsg{err: err}
		}
		if r.Enabled {
			return actionMsg{text: "disabled " + r.Name}
		}
		return actionMsg{text: "enabled " + r.Name}
	}
}

// syncRepos hands the work to the server, or runs git in-process when the
// server is down (sequentially, in the background of the TUI).
func (m tuiModel) syncRepos(names []string) tea.Cmd {
	a, ctx := m.app, m.ctx
	return func() tea.Msg {
		queued, done := 0, 0
		viaServer, err := a.Sync(ctx, names, app.SyncOptions{Progress: func(e app.SyncEvent) {
			switch e.Phase {
			case app.PhaseQueued:
				if e.Err == nil {
					queued++
				}
			case app.PhaseDone:
				done++
			}
		}})
		switch {
		case err != nil:
			return actionMsg{err: err}
		case viaServer && queued == 0:
			return actionMsg{text: "already queued or syncing"}
		case viaServer:
			return actionMsg{text: fmt.Sprintf("queued %d repo(s) on the server", queued)}
		default:
			return actionMsg{text: fmt.Sprintf("synced %d repo(s) locally", done)}
		}
	}
}

func (m tuiModel) viewList(w int) string {
	var b sb

	// Header line
	title := titleStyle.Render("Repositories")
	right := m.viewNotice(w)
	b.WriteString(title)
	if n := m.syncingCount(); n > 0 {
		b.add("  ", spinnerStyle.Render(strings.TrimSpace(m.spinner.View())), " ",
			highlightStyle.Render(fmt.Sprintf("syncing %d of %d", n, len(m.status.Repos))))
	}
	if right != "" {
		b.add("  ", right)
	}
	b.WriteString("\n")

	// Filter line
	if m.mode == modeFilter {
		b.line(mutedStyle.Render("Filter: "), inputStyle.Render(m.filter), cursorStyle.Render("▌"), "\n")
	} else if m.filter != "" {
		b.line(mutedStyle.Render("Filter: "), inputStyle.Render(m.filter), mutedStyle.Render("  (esc clears)"), "\n")
	} else {
		b.line(mutedStyle.Render("Press / to filter, a to add"), "\n")
	}

	if !m.loaded {
		b.line(mutedStyle.Render(m.spinner.View() + " loading..."))
		return b.String()
	}
	if len(m.status.Repos) == 0 {
		b.line(mutedStyle.Render("No repositories yet. Press "), hintKeyStyle.Render("a"), mutedStyle.Render(" to add one."))
		return b.String()
	}
	if len(m.filtered) == 0 {
		b.line(mutedStyle.Render("No matches."))
		return b.String()
	}

	// Column widths: icon(2) name(*) last(12) interval(8)
	lastW, ivW := 12, 8
	nameW := max(10, w-2-1-lastW-1-ivW-2)

	maxVisible := m.listHeight()
	start := 0
	if m.cursor >= maxVisible {
		start = m.cursor - maxVisible + 1
	}
	end := min(start+maxVisible, len(m.filtered))

	b.line("  ", mutedStyle.Render(fmt.Sprintf("%-*s %*s %*s", nameW, "NAME", lastW, "LAST SYNC", ivW, "EVERY")))
	for i := start; i < end; i++ {
		r := m.status.Repos[m.filtered[i]]
		icon := m.statusIcon(r)
		name := truncate(r.Repo.Name, nameW)
		last := ago(r.State.LastSuccess)
		if r.State.Status == config.StatusError && r.State.LastSuccess.IsZero() {
			last = "failed"
		}
		row := fmt.Sprintf("%-*s %*s %*s", nameW, name, lastW, truncate(last, lastW), ivW, humanDuration(r.Repo.EffectiveInterval(m.status.Settings)))
		style := normalItemStyle
		if !r.Repo.Enabled {
			style = disabledItemStyle
		}
		if i == m.cursor {
			b.line(cursorStyle.Render("> "), icon, " ", selectedItemStyle.Render(row))
		} else {
			b.line("  ", icon, " ", style.Render(row))
		}
	}
	if len(m.filtered) > maxVisible {
		b.line(mutedStyle.Render(fmt.Sprintf("  … %d-%d of %d", start+1, end, len(m.filtered))))
	}

	// Details
	if r, ok := m.selected(); ok {
		b.line("\n", dividerStyle.Render(strings.Repeat("─", min(w, 80))))
		b.WriteString(m.viewDetails(r, w))
	}

	b.add("\n", hints("a", "add", "d", "remove", "s", "sync", "S", "sync all", "e", "edit", "t", "toggle", "c", "clone", "T", "tokens", "K", "keys", "I", "identities", "?", "help", "q", "quit"))
	return b.String()
}

// viewAdd renders the add wizard: breadcrumbs of the planned steps, the

// syncingCount is the number of repositories currently being synced.
func (m tuiModel) syncingCount() int {
	n := 0
	for _, r := range m.status.Repos {
		if r.InFlight || r.State.Status == config.StatusSyncing {
			n++
		}
	}
	return n
}

func (m tuiModel) statusIcon(r api.RepoStatus) string {
	switch {
	case r.InFlight || r.State.Status == config.StatusSyncing:
		return spinnerStyle.Render(strings.TrimSpace(m.spinner.View()))
	case !r.Repo.Enabled:
		return mutedStyle.Render("◌")
	case r.State.Status == config.StatusOK:
		return okStyle.Render("●")
	case r.State.Status == config.StatusError:
		return errorStyle.Render("●")
	default:
		return mutedStyle.Render("○")
	}
}

// accessLabel summarizes which tokens and client keys may reach a mirror.
func (m tuiModel) accessLabel(name string) string {
	toks, keys := repoAccess(m.tokens, m.keys, name)
	parts := []string{"tokens: " + orDash(strings.Join(toks, ", "))}
	switch {
	case m.status.Settings.SSHAllowAnyKey:
		parts = append(parts, "ssh keys: any")
	case len(keys) == len(m.keys):
		parts = append(parts, fmt.Sprintf("ssh keys: all %d", len(keys)))
	default:
		parts = append(parts, fmt.Sprintf("ssh keys: %d of %d", len(keys), len(m.keys)))
	}
	return strings.Join(parts, "   ")
}

func (m tuiModel) viewDetails(r api.RepoStatus, w int) string {
	var b sb
	line := func(k, v string) {
		b.line(detailLabelStyle.Render(k), " ", detailValueStyle.Render(truncate(v, w-15)))
	}
	status := string(r.State.Status)
	if r.InFlight || r.State.Status == config.StatusSyncing {
		status = "syncing"
	}
	if !r.Repo.Enabled {
		status += " (disabled)"
	}
	line("url", r.Repo.URL)
	line("branches", branchesLabel(r.Repo.Branches)+"  (tags: all)")
	line("path", m.app.Dir.RepoPath(r.Repo.Name))
	if mirror.IsSSHURL(r.Repo.URL) {
		v := sshKeyLabel(r.Repo.EffectiveSSHKey(m.status.Settings))
		if r.Repo.SSHKey == "" && r.Repo.EffectiveSSHKey(m.status.Settings) != "" {
			v += "  (global)"
		}
		line("ssh key", v)
	}
	if proxy := r.Repo.EffectiveProxy(m.status.Settings); proxy != "" {
		v := proxy
		if r.Repo.Proxy == "" {
			v += "  (global)"
		}
		line("proxy", v)
	}
	line("access", m.accessLabel(r.Repo.Name))
	line("clone", "press c for http / ssh clone urls")
	line("status", status)
	last := ago(r.State.LastSuccess)
	if !r.State.LastSuccess.IsZero() {
		last += "  (" + r.State.LastSuccess.Local().Format("2006-01-02 15:04:05") + ")"
	}
	line("last success", last)
	if !r.State.LastAttempt.IsZero() && r.State.Duration > 0 {
		line("last run", ago(r.State.LastAttempt)+", took "+r.State.Duration.Round(time.Millisecond).String())
	}
	line("next sync", in(r.NextDue))
	line("head / size", shortSHA(r.State.Head)+"  /  "+humanSize(r.State.SizeBytes))
	if r.State.LastError != "" {
		b.line(detailLabelStyle.Render("error"), " ", errorTextStyle.Render(wrap(mirror.StripControl(r.State.LastError), w-15)))
	}
	return b.String()
}
