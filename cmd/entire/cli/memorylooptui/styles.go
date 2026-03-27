package memorylooptui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
)

type tuiStyles struct {
	colorEnabled bool

	// Status colors
	active     lipgloss.Style
	candidate  lipgloss.Style
	suppressed lipgloss.Style
	archived   lipgloss.Style

	// Kind colors
	repoRule         lipgloss.Style
	workflowRule     lipgloss.Style
	agentInstruction lipgloss.Style
	skillPatch       lipgloss.Style
	antiPattern      lipgloss.Style

	// UI elements
	title       lipgloss.Style
	selected    lipgloss.Style
	dim         lipgloss.Style
	bold        lipgloss.Style
	tabActive   lipgloss.Style
	tabInactive lipgloss.Style
	statusBar   lipgloss.Style
	errorFlash  lipgloss.Style

	// Tab bar & filter chips
	appTitle           lipgloss.Style
	tabUnderline       lipgloss.Style
	filterChipActive   lipgloss.Style
	filterChipInactive lipgloss.Style
	sectionHeader      lipgloss.Style
}

func newStyles() tuiStyles {
	useColor := termstyle.ShouldUseColor(os.Stdout)

	s := tuiStyles{colorEnabled: useColor}

	if !useColor {
		return s
	}

	green := lipgloss.Color("2")
	red := lipgloss.Color("1")
	yellow := lipgloss.Color("3")
	gray := lipgloss.Color("8")
	amber := lipgloss.Color("214")
	purple := lipgloss.Color("5")

	s.active = lipgloss.NewStyle().Foreground(green)
	s.candidate = lipgloss.NewStyle().Foreground(yellow)
	s.suppressed = lipgloss.NewStyle().Foreground(red)
	s.archived = lipgloss.NewStyle().Foreground(gray)

	s.repoRule = lipgloss.NewStyle().Foreground(green)
	s.workflowRule = lipgloss.NewStyle().Foreground(green)
	s.agentInstruction = lipgloss.NewStyle().Foreground(amber)
	s.skillPatch = lipgloss.NewStyle().Foreground(purple)
	s.antiPattern = lipgloss.NewStyle().Foreground(red)

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
	s.filterChipActive = lipgloss.NewStyle().
		Background(lipgloss.Color("214")).
		Foreground(lipgloss.Color("0")).
		Bold(true).
		Padding(0, 1)
	s.filterChipInactive = lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Padding(0, 1)
	s.sectionHeader = lipgloss.NewStyle().Bold(true).Faint(true)

	return s
}

func (s tuiStyles) render(style lipgloss.Style, text string) string {
	if !s.colorEnabled {
		return text
	}
	return style.Render(text)
}
