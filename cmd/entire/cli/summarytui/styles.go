package summarytui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/stringutil"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
)

type styles struct {
	colorEnabled    bool
	appTitle        lipgloss.Style
	dim             lipgloss.Style
	statusBar       lipgloss.Style
	filterLabel     lipgloss.Style
	filterActive    lipgloss.Style
	filterInactive  lipgloss.Style
	filterSeparator lipgloss.Style
	listHeader      lipgloss.Style
	listSelected    lipgloss.Style
	listSelectedBg  lipgloss.Style
	listNormal      lipgloss.Style
	listAccent      lipgloss.Style
	boxStyle        lipgloss.Style
	boxTitle        lipgloss.Style
	detailLabel     lipgloss.Style
	detailValue     lipgloss.Style
	bullet          lipgloss.Style
	emptyState      lipgloss.Style
	errorText       lipgloss.Style
	sessionCount    lipgloss.Style
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
	darkBg := lipgloss.Color("236")
	borderGray := lipgloss.Color("240")

	s.appTitle = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.dim = lipgloss.NewStyle().Faint(true)
	s.statusBar = lipgloss.NewStyle().Faint(true)

	// Filter bar
	s.filterLabel = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.filterActive = lipgloss.NewStyle().Foreground(amber).Underline(true)
	s.filterInactive = lipgloss.NewStyle().Foreground(dimGray)
	s.filterSeparator = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	s.sessionCount = lipgloss.NewStyle().Foreground(dimGray)

	// List pane
	s.listHeader = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.listSelected = lipgloss.NewStyle().Bold(true).Foreground(amber)
	s.listSelectedBg = lipgloss.NewStyle().Background(darkBg)
	s.listNormal = lipgloss.NewStyle().Foreground(gray)
	s.listAccent = lipgloss.NewStyle().Foreground(amber).Background(darkBg)

	// Detail pane boxes
	s.boxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderGray).
		Padding(0, 1)
	s.boxTitle = lipgloss.NewStyle().Bold(true).Foreground(amber)

	// Detail content
	s.detailLabel = lipgloss.NewStyle().Foreground(gray)
	s.detailValue = lipgloss.NewStyle()
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

func (s styles) renderBox(title, content string, width int) string {
	box := s.boxStyle.Width(max(0, width-2)) // account for border
	titleStr := s.render(s.boxTitle, title)
	if !s.colorEnabled {
		return titleStr + "\n" + content
	}
	box = box.BorderTop(true).
		BorderBottom(true).
		BorderLeft(true).
		BorderRight(true)
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStr,
		box.Render(content),
	)
}

func truncate(value string, limit int) string {
	return stringutil.TruncateRunes(value, limit, "...")
}
