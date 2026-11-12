package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"git-mirror/internal/app"
	"git-mirror/internal/authkeys"
	"git-mirror/internal/config"
	"git-mirror/internal/mirror"
)

type addStep int

const (
	addStepURL addStep = iota
	addStepName
	addStepInterval
	addStepBranches
	addStepSSHKey
	addStepProxy
	addStepTokens // checklist: tokens that get the new mirror added to their scope
	addStepKeys   // checklist: client keys that get the new mirror added to their scope
)

var addStepNames = []string{"URL", "Name", "Interval", "Branches", "SSH key", "Proxy", "Tokens", "Client keys"}

func (m tuiModel) startAdd() tuiModel {
	m.mode = modeAdd
	m.addURL, m.addName, m.addInterval, m.addBranches = "", "", 0, nil
	m.addSSHKey, m.addProxy, m.addTokens, m.addKeys = "", "", nil, nil
	m.addPlan = []addStep{addStepURL, addStepName, addStepInterval, addStepBranches, addStepSSHKey, addStepProxy}
	if len(m.tokens) > 0 {
		m.addPlan = append(m.addPlan, addStepTokens)
	}
	if len(m.keys) > 0 && !m.status.Settings.SSHAllowAnyKey {
		m.addPlan = append(m.addPlan, addStepKeys)
	}
	m.addStep = addStepURL
	m.input = newInputModel("Repository URL", "https://github.com/owner/repo.git", "", validateURL).
		withNote("https://, ssh:// or git@host:path. The mirror name is derived from it in the next step.")
	return m
}

// addIsChecklist reports whether the current wizard step is a multi-select.
func (m tuiModel) addIsChecklist() bool {
	return m.addStep == addStepTokens || m.addStep == addStepKeys
}

func (m tuiModel) updateAdd(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeList
		return m, nil
	}
	if m.addIsChecklist() {
		m.check = m.check.Update(k)
		if !m.check.Done() {
			return m, nil
		}
		if m.addStep == addStepTokens {
			m.addTokens = m.check.Selected()
		} else {
			m.addKeys = m.check.Selected()
		}
		return m.nextAddStep()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if !m.input.submitted {
		return m, cmd
	}
	val := m.input.Value()
	switch m.addStep {
	case addStepURL:
		m.addURL = val
		if !mirror.IsSSHURL(val) {
			m.addPlan = slices.DeleteFunc(m.addPlan, func(s addStep) bool { return s == addStepSSHKey })
		} else if !slices.Contains(m.addPlan, addStepSSHKey) {
			m.addPlan = slices.Insert(m.addPlan, slices.Index(m.addPlan, addStepProxy), addStepSSHKey)
		}
	case addStepName:
		m.addName = val
	case addStepInterval:
		if val != "" {
			m.addInterval, _ = time.ParseDuration(val)
		}
	case addStepBranches:
		m.addBranches, _ = config.ParseBranches(val)
	case addStepSSHKey:
		m.addSSHKey = val
	case addStepProxy:
		m.addProxy = val
	}
	return m.nextAddStep()
}

// nextAddStep advances the wizard and prepares the prompt of the new step,
// or submits when the plan is exhausted.
func (m tuiModel) nextAddStep() (tea.Model, tea.Cmd) {
	i := slices.Index(m.addPlan, m.addStep) + 1
	if i >= len(m.addPlan) {
		m.mode = modeList
		return m, m.doAdd()
	}
	m.addStep = m.addPlan[i]
	switch m.addStep {
	case addStepName:
		name, _ := mirror.NameFromURL(m.addURL)
		name = config.ApplyNameStyle(name, m.status.Settings.NameStyle)
		m.input = newInputModel("Mirror name", "owner/repo", name, config.ValidateName).
			withNote("Directory under repos/ and the name in clone urls. Slashes group mirrors, e.g. team/repo.")
	case addStepInterval:
		m.input = newInputModel("Sync interval", "30m", "", validateInterval).withNote(m.intervalNote())
	case addStepBranches:
		m.input = newInputModel("Branches to mirror", "main, release/*", "",
			func(s string) error { _, err := config.ParseBranches(s); return err }).withNote(branchesNote)
	case addStepSSHKey:
		m.input = newInputModel("SSH key for fetching", "deploy", "", nil).withNote(m.identityNote())
	case addStepProxy:
		m.input = newInputModel("Proxy for fetching", "http://proxy.internal:8080", "", nil).withNote(
			`A proxy url: http://, https://, socks5:// or socks5h://. "none" bypasses the global proxy for this repository. ` +
				"Empty uses the global setting (" + orDash(m.status.Settings.Proxy) + ").")
	case addStepTokens:
		items := make([]checkItem, 0, len(m.tokens))
		for _, t := range m.tokens {
			it := checkItem{id: t.Name, label: t.Name, note: "repos: " + t.Scope()}
			if t.Allows(m.addName) {
				it.locked = true
				it.note += ", already has access"
			}
			items = append(items, it)
		}
		m.check = newChecklistModel("HTTP tokens that may clone "+m.addName,
			"Checked tokens get the mirror added to their scope. Tokens that already cover it are shown locked.", items)
		return m, nil
	case addStepKeys:
		items := make([]checkItem, 0, len(m.keys))
		for _, k := range m.keys {
			it := checkItem{id: k.Fingerprint(), label: keyLabel(k), note: "repos: " + k.Scope()}
			if k.Allows(m.addName) {
				it.locked = true
				it.note += ", already has access"
			}
			items = append(items, it)
		}
		m.check = newChecklistModel("SSH client keys that may clone "+m.addName,
			"Checked keys get the mirror added to their scope. Keys that already cover it are shown locked.", items)
		return m, nil
	}
	return m, m.input.Init()
}

func (m tuiModel) doAdd() tea.Cmd {
	a, ctx := m.app, m.ctx
	url, name, iv, bs := m.addURL, m.addName, m.addInterval, m.addBranches
	sshKey, proxy, tokens, keys := m.addSSHKey, m.addProxy, m.addTokens, m.addKeys
	tokenScopes := map[string][]string{}
	for _, t := range m.tokens {
		tokenScopes[t.Name] = t.Repos
	}
	keyScopes := map[string][]string{}
	for _, k := range m.keys {
		keyScopes[k.Fingerprint()] = k.Repos
	}
	return func() tea.Msg {
		r, err := a.Add(ctx, url, app.AddOptions{Name: name, Interval: iv, Branches: bs, SSHKey: sshKey, Proxy: proxy})
		if err != nil {
			return actionMsg{err: err}
		}
		// Grant access: append the new mirror to the scope of the chosen
		// tokens and client keys. A scope that was empty means every mirror,
		// but such credentials are locked in the checklist and never end up here.
		var grantErrs []error
		for _, t := range tokens {
			if _, err := config.SetTokenRepos(a.Dir, t, append(slices.Clone(tokenScopes[t]), r.Name)); err != nil {
				grantErrs = append(grantErrs, fmt.Errorf("token %s: %w", t, err))
			}
		}
		for _, fp := range keys {
			if _, err := authkeys.SetRepos(a.Dir, fp, append(slices.Clone(keyScopes[fp]), r.Name)); err != nil {
				grantErrs = append(grantErrs, fmt.Errorf("key %s: %w", fp, err))
			}
		}
		if err := errors.Join(grantErrs...); err != nil {
			return actionMsg{text: "added " + r.Name, err: fmt.Errorf("grant access: %w", err)}
		}
		via, err := a.Sync(ctx, []string{r.Name}, app.SyncOptions{Local: false})
		if err != nil {
			return actionMsg{text: "added " + r.Name, err: fmt.Errorf("sync: %w", err)}
		}
		if via {
			return actionMsg{text: "added " + r.Name + ", sync queued"}
		}
		return actionMsg{text: "added and synced " + r.Name}
	}
}

func (m tuiModel) viewAdd(w int) string {
	var crumbs []string
	cur := slices.Index(m.addPlan, m.addStep)
	for i, s := range m.addPlan {
		label := addStepNames[s]
		switch {
		case i < cur:
			crumbs = append(crumbs, okStyle.Render("✓ "+label))
		case i == cur:
			crumbs = append(crumbs, highlightStyle.Bold(true).Render("▸ "+label))
		default:
			crumbs = append(crumbs, mutedStyle.Render("  "+label))
		}
	}
	var b sb
	b.line(titleStyle.Render("Add repository"))
	b.line(wrap(strings.Join(crumbs, mutedStyle.Render("  ›  ")), w), "\n")
	done := func(s addStep) bool { return slices.Index(m.addPlan, s) < cur }
	line := func(k, v string) { b.line(detailLabelStyle.Render(k), " ", detailValueStyle.Render(truncate(v, w-15))) }
	if done(addStepURL) {
		line("url", m.addURL)
	}
	if done(addStepName) {
		line("name", m.addName)
	}
	if done(addStepInterval) {
		iv := "default (" + m.status.Settings.SyncInterval.String() + ")"
		if m.addInterval > 0 {
			iv = m.addInterval.String()
		}
		line("interval", iv)
	}
	if done(addStepBranches) {
		line("branches", branchesLabel(m.addBranches))
	}
	if done(addStepSSHKey) {
		v := "global (" + sshKeyLabel(m.status.Settings.SSHKey) + ")"
		if m.addSSHKey != "" {
			v = m.addSSHKey
		}
		line("ssh key", v)
	}
	if done(addStepProxy) {
		v := "global (" + orDash(m.status.Settings.Proxy) + ")"
		if m.addProxy != "" {
			v = m.addProxy
		}
		line("proxy", v)
	}
	if done(addStepTokens) {
		line("tokens", "grant access to "+orDash(strings.Join(m.addTokens, ", ")))
	}
	if cur > 0 {
		b.WriteString("\n")
	}
	if m.addIsChecklist() {
		b.WriteString(m.check.View(w))
		b.add("\n", hints("space", "toggle", "enter", "next", "esc", "cancel"))
	} else {
		b.WriteString(m.input.View(w))
		b.add("\n", hints("enter", "next", "esc", "cancel"))
	}
	return b.String()
}
