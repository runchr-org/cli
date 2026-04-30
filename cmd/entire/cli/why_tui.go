package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/go-git/go-git/v6/plumbing"
)

type whyTUIStyles struct {
	statusStyles

	time       lipgloss.Style
	author     lipgloss.Style
	commit     lipgloss.Style
	lineNo     lipgloss.Style
	checkpoint lipgloss.Style
	helpKey    lipgloss.Style
	helpSep    lipgloss.Style
}

type whyTUIModel struct {
	data     whyViewData
	selected int
	viewport viewport.Model
	styles   whyTUIStyles
	width    int
	height   int
	ready    bool

	lineWidth int
}

const (
	whyTUICheckpointMaxWidth = 12
	whyTUISelectedBackground = "\x1b[48;5;236m"
	whyTUIReset              = "\x1b[0m"
)

var runWhyTUI = defaultRunWhyTUI

func defaultRunWhyTUI(ctx context.Context, w io.Writer, data whyViewData) error {
	ss := newStatusStyles(w)
	m := newWhyTUIModel(data, ss)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("why TUI: %w", err)
	}
	return nil
}

func newWhyTUIModel(data whyViewData, ss statusStyles) whyTUIModel {
	if ss.width <= 0 {
		ss.width = 60
	}

	m := whyTUIModel{
		data:     data,
		viewport: viewport.New(ss.width, 1),
		styles:   newWhyTUIStyles(ss),
		width:    ss.width,
	}
	return m.refreshViewport()
}

func newWhyTUIStyles(ss statusStyles) whyTUIStyles {
	s := whyTUIStyles{statusStyles: ss}
	if !ss.colorEnabled {
		return s
	}

	s.lineNo = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.time = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.author = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	s.commit = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	s.checkpoint = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.helpKey = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	s.helpSep = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	return s
}

func (m whyTUIModel) Init() tea.Cmd {
	return nil
}

func (m whyTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { //nolint:ireturn // bubbletea interface
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m = m.refreshViewport()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m whyTUIModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) { //nolint:ireturn // bubbletea pattern
	switch {
	case key.Matches(msg, keys.Quit), key.Matches(msg, keys.Back):
		return m, tea.Quit
	case key.Matches(msg, keys.Up):
		if m.selected > 0 {
			m.selected--
			m = m.ensureSelectedVisible()
		}
	case key.Matches(msg, keys.Down):
		if m.selected < len(m.data.Rows)-1 {
			m.selected++
			m = m.ensureSelectedVisible()
		}
	case key.Matches(msg, keys.Home):
		m.selected = 0
		m.viewport.GotoTop()
	case key.Matches(msg, keys.End):
		if len(m.data.Rows) > 0 {
			m.selected = len(m.data.Rows) - 1
			m.viewport.GotoBottom()
		}
	case key.Matches(msg, keys.Confirm):
		return m, nil
	}
	return m, nil
}

func (m whyTUIModel) View() string {
	if m.width == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString(m.renderViewport())
	b.WriteString("\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m whyTUIModel) renderViewport() string {
	view := m.viewport.View()
	selectedLine := m.selected - m.viewport.YOffset
	if selectedLine < 0 || selectedLine >= m.viewport.Height {
		return view
	}

	lines := strings.Split(view, "\n")
	if selectedLine >= len(lines) {
		return view
	}

	lines[selectedLine] = m.renderSelectedViewportLine(lines[selectedLine])
	return strings.Join(lines, "\n")
}

func (m whyTUIModel) renderSelectedViewportLine(line string) string {
	if line == "" {
		line = ">"
	} else {
		line = ">" + line[1:]
	}
	if !m.styles.colorEnabled {
		return line
	}
	line += strings.Repeat(" ", max(m.width-lipgloss.Width(line), 0))
	return whyTUISelectedBackground + strings.ReplaceAll(line, whyTUIReset, whyTUIReset+whyTUISelectedBackground) + whyTUIReset
}

func (m whyTUIModel) refreshViewport() whyTUIModel {
	headerHeight := strings.Count(m.renderHeader(), "\n")
	vpHeight := m.height - headerHeight - 1
	if vpHeight < 1 {
		vpHeight = 1
	}

	if !m.ready {
		m.viewport = viewport.New(m.width, vpHeight)
		m.ready = true
	} else {
		m.viewport.Width = m.width
		m.viewport.Height = vpHeight
	}
	m.lineWidth = whyLineColumnWidth(m.data.Rows)
	m.viewport.SetContent(m.renderRows())
	return m.ensureSelectedVisible()
}

func (m whyTUIModel) ensureSelectedVisible() whyTUIModel {
	if len(m.data.Rows) == 0 || m.viewport.Height <= 0 {
		return m
	}
	if m.selected < m.viewport.YOffset {
		m.viewport.SetYOffset(m.selected)
		return m
	}
	bottom := m.viewport.YOffset + m.viewport.Height - 1
	if m.selected > bottom {
		m.viewport.SetYOffset(m.selected - m.viewport.Height + 1)
	}
	return m
}

func (m whyTUIModel) renderHeader() string {
	if len(m.data.Rows) == 0 {
		return fitWhyTUILine(m.styles.render(m.styles.bold, m.data.GitPath)+" has no blame lines\n", m.width)
	}

	row := m.data.Rows[m.selected]
	info := m.commitInfoForRow(row)

	lineLabel := fmt.Sprintf("%s:%d", m.data.GitPath, row.FinalLine)
	header := fmt.Sprintf(
		"%s  checkpoint %s",
		lineLabel,
		whyStaticCheckpoint(info),
	)
	return fitWhyTUILine(header, m.width) + "\n"
}

func (m whyTUIModel) renderRows() string {
	if len(m.data.Rows) == 0 {
		return "No blame lines for this file."
	}

	lineWidth := m.currentLineWidth()
	gutterWidth := m.gutterWidth(lineWidth)
	codeWidth := max(m.width-gutterWidth, 0)
	sourceLines := make([]string, len(m.data.Rows))
	for i, row := range m.data.Rows {
		sourceLines[i] = truncateDisplayWidth(row.Source, codeWidth, "")
	}
	highlightedLines := highlightWhyCodeLines(m.data.GitPath, sourceLines, m.styles.colorEnabled)

	var b strings.Builder
	for i, row := range m.data.Rows {
		line := m.renderGutter(row, lineWidth)
		if codeWidth > 0 && i < len(highlightedLines) {
			line += highlightedLines[i]
		}
		b.WriteString(line)
		if i < len(m.data.Rows)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m whyTUIModel) renderGutter(row whyBlameRow, lineWidth int) string {
	info := m.commitInfoForRow(row)
	lineNo := fmt.Sprintf("%*d", lineWidth, row.FinalLine)
	lineColumn := whyTUIColumn(lineNo, lineWidth, m.styles.lineNo, m.styles.colorEnabled)
	checkpoint := whyTUIColumn(whyTUICheckpoint(info), whyTUICheckpointMaxWidth, m.styles.checkpoint, m.styles.colorEnabled)
	return "  " + strings.Join([]string{
		whyTUIColumn(whyStaticTime(row), whyTimeMaxWidth, m.styles.time, m.styles.colorEnabled),
		whyTUIColumn(whyStaticAuthor(row), whyAuthorMaxWidth, m.styles.author, m.styles.colorEnabled),
		whyTUIColumn(whyStaticCommit(row), whyCommitColumnWidth, m.styles.commit, m.styles.colorEnabled),
		checkpoint,
		lineColumn,
	}, " ") + " | "
}

func (m whyTUIModel) currentLineWidth() int {
	if m.lineWidth > 0 {
		return m.lineWidth
	}
	return whyLineColumnWidth(m.data.Rows)
}

func (m whyTUIModel) gutterWidth(lineWidth int) int {
	if len(m.data.Rows) == 0 {
		return 0
	}
	width := 0
	for _, row := range m.data.Rows {
		width = max(width, lipgloss.Width(m.renderGutter(row, lineWidth)))
	}
	return width
}

func (m whyTUIModel) renderFooter() string {
	sep := m.styles.render(m.styles.helpSep, " · ")
	fullHelp := m.styles.helpItem("↑/↓, j/k", "scroll") +
		sep + m.styles.helpItem("home/end, g/G", "top/bottom") +
		sep + m.styles.helpItem(keys.Quit.Help().Key, keys.Quit.Help().Desc)
	standardHelp := m.styles.helpItem("↑/↓", "scroll") +
		sep + m.styles.helpItem("home/end", "top/bottom") +
		sep + m.styles.helpItem(keys.Quit.Help().Key, keys.Quit.Help().Desc)
	shortHelp := m.styles.helpItem("↑/↓", "scroll")
	quitHelp := m.styles.helpItem(keys.Quit.Help().Key, keys.Quit.Help().Desc)
	helpChoices := []string{fullHelp, standardHelp, shortHelp, quitHelp}

	position := m.styles.render(m.styles.dim, m.positionText())
	for _, help := range helpChoices {
		gap := m.width - lipgloss.Width(help) - lipgloss.Width(position)
		if gap >= 1 {
			return help + strings.Repeat(" ", gap) + position
		}
	}

	positionWidth := lipgloss.Width(position)
	if m.width <= 0 {
		return ""
	}
	if m.width < positionWidth {
		return strings.Repeat(" ", m.width)
	}
	return strings.Repeat(" ", m.width-positionWidth) + position
}

func (m whyTUIModel) positionText() string {
	if len(m.data.Rows) == 0 {
		return "0/0"
	}
	return fmt.Sprintf("%d/%d", m.selected+1, len(m.data.Rows))
}

func (m whyTUIModel) commitInfoForRow(row whyBlameRow) whyCommitInfo {
	hash := plumbing.NewHash(row.CommitHash)
	info, ok := m.data.Commits[hash]
	if !ok {
		info = whyCommitInfo{Hash: hash}
	}
	return info
}

func (s whyTUIStyles) helpItem(keyLabel, desc string) string {
	return s.render(s.helpKey, keyLabel) + " " + desc
}

func whyTUIColumn(value string, width int, style lipgloss.Style, colorEnabled bool) string {
	value = truncateDisplayWidth(value, width, "...")
	value += strings.Repeat(" ", max(width-lipgloss.Width(value), 0))
	if !colorEnabled {
		return value
	}
	return style.Render(value)
}

func whyTUICheckpoint(info whyCommitInfo) string {
	return truncateDisplayWidth(whyStaticCheckpoint(info), whyTUICheckpointMaxWidth, "...")
}

func fitWhyTUILine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	return truncateDisplayWidth(line, width, "")
}
