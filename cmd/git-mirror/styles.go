package main

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary   = lipgloss.Color("205")
	colorSuccess   = lipgloss.Color("42")
	colorMuted     = lipgloss.Color("241")
	colorError     = lipgloss.Color("196")
	colorHighlight = lipgloss.Color("39")
	colorCyan      = lipgloss.Color("86")
	colorWarn      = lipgloss.Color("214")
	colorText      = lipgloss.Color("252")

	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	okStyle        = lipgloss.NewStyle().Foreground(colorSuccess)
	mutedStyle     = lipgloss.NewStyle().Foreground(colorMuted)
	errorStyle     = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	errorTextStyle = lipgloss.NewStyle().Foreground(colorError)
	highlightStyle = lipgloss.NewStyle().Foreground(colorHighlight)
	warnStyle      = lipgloss.NewStyle().Foreground(colorWarn)
	textStyle      = lipgloss.NewStyle().Foreground(colorText)

	sidebarWidth = 40
	sidebarStyle = lipgloss.NewStyle().
			Width(sidebarWidth).
			BorderRight(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colorMuted).
			Padding(1, 2)
	contentStyle = lipgloss.NewStyle().Padding(1, 2)

	cursorStyle       = lipgloss.NewStyle().Foreground(colorPrimary)
	selectedItemStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	normalItemStyle   = lipgloss.NewStyle().Foreground(colorText)
	disabledItemStyle = lipgloss.NewStyle().Foreground(colorMuted)

	detailLabelStyle = lipgloss.NewStyle().Foreground(colorMuted).Width(14)
	detailValueStyle = lipgloss.NewStyle().Foreground(colorText)

	promptLabelStyle   = lipgloss.NewStyle().Foreground(colorHighlight).Bold(true)
	inputStyle         = lipgloss.NewStyle().Foreground(colorText)
	validationErrStyle = lipgloss.NewStyle().Foreground(colorError).MarginTop(1)
	spinnerStyle       = lipgloss.NewStyle().Foreground(colorPrimary)
	hintStyle          = lipgloss.NewStyle().Foreground(colorMuted)
	hintKeyStyle       = lipgloss.NewStyle().Foreground(colorText)

	noticeOKStyle  = lipgloss.NewStyle().Foreground(colorSuccess)
	noticeErrStyle = lipgloss.NewStyle().Foreground(colorError)

	sectionStyle = lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	dividerStyle = lipgloss.NewStyle().Foreground(colorMuted)
)

const logo = `╔═╗╦╔╦╗  ╔╦╗╦╦═╗╦═╗╔═╗╦═╗
║ ╦║ ║   ║║║║╠╦╝╠╦╝║ ║╠╦╝
╚═╝╩ ╩   ╩ ╩╩╩╚═╩╚═╚═╝╩╚═`
