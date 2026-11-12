package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"git-mirror/internal/app"
	"git-mirror/internal/config"
	"git-mirror/internal/mirror"
)

// editField identifies the repo attribute being edited.
type editField int

const (
	editInterval editField = iota
	editBranches
	editSSHKey
	editProxy
)

var editFieldNames = []string{"Sync interval", "Branch filter", "SSH identity", "Proxy"}

const branchesNote = `Comma separated branch names, one "*" per pattern (release/*). Only matching branches plus all tags ` +
	"are mirrored, branches that stop matching are removed. Empty mirrors every branch."

func (m tuiModel) updateEditMenu(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeList
		return m, nil
	}
	m.confirm = m.confirm.Update(k)
	idx, ok := m.confirm.Chosen()
	if !ok {
		return m, nil
	}
	if idx >= len(m.editFields) {
		m.mode = modeList // Cancel
		return m, nil
	}
	return m.startEditField(m.editFields[idx])
}

// editFieldsFor lists the attributes the edit menu offers for a repo. The
// ssh key only matters for upstreams fetched over ssh.
func editFieldsFor(r config.Repo) []editField {
	fields := []editField{editInterval, editBranches}
	if mirror.IsSSHURL(r.URL) {
		fields = append(fields, editSSHKey)
	}
	return append(fields, editProxy)
}

// startEditField opens the text editor for one attribute of the selected repo.
func (m tuiModel) startEditField(f editField) (tea.Model, tea.Cmd) {
	r, ok := m.selected()
	if !ok {
		m.mode = modeList
		return m, nil
	}
	m.editField = f
	label := editFieldNames[f] + " for " + r.Repo.Name
	switch f {
	case editInterval:
		cur := ""
		if r.Repo.Interval > 0 {
			cur = r.Repo.Interval.String()
		}
		m.input = newInputModel(label, "30m", cur, validateInterval).withNote(m.intervalNote())
	case editBranches:
		m.input = newInputModel(label, "main, release/*", strings.Join(r.Repo.Branches, ","),
			func(s string) error { _, err := config.ParseBranches(s); return err }).withNote(branchesNote)
	case editSSHKey:
		m.input = newInputModel(label, "deploy", r.Repo.SSHKey, nil).withNote(m.identityNote())
	case editProxy:
		m.input = newInputModel(label, "http://proxy.internal:8080", r.Repo.Proxy, nil).withNote(
			`A proxy url: http://, https://, socks5:// or socks5h://. "none" bypasses the global proxy for this repository. ` +
				"Empty uses the global setting (" + orDash(m.status.Settings.Proxy) + ").")
	}
	m.mode = modeEditField
	return m, m.input.Init()
}

// intervalNote explains the interval field with the current global default.
func (m tuiModel) intervalNote() string {
	return "A duration like 30m, 2h or 1h30m, at least 1m. Empty uses the global default (" +
		m.status.Settings.SyncInterval.String() + ")."
}

// identityNote explains the ssh key field and lists the stored identities.
func (m tuiModel) identityNote() string {
	names := make([]string, 0, len(m.identities))
	for _, id := range m.identities {
		names = append(names, id.Name)
	}
	note := "The name of an identity from the data dir"
	if len(names) > 0 {
		note += " (" + strings.Join(names, ", ") + ")"
	} else {
		note += " (none yet, press I to create one)"
	}
	return note + " or an absolute key path. Empty uses the global setting (" + sshKeyLabel(m.status.Settings.SSHKey) + ")."
}

func (m tuiModel) updateEditField(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.mode = modeList
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if !m.input.submitted {
		return m, cmd
	}
	m.mode = modeList
	r, ok := m.selected()
	if !ok {
		return m, nil
	}
	val := m.input.Value()
	var (
		opts     app.EditOptions
		what     string
		resync   bool
		fieldErr error
	)
	switch m.editField {
	case editInterval:
		var d time.Duration
		if val != "" {
			d, fieldErr = time.ParseDuration(val)
		}
		opts.Interval = &d
		what = "interval"
	case editBranches:
		bs, err := config.ParseBranches(val)
		if bs == nil {
			bs = []string{}
		}
		opts.Branches, fieldErr = &bs, err
		what, resync = "branches", true // apply the new filter right away
	case editSSHKey:
		opts.SSHKey = new(val)
		what = "ssh key"
	case editProxy:
		opts.Proxy = new(val)
		what = "proxy"
	}
	if fieldErr != nil {
		m = m.setNotice("", fieldErr)
		return m, nil
	}
	a, ctx, name := m.app, m.ctx, r.Repo.Name
	return m, func() tea.Msg {
		if err := a.Edit(ctx, name, opts); err != nil {
			return actionMsg{err: err}
		}
		text := what + " updated for " + name
		if !resync {
			return actionMsg{text: text}
		}
		if _, err := a.Sync(ctx, []string{name}, app.SyncOptions{}); err != nil {
			return actionMsg{text: text, err: fmt.Errorf("sync: %w", err)}
		}
		return actionMsg{text: text + ", sync started"}
	}
}
