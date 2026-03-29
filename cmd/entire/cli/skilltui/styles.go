package skilltui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
)

type tuiStyles struct {
	colorEnabled bool

	// UI elements
	title       lipgloss.Style
	selected    lipgloss.Style
	dim         lipgloss.Style
	bold        lipgloss.Style
	tabActive   lipgloss.Style
	tabInactive lipgloss.Style
	statusBar   lipgloss.Style
	errorFlash  lipgloss.Style

	// Tab bar & sections
	appTitle      lipgloss.Style
	tabUnderline  lipgloss.Style
	sectionHeader lipgloss.Style

	// Picker
	selectedRow  lipgloss.Style
	chipActive   lipgloss.Style
	chipDisabled lipgloss.Style

	// Skill-specific
	priorityHigh   lipgloss.Style
	priorityMedium lipgloss.Style
	priorityLow    lipgloss.Style
	diffAdd        lipgloss.Style
	diffRemove     lipgloss.Style
	success        lipgloss.Style
	friction       lipgloss.Style
	sparkline      lipgloss.Style
}

func newStyles() tuiStyles {
	useColor := termstyle.ShouldUseColor(os.Stdout)

	s := tuiStyles{colorEnabled: useColor}

	if !useColor {
		return s
	}

	green := lipgloss.Color("2")
	red := lipgloss.Color("1")
	gray := lipgloss.Color("8")
	amber := lipgloss.Color("214")

	s.title = lipgloss.NewStyle().Foreground(amber)
	s.selected = lipgloss.NewStyle().Foreground(amber)
	s.dim = lipgloss.NewStyle().Faint(true)
	s.bold = lipgloss.NewStyle().Bold(true)
	s.tabActive = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.tabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.statusBar = lipgloss.NewStyle().Faint(true)
	s.errorFlash = lipgloss.NewStyle().Foreground(red)

	s.appTitle = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.tabUnderline = lipgloss.NewStyle().Foreground(amber)
	s.sectionHeader = lipgloss.NewStyle().Bold(true).Faint(true)
	s.selectedRow = lipgloss.NewStyle().Bold(true)
	s.chipActive = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.chipDisabled = lipgloss.NewStyle().Faint(true)

	s.priorityHigh = lipgloss.NewStyle().Foreground(red).Bold(true)
	s.priorityMedium = lipgloss.NewStyle().Foreground(amber).Bold(true)
	s.priorityLow = lipgloss.NewStyle().Foreground(gray)
	s.diffAdd = lipgloss.NewStyle().Foreground(green)
	s.diffRemove = lipgloss.NewStyle().Foreground(red)
	s.success = lipgloss.NewStyle().Foreground(green)
	s.friction = lipgloss.NewStyle().Foreground(red)
	s.sparkline = lipgloss.NewStyle().Foreground(amber)

	return s
}

func (s tuiStyles) render(style lipgloss.Style, text string) string {
	if !s.colorEnabled {
		return text
	}
	return style.Render(text)
}
