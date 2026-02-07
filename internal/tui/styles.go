package tui

import "github.com/charmbracelet/lipgloss"

type Styles struct {
	Header       lipgloss.Style
	Repo         lipgloss.Style
	Workspace    lipgloss.Style
	Active       lipgloss.Style
	Cursor       lipgloss.Style
	HelpBar      lipgloss.Style
	Error        lipgloss.Style
	Form         lipgloss.Style
	Dim          lipgloss.Style
	Separator    lipgloss.Style
	Notification lipgloss.Style
}

func DefaultStyles() Styles {
	return Styles{
		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("4")).
			MarginBottom(1),
		Repo: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6")),
		Workspace: lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")),
		Active: lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Bold(true),
		Cursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Bold(true),
		HelpBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
		Error: lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Bold(true),
		Form: lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")),
		Dim: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
		Separator: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
		Notification: lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")),
	}
}
