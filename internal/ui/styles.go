package ui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Padding(0, 1)

	badgeEditable = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).Background(lipgloss.Color("42")).Padding(0, 1)
	badgeNoRevisions = lipgloss.NewStyle().
				Foreground(lipgloss.Color("0")).Background(lipgloss.Color("214")).Padding(0, 1)
	badgeUnverified = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).Background(lipgloss.Color("240")).Padding(0, 1)

	publishedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	unpublishedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	statusBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	okStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	promptStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	helpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	headerStyle    = lipgloss.NewStyle().Bold(true)
)

// badge renders the editability badge for a content type.
func badge(editable, verified bool) string {
	switch {
	case editable:
		return badgeEditable.Render("editable")
	case !verified:
		return badgeUnverified.Render("unverified — read only")
	default:
		return badgeNoRevisions.Render("revisions disabled — read only")
	}
}
