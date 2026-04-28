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

	lineNo         lipgloss.Style
	commit         lipgloss.Style
	checkpoint     lipgloss.Style
	selectedMarker lipgloss.Style
	helpKey        lipgloss.Style
	helpSep        lipgloss.Style
}

type whyTUIModel struct {
	data     whyViewData
	selected int
	viewport viewport.Model
	styles   whyTUIStyles
	width    int
	height   int
	ready    bool
}

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
	s.commit = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	s.checkpoint = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.selectedMarker = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(agentDisplayMap["claude"].Color))
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
			m = m.refreshViewport()
		}
	case key.Matches(msg, keys.Down):
		if m.selected < len(m.data.Rows)-1 {
			m.selected++
			m = m.refreshViewport()
		}
	case key.Matches(msg, keys.Home):
		m.selected = 0
		m = m.refreshViewport()
		m.viewport.GotoTop()
	case key.Matches(msg, keys.End):
		if len(m.data.Rows) > 0 {
			m.selected = len(m.data.Rows) - 1
			m = m.refreshViewport()
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
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(m.renderFooter())
	return b.String()
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
	commit := whyStaticCommit(info.Hash)
	author := whyStaticAuthor(row, info)
	date := "-"
	if !info.AuthorTime.IsZero() {
		date = info.AuthorTime.Format("2006-01-02")
	} else if !row.AuthorTime.IsZero() {
		date = row.AuthorTime.Format("2006-01-02")
	}

	lineLabel := fmt.Sprintf("%s:%d", m.data.GitPath, row.FinalLine)
	first := fmt.Sprintf(
		"%s  commit %s  author %s  date %s",
		lineLabel,
		commit,
		author,
		date,
	)
	second := fmt.Sprintf(
		"checkpoint %s  agent %s  summary %s",
		whyStaticCheckpoint(info),
		m.styles.renderAgent(whyStaticAgent(info)),
		whyTUISummary(info),
	)
	return fitWhyTUILine(first, m.width) + "\n" + fitWhyTUILine(second, m.width) + "\n"
}

func (m whyTUIModel) renderRows() string {
	if len(m.data.Rows) == 0 {
		return "No blame lines for this file."
	}

	gutterWidth := m.gutterWidth()
	codeWidth := max(m.width-gutterWidth, 0)
	sourceLines := make([]string, len(m.data.Rows))
	for i, row := range m.data.Rows {
		sourceLines[i] = truncateDisplayWidth(row.Source, codeWidth, "")
	}
	highlightedLines := highlightWhyCodeLines(m.data.GitPath, sourceLines, m.styles.colorEnabled)

	var b strings.Builder
	for i, row := range m.data.Rows {
		line := m.renderGutter(i, row)
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

func (m whyTUIModel) renderGutter(rowIndex int, row whyBlameRow) string {
	marker := " "
	if rowIndex == m.selected {
		marker = m.styles.render(m.styles.selectedMarker, ">")
	}

	hash := plumbing.NewHash(row.CommitHash)
	info := m.commitInfoForRow(row)
	lineNo := fmt.Sprintf("%*d", whyLineColumnWidth(m.data.Rows), row.FinalLine)
	commit := whyStaticCommit(hash)
	checkpointID := whyStaticCheckpoint(info)

	if !m.styles.colorEnabled {
		return fmt.Sprintf("%s %s %-*s %-*s | ", marker, lineNo, whyCommitColumnWidth, commit, whyCheckpointColumnWidth, checkpointID)
	}

	return fmt.Sprintf(
		"%s %s %s %s | ",
		marker,
		m.styles.render(m.styles.lineNo, lineNo),
		m.styles.render(m.styles.commit, fmt.Sprintf("%-*s", whyCommitColumnWidth, commit)),
		m.styles.render(m.styles.checkpoint, fmt.Sprintf("%-*s", whyCheckpointColumnWidth, checkpointID)),
	)
}

func (m whyTUIModel) gutterWidth() int {
	if len(m.data.Rows) == 0 {
		return 0
	}
	sample := fmt.Sprintf(
		"%s %*d %-*s %-*s | ",
		">",
		whyLineColumnWidth(m.data.Rows),
		1,
		whyCommitColumnWidth,
		strings.Repeat("x", whyCommitColumnWidth),
		whyCheckpointColumnWidth,
		strings.Repeat("x", whyCheckpointColumnWidth),
	)
	return lipgloss.Width(sample)
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

func (s whyTUIStyles) renderAgent(agent string) string {
	if !s.colorEnabled || agent == "-" {
		return agent
	}
	display := whyAgentDisplay(agent)
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(display.Color)).Render(agent)
}

func whyAgentDisplay(agent string) agentDisplay {
	id := normalizeAgentString(agent)
	display, ok := agentDisplayMap[id]
	if !ok {
		return agentDisplayMap[agentUnknown]
	}
	return display
}

func fitWhyTUILine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	return truncateDisplayWidth(line, width, "")
}

func whyTUISummary(info whyCommitInfo) string {
	if strings.TrimSpace(info.Summary) != "" {
		return info.Summary
	}
	if strings.TrimSpace(info.Subject) != "" {
		return info.Subject
	}
	return whyNotGeneratedSummary
}
