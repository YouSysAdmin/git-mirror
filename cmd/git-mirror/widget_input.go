package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type inputModel struct {
	label     string
	help      string // short example shown inside the empty field
	note      string // explanation and defaults, rendered under the label
	input     textinput.Model
	validator func(string) error
	err       string
	submitted bool
}

func newInputModel(label, help, initial string, validator func(string) error) inputModel {
	ti := textinput.New()
	ti.Placeholder = help
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 60
	ti.SetValue(initial)
	ti.CursorEnd()
	return inputModel{label: label, help: help, input: ti, validator: validator}
}

// withNote sets the explanation rendered under the label. Defaults and
// available values belong here, not in the placeholder, which is cut to the
// field width.
func (m inputModel) withNote(note string) inputModel {
	m.note = note
	return m
}

func (m inputModel) Init() tea.Cmd { return textinput.Blink }

func (m inputModel) Update(msg tea.Msg) (inputModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "enter" {
		val := strings.TrimSpace(m.input.Value())
		if m.validator != nil {
			if err := m.validator(val); err != nil {
				m.err = err.Error()
				return m, nil
			}
		}
		m.submitted = true
		m.err = ""
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if _, isKey := msg.(tea.KeyMsg); isKey {
		m.err = ""
	}
	return m, cmd
}

// View renders the prompt for a pane w cells wide.
func (m inputModel) View(w int) string {
	var b sb
	b.line(promptLabelStyle.Render(m.label))
	if m.note != "" {
		b.line(mutedStyle.Render(wrap(m.note, max(20, w))))
	}
	b.line()
	b.line(m.input.View())
	if m.err != "" {
		b.line(validationErrStyle.Render("✗ " + m.err))
	}
	return b.String()
}

func (m inputModel) Value() string { return strings.TrimSpace(m.input.Value()) }
