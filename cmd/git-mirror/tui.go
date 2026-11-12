package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"git-mirror/internal/access"
	"git-mirror/internal/api"
	"git-mirror/internal/app"
	"git-mirror/internal/authkeys"
	"git-mirror/internal/config"
	"git-mirror/internal/identity"
	"git-mirror/internal/mirror"
)

type statusMsg struct {
	status api.Status
	err    error
}

type keysMsg struct {
	keys []authkeys.Key
	err  error
}

type tokensMsg struct {
	tokens []config.Token
	err    error
}

type identitiesMsg struct {
	ids []identity.Identity
	err error
}

type tickMsg time.Time

// actionMsg reports the outcome of an add/remove/toggle/sync action.
type actionMsg struct {
	text string
	err  error
}

// mode is the screen currently shown, it selects the update and view handlers.
type mode int

const (
	modeList mode = iota
	modeFilter
	modeAdd
	modeConfirmRemove
	modeEditMenu  // choose which field of the selected repo to edit
	modeEditField // text input for that field
	modeHelp
	modeKeys
	modeKeysAdd     // add wizard: key line, then repos
	modeKeysField   // edit the repos of the selected key
	modeKeysConfirm // remove confirmation
	modeClone       // clone links of the selected repo
	modeToken       // HTTP access tokens
	modeTokenAdd    // add wizard: name, then repos
	modeTokenField  // edit the repos of the selected token
	modeTokenConfirm
	modeIdentities // private keys for upstream fetches
	modeIdentityAdd
	modeIdentityConfirm
)

// refreshEvery is the period of the background status/keys/tokens refresh.
const refreshEvery = 2 * time.Second

// Field notes shared by the access screens (keys, tokens, identities).
const (
	scopeNote = `Mirror names or patterns with one "*" (team/*), comma separated. Empty grants access to every mirror.`
	nameNote  = "Letters, digits, '.', '_' and '-'."
)

type tuiModel struct {
	app *app.App
	ctx context.Context

	status    api.Status
	statusErr error
	loaded    bool

	mode     mode
	filter   string
	filtered []int
	cursor   int

	// input is the text field of whichever mode is currently asking for text
	// (add wizard, interval editor, key editor). Only one is active at a time.
	input   inputModel
	confirm choiceModel
	spinner spinner.Model

	// edit menu / field editor
	editField  editField
	editFields []editField // menu entries for the selected repo, in menu order

	// help screen, scrollable
	help viewport.Model

	// add wizard
	addStep     addStep
	addPlan     []addStep // steps shown for this run, in order
	addURL      string
	addName     string
	addInterval time.Duration
	addBranches []string
	addSSHKey   string
	addProxy    string
	addTokens   []string // token names granted access
	addKeys     []string // client key fingerprints granted access
	check       checklistModel

	// ssh client keys screen
	keys        []authkeys.Key
	keysErr     error
	keysCursor  int
	keysAddLine string // first step of the add wizard

	// http access tokens (read from the data dir)
	tokens       []config.Token
	tokensErr    error
	tokensCursor int
	tokenAddName string        // first step of the add wizard
	tokenAction  confirmAction // what modeTokenConfirm confirms

	// identities screen
	identities       []identity.Identity
	identitiesErr    error
	identitiesCursor int
	identityGen      bool   // add wizard generates instead of importing
	identityAddName  string // first step of the import wizard

	notice    string
	noticeErr bool
	noticeAt  time.Time

	width, height int
	quitting      bool
}

func runTUI(ctx context.Context, a *app.App) error {
	if err := a.Dir.Ensure(); err != nil {
		return err
	}
	if _, err := mirror.Version(ctx); err != nil {
		return fmt.Errorf("git is required: %w", err)
	}
	m := newTUIModel(ctx, a)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx), tea.WithOutput(os.Stdout))
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
		return nil // interrupted by signal
	}
	return err
}

func newTUIModel(ctx context.Context, a *app.App) tuiModel {
	sp := spinner.New()
	// MiniDot frames are exactly one cell wide, so a syncing row keeps the
	// same column layout as the others.
	sp.Spinner = spinner.MiniDot
	sp.Style = spinnerStyle
	return tuiModel{
		app:     a,
		ctx:     ctx,
		spinner: sp,
		width:   100,
		height:  30,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(tea.WindowSize(), m.fetchStatus(), m.fetchKeys(), m.fetchTokens(), m.fetchIdentities(), m.spinner.Tick, tick())
}

func (m tuiModel) fetchTokens() tea.Cmd {
	dir := m.app.Dir
	return func() tea.Msg {
		ts, err := config.LoadTokens(dir)
		if err != nil {
			return tokensMsg{err: err}
		}
		return tokensMsg{tokens: ts.Tokens}
	}
}

func (m tuiModel) fetchIdentities() tea.Cmd {
	dir := m.app.Dir
	return func() tea.Msg {
		ids, err := identity.List(dir)
		return identitiesMsg{ids: ids, err: err}
	}
}

func (m tuiModel) fetchKeys() tea.Cmd {
	dir := m.app.Dir
	return func() tea.Msg {
		keys, err := authkeys.Load(dir)
		return keysMsg{keys: keys, err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m tuiModel) fetchStatus() tea.Cmd {
	a := m.app
	ctx := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		st, err := a.Status(ctx)
		return statusMsg{status: st, err: err}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.resizeHelp(), nil

	case tickMsg:
		return m, tea.Batch(m.fetchStatus(), m.fetchKeys(), m.fetchTokens(), m.fetchIdentities(), tick())

	case tokensMsg:
		m.tokensErr = msg.err
		if msg.err == nil {
			m.tokens = msg.tokens
		}
		if m.tokensCursor >= len(m.tokens) {
			m.tokensCursor = max(0, len(m.tokens)-1)
		}
		return m, nil

	case identitiesMsg:
		m.identitiesErr = msg.err
		if msg.err == nil {
			m.identities = msg.ids
		}
		if m.identitiesCursor >= len(m.identities) {
			m.identitiesCursor = max(0, len(m.identities)-1)
		}
		return m, nil

	case keysMsg:
		m.keysErr = msg.err
		if msg.err == nil {
			m.keys = msg.keys
		}
		if m.keysCursor >= len(m.keys) {
			m.keysCursor = max(0, len(m.keys)-1)
		}
		return m, nil

	case statusMsg:
		m.loaded = true
		m.statusErr = msg.err
		if msg.err == nil {
			m.status = msg.status
			m = m.applyFilter()
		}
		return m, nil

	case actionMsg:
		m = m.setNotice(msg.text, msg.err)
		return m, tea.Batch(m.fetchStatus(), m.fetchKeys(), m.fetchTokens(), m.fetchIdentities())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeFilter:
			return m.updateFilter(msg)
		case modeAdd:
			return m.updateAdd(msg)
		case modeConfirmRemove:
			return m.updateConfirm(msg)
		case modeEditMenu:
			return m.updateEditMenu(msg)
		case modeEditField:
			return m.updateEditField(msg)
		case modeHelp:
			return m.updateHelp(msg)
		case modeKeys:
			return m.updateKeys(msg)
		case modeKeysAdd, modeKeysField:
			return m.updateKeysAdd(msg)
		case modeKeysConfirm:
			return m.updateKeysConfirm(msg)
		case modeClone:
			return m.updateClone(msg)
		case modeToken:
			return m.updateToken(msg)
		case modeTokenAdd, modeTokenField:
			return m.updateTokenAdd(msg)
		case modeTokenConfirm:
			return m.updateTokenConfirm(msg)
		case modeIdentities:
			return m.updateIdentities(msg)
		case modeIdentityAdd:
			return m.updateIdentityAdd(msg)
		case modeIdentityConfirm:
			return m.updateIdentityConfirm(msg)
		}
	}

	// Forward blink etc. to the focused input.
	var cmd tea.Cmd
	switch m.mode {
	case modeAdd, modeEditField, modeKeysAdd, modeKeysField, modeTokenAdd, modeTokenField, modeIdentityAdd:
		m.input, cmd = m.input.Update(msg)
	}
	return m, cmd
}

// viewNotice renders the transient action result shown next to a title.
func (m tuiModel) viewNotice(w int) string {
	if m.notice != "" && time.Since(m.noticeAt) < 8*time.Second {
		if m.noticeErr {
			return noticeErrStyle.Render("✗ " + truncate(m.notice, w-20))
		}
		return noticeOKStyle.Render("✓ " + truncate(m.notice, w-20))
	}
	if m.statusErr != nil {
		return noticeErrStyle.Render("✗ " + truncate(m.statusErr.Error(), w-20))
	}
	return ""
}

func (m tuiModel) View() string {
	if m.quitting {
		return ""
	}
	contentWidth := max(m.width-sidebarWidth-4, 30)
	panelHeight := max(m.height-2, 0)
	// MaxHeight keeps both panels from pushing the top of the screen out of
	// view on short terminals.
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		sidebarStyle.Height(panelHeight).MaxHeight(panelHeight).Render(m.viewSidebar()),
		contentStyle.Width(contentWidth).Height(panelHeight).MaxHeight(panelHeight).Render(m.viewContent(contentWidth-4)),
	)
}

func (m tuiModel) viewContent(w int) string {
	switch m.mode {
	case modeAdd:
		return m.viewAdd(w)

	case modeConfirmRemove:
		return m.confirm.View() + "\n" + hints("↑/↓", "choose", "enter", "confirm", "esc", "cancel")

	case modeEditMenu:
		return m.confirm.View() + "\n" + hints("↑/↓", "choose", "enter", "edit", "esc", "cancel")
	case modeEditField:
		return m.input.View(w) + "\n" + hints("enter", "save", "esc", "cancel")

	case modeHelp:
		return m.viewHelp(w)
	case modeKeys:
		return m.viewKeys(w)
	case modeKeysAdd:
		return titleStyle.Render("Add SSH client key") + "\n\n" + m.viewKeysAddCrumbs() + m.input.View(w) + "\n" + hints("enter", "next", "esc", "cancel")
	case modeKeysField:
		return titleStyle.Render("SSH client key scope") + "\n\n" + m.input.View(w) + "\n" + hints("enter", "save", "esc", "cancel")
	case modeKeysConfirm, modeTokenConfirm, modeIdentityConfirm:
		return m.confirm.View() + "\n" + hints("↑/↓", "choose", "enter", "confirm", "esc", "cancel")
	case modeClone:
		return m.viewClone(w)
	case modeToken:
		return m.viewToken(w)
	case modeTokenAdd:
		return titleStyle.Render("Add HTTP token") + "\n\n" + m.viewTokenAddCrumbs() + m.input.View(w) + "\n" + hints("enter", "next", "esc", "cancel")
	case modeTokenField:
		return titleStyle.Render("HTTP token scope") + "\n\n" + m.input.View(w) + "\n" + hints("enter", "save", "esc", "cancel")
	case modeIdentities:
		return m.viewIdentities(w)
	case modeIdentityAdd:
		title := "Import SSH identity"
		if m.identityGen {
			title = "Generate SSH identity"
		}
		return titleStyle.Render(title) + "\n\n" + m.viewIdentityAddCrumbs() + m.input.View(w) + "\n" + hints("enter", "next", "esc", "cancel")
	}
	return m.viewList(w)
}

// applyFilter recomputes the visible rows from the current filter text.
// A fresh slice is built every time so model copies never share a backing array.
func (m tuiModel) applyFilter() tuiModel {
	filtered := make([]int, 0, len(m.status.Repos))
	needle := strings.ToLower(m.filter)
	for i, r := range m.status.Repos {
		if needle == "" || strings.Contains(strings.ToLower(r.Repo.Name), needle) || strings.Contains(strings.ToLower(r.Repo.URL), needle) {
			filtered = append(filtered, i)
		}
	}
	m.filtered = filtered
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
	return m
}

func (m tuiModel) selected() (api.RepoStatus, bool) {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return api.RepoStatus{}, false
	}
	return m.status.Repos[m.filtered[m.cursor]], true
}

// setNotice records a transient message shown next to screen titles.
func (m tuiModel) setNotice(text string, err error) tuiModel {
	m.noticeAt = time.Now()
	m.noticeErr = err != nil
	m.notice = text
	if err != nil {
		if text != "" {
			m.notice = text + ": " + err.Error()
		} else {
			m.notice = err.Error()
		}
	}
	return m
}

func (m tuiModel) listHeight() int {
	// content height minus header (3), filter line (2), details (~9), hints (2)
	return max(3, m.height-2-3-2-9-2)
}

func validateURL(s string) error {
	if s == "" {
		return fmt.Errorf("url is required")
	}
	if strings.ContainsAny(s, " \t") {
		return fmt.Errorf("url must not contain spaces")
	}
	_, err := mirror.NameFromURL(s)
	return err
}

func validateInterval(s string) error {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("use a duration like 30m, 2h, 1h30m")
	}
	if d < time.Minute {
		return fmt.Errorf("interval must be at least 1m")
	}
	return nil
}

// validateScope accepts an empty value (every mirror) or a repo list.
func validateScope(s string) error {
	_, err := access.ParseRepoList(s)
	return err
}
