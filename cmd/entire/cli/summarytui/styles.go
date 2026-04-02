package summarytui

import (
	"os"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
)

type styles struct {
	colorEnabled  bool
	appTitle      lipgloss.Style
	dim           lipgloss.Style
	statusBar     lipgloss.Style
	chipActive    lipgloss.Style
	chipInactive  lipgloss.Style
	tableHeader   lipgloss.Style
	tableCell     lipgloss.Style
	tableSelect   lipgloss.Style
	detailLabel   lipgloss.Style
	detailValue   lipgloss.Style
	sectionHeader lipgloss.Style
	bullet        lipgloss.Style
	emptyState    lipgloss.Style
	errorText     lipgloss.Style
}

func newStyles() styles {
	useColor := termstyle.ShouldUseColor(os.Stdout)
	s := styles{colorEnabled: useColor}
	if !useColor {
		return s
	}

	amber := lipgloss.Color("214")
	gray := lipgloss.Color("245")
	dimGray := lipgloss.Color("239")

	s.appTitle = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.dim = lipgloss.NewStyle().Faint(true)
	s.statusBar = lipgloss.NewStyle().Faint(true)
	s.chipActive = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.chipInactive = lipgloss.NewStyle().Foreground(gray)
	s.tableHeader = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.tableCell = lipgloss.NewStyle()
	s.tableSelect = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.detailLabel = lipgloss.NewStyle().Foreground(gray).Width(14)
	s.detailValue = lipgloss.NewStyle()
	s.sectionHeader = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.bullet = lipgloss.NewStyle().Foreground(gray)
	s.emptyState = lipgloss.NewStyle().Foreground(dimGray).Italic(true)
	s.errorText = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	return s
}

func (s styles) render(style lipgloss.Style, text string) string {
	if !s.colorEnabled {
		return text
	}
	return style.Render(text)
}

func (s styles) tableStyles() table.Styles {
	ts := table.DefaultStyles()
	if !s.colorEnabled {
		return ts
	}
	ts.Header = s.tableHeader
	ts.Cell = s.tableCell
	ts.Selected = s.tableSelect
	return ts
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
