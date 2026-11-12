package main

import (
	tea "github.com/charmbracelet/bubbletea"
)

type checkItem struct {
	id      string
	label   string
	note    string // muted text after the label, optional
	checked bool
	locked  bool // shown checked, cannot be toggled
}

type checklistModel struct {
	title  string
	body   string
	items  []checkItem
	cursor int
	done   bool
}

func newChecklistModel(title, body string, items []checkItem) checklistModel {
	m := checklistModel{title: title, body: body, items: items}
	// Start on the first item the user can actually change.
	for i, it := range items {
		if !it.locked {
			m.cursor = i
			break
		}
	}
	return m
}

func (m checklistModel) Update(msg tea.Msg) checklistModel {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m
	}
	switch k.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case " ", "x":
		if m.cursor < len(m.items) && !m.items[m.cursor].locked {
			m.items[m.cursor].checked = !m.items[m.cursor].checked
		}
	case "enter":
		m.done = true
	}
	return m
}

// View renders the list for a pane w cells wide.
func (m checklistModel) View(w int) string {
	var b sb
	b.line(promptLabelStyle.Render(m.title))
	if m.body != "" {
		b.line(mutedStyle.Render(wrap(m.body, max(20, w))))
	}
	b.line()
	for i, it := range m.items {
		box := "[ ]"
		if it.checked || it.locked {
			box = "[x]"
		}
		row := box + " " + it.label
		if it.note != "" {
			row += "  " + mutedStyle.Render(it.note)
		}
		switch {
		case i == m.cursor && it.locked:
			b.line(cursorStyle.Render("> "), disabledItemStyle.Render(row))
		case i == m.cursor:
			b.line(cursorStyle.Render("> "), selectedItemStyle.Render(row))
		case it.locked:
			b.line("  ", disabledItemStyle.Render(row))
		default:
			b.line("  ", normalItemStyle.Render(row))
		}
	}
	return b.String()
}

// Done reports whether enter was pressed.
func (m checklistModel) Done() bool { return m.done }

// Selected returns the ids of the items the user checked (locked ones excluded).
func (m checklistModel) Selected() []string {
	var out []string
	for _, it := range m.items {
		if it.checked && !it.locked {
			out = append(out, it.id)
		}
	}
	return out
}
