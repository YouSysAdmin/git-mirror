package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type cloneItem struct {
	group string // "http" or "ssh", items are rendered in sections
	label string // shown in the notice after copying
	text  string // copied verbatim
	note  string // muted heading above the command, optional
}

// maxCloneTokens bounds the per-token HTTP lines so the digits 1-9 still
// cover every item on the clone screen.
const maxCloneTokens = 5

// cloneItems lists the copyable clone urls for the selected repository: the
// HTTP group first (one url per token that may access it), then the SSH
// group. The second result counts tokens left out.
func (m tuiModel) cloneItems(name string) (items []cloneItem, moreTokens int) {
	l := buildCloneLinks(m.status, "", name)
	if l.HTTP != "" {
		toks := tokensFor(m.tokens, name)
		if len(toks) > maxCloneTokens {
			moreTokens = len(toks) - maxCloneTokens
			toks = toks[:maxCloneTokens]
		}
		for _, t := range toks {
			items = append(items, cloneItem{"http", "http url (token " + t.Name + ")", l.withToken(t.Value), "token " + t.Name + " (" + t.Scope() + ")"})
		}
		if l.HTTPSelf {
			items = append(items, cloneItem{"http", "certificate url", l.CertURL,
				"self-signed certificate: fetch it once (curl -sk <url> > mirror.pem) and clone with git " + l.GitOpts})
		}
	}
	if l.SSH != "" {
		items = append(items, cloneItem{"ssh", "ssh url", l.SSH, ""})
	}
	return items, moreTokens
}

func (m tuiModel) updateClone(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok {
		m.mode = modeList
		return m, nil
	}
	key := k.String()
	switch {
	case key == "T":
		m.mode = modeToken
	case key == "K":
		m.mode = modeKeys
	case len(key) == 1 && key[0] >= '1' && key[0] <= '9':
		items, _ := m.cloneItems(r.Repo.Name)
		if i := int(key[0] - '1'); i < len(items) {
			return m, copyCmd(items[i].label, items[i].text)
		}
	default:
		m.mode = modeList
	}
	return m, nil
}

func (m tuiModel) viewClone(w int) string {
	r, ok := m.selected()
	if !ok {
		return ""
	}
	var b sb
	b.add(titleStyle.Render("Clone " + r.Repo.Name))
	if n := m.viewNotice(w); n != "" {
		b.add("  ", n)
	}
	b.line()
	if !m.status.Server.Running {
		b.line(warnStyle.Render("server is not running, commands show the configured endpoints"))
	}
	b.line(mutedStyle.Render("press a number to copy the url to the clipboard"))

	items, moreTokens := m.cloneItems(r.Repo.Name)
	if len(items) == 0 {
		b.line()
		b.line(mutedStyle.Render("no endpoints enabled: git-mirror config set listen :8080 / ssh_listen :2222"))
	}
	group := ""
	for i, it := range items {
		if it.group != group {
			group = it.group
			b.line()
			b.line(sectionStyle.Render(strings.ToUpper(group)), "  ", mutedStyle.Render(m.cloneGroupHint(group, r.Repo.Name)))
		}
		if it.note != "" {
			b.line("    ", mutedStyle.Render(it.note))
		}
		b.line(hintKeyStyle.Render(fmt.Sprintf("[%d]", i+1)), " ", textStyle.Render(truncate(it.text, w-4)))
		if it.group == "http" && moreTokens > 0 && (i+1 == len(items) || items[i+1].group != "http") {
			b.line("    ", mutedStyle.Render(fmt.Sprintf("%d more token(s) may access this repository, see T or: git-mirror token ls", moreTokens)))
		}
	}
	b.line()
	if m.status.Settings.PublicHost == "" {
		b.line(mutedStyle.Render(wrap("host name taken from this machine. Override with: git-mirror config set public_host <name>", w)))
	}
	b.line(mutedStyle.Render("from a shell: git-mirror url " + r.Repo.Name + " [--ssh] [--token]"))
	b.line("", hints("1-9", "copy", "T", "tokens", "K", "keys", "any key", "back"))
	return b.String()
}

// cloneGroupHint explains the access rule of a clone section for the repo.
func (m tuiModel) cloneGroupHint(group, name string) string {
	switch group {
	case "http":
		toks := tokensFor(m.tokens, name)
		if len(toks) == 0 {
			return warnStyle.Render("no token has access to this repository, press T to add one")
		}
		return fmt.Sprintf("%d of %d token(s) may access this repository", len(toks), len(m.tokens))
	case "ssh":
		if m.status.Settings.SSHAllowAnyKey {
			return "any client key is accepted (ssh_allow_any_key)"
		}
		_, keys := repoAccess(m.tokens, m.keys, name)
		return fmt.Sprintf("client key must be in authorized_keys, %d of %d key(s) may access this repository", len(keys), len(m.keys))
	}
	return ""
}

// endpointLabel shows the effective listen address of an endpoint and marks
