package main

import (
	tea "github.com/charmbracelet/bubbletea"
)

type choiceModel struct {
	title   string
	body    string
	choices []string
	cursor  int
	chosen  int
}

func newChoiceModel(title, body string, choices []string) choiceModel {
	return choiceModel{title: title, body: body, choices: choices, chosen: -1}
}

func (m choiceModel) Update(msg tea.Msg) choiceModel {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			m.chosen = m.cursor
		}
	}
	return m
}

func (m choiceModel) View() string {
	var b sb
	b.line(promptLabelStyle.Render(m.title))
	if m.body != "" {
		b.line(textStyle.Render(m.body))
	}
	b.WriteString("\n")
	for i, c := range m.choices {
		if i == m.cursor {
			b.line(cursorStyle.Render("> "), selectedItemStyle.Render(c))
		} else {
			b.line("  ", normalItemStyle.Render(c))
		}
	}
	return b.String()
}

func (m choiceModel) Chosen() (int, bool) { return m.chosen, m.chosen >= 0 }
