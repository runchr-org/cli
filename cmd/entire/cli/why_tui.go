package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/go-git/go-git/v6/plumbing"
)

type whyTUIStyles struct {
	statusStyles

	bold        lipgloss.Style
	dim         lipgloss.Style
	time        lipgloss.Style
	author      lipgloss.Style
	commit      lipgloss.Style
	lineNo      lipgloss.Style
	checkpoint  lipgloss.Style
	columnHead  lipgloss.Style
	headerLabel lipgloss.Style
	headerValue lipgloss.Style
	helpKey     lipgloss.Style
	helpSep     lipgloss.Style
}

type whyTUIModel struct {
	data     whyViewData
	selected int
	viewport viewport.Model
	styles   whyTUIStyles
	width    int
	height   int

	lineWidth int
}

const (
	whyTUICheckpointMaxWidth = 12
	whyTUIHeaderLabelWidth   = 11
	whyTUISelectedBackground = "\x1b[48;5;236m"
	whyTUIReset              = "\x1b[0m"
	whyTUIResetShort         = "\x1b[m"
	whyTUIResetHyperlink     = "\x1b]8;;\x07"
	whyTUICommitURLPrefix    = "https://entire.io/gh/entireio/cli/commit/"
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
		data:      data,
		viewport:  viewport.New(ss.width, 1),
		styles:    newWhyTUIStyles(ss),
		width:     ss.width,
		lineWidth: whyLineColumnWidth(data.Rows),
	}
	return m.refreshViewport()
}

func newWhyTUIStyles(ss statusStyles) whyTUIStyles {
	s := whyTUIStyles{statusStyles: ss}
	if !ss.colorEnabled {
		return s
	}

	s.bold = lipgloss.NewStyle().Bold(true)
	s.dim = lipgloss.NewStyle().Faint(true)
	s.lineNo = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.time = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.author = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	s.commit = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	s.checkpoint = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	s.columnHead = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	s.headerLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#fb923c")).Bold(true)
	s.headerValue = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
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
		if msg.Width == m.width && msg.Height == m.height {
			return m, nil
		}
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
	}
	return m, nil
}

func (m whyTUIModel) View() string {
	if m.width == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString(m.renderColumnHeader())
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
	line = markWhyTUISelectedLine(line)
	if !m.styles.colorEnabled {
		return line
	}
	line += strings.Repeat(" ", max(m.width-lipgloss.Width(line), 0))
	line = strings.ReplaceAll(line, whyTUIReset, whyTUIReset+whyTUISelectedBackground)
	line = strings.ReplaceAll(line, whyTUIResetShort, whyTUIResetShort+whyTUISelectedBackground)
	return whyTUISelectedBackground + line + whyTUIReset
}

func markWhyTUISelectedLine(line string) string {
	if line == "" {
		return ">"
	}

	prefixLen := leadingSGRLen(line)
	if prefixLen >= len(line) {
		return line + ">"
	}
	if line[prefixLen] == ' ' {
		return line[:prefixLen] + ">" + line[prefixLen+1:]
	}
	return line[:prefixLen] + ">" + line[prefixLen:]
}

func leadingSGRLen(s string) int {
	offset := 0
	for offset < len(s) && s[offset] == '\x1b' {
		if offset+1 >= len(s) || s[offset+1] != '[' {
			break
		}
		end := strings.IndexByte(s[offset:], 'm')
		if end < 0 {
			break
		}
		offset += end + 1
	}
	return offset
}

func (m whyTUIModel) refreshViewport() whyTUIModel {
	headerHeight := strings.Count(m.renderHeader(), "\n") + strings.Count(m.renderColumnHeader(), "\n")
	vpHeight := m.height - headerHeight - 1
	if vpHeight < 1 {
		vpHeight = 1
	}

	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
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

	title := fmt.Sprintf("%s:%d", m.data.GitPath, row.FinalLine)
	metadata := strings.Join([]string{
		m.renderHeaderCommitField(row),
		m.renderHeaderField("author", whyStaticAuthor(row)),
		m.renderHeaderField("date", whyStaticTime(row)),
		m.renderHeaderField("checkpoint", whyStaticCheckpoint(info)),
	}, "  ")
	return fitWhyTUILine(title, m.width) + "\n" + fitWhyTUILine(metadata, m.width) + "\n\n"
}

func (m whyTUIModel) renderHeaderCommitField(row whyBlameRow) string {
	label := whyColumn("COMMIT:", whyTUIHeaderLabelWidth)
	value := m.renderCommitHash(whyStaticCommit(row), row.CommitHash, m.styles.headerValue)
	return m.styles.render(m.styles.headerLabel, label) + " " + value
}

func (m whyTUIModel) renderHeaderField(label, value string) string {
	label = whyColumn(strings.ToUpper(label)+":", whyTUIHeaderLabelWidth)
	return m.styles.render(m.styles.headerLabel, label) + " " + m.styles.render(m.styles.headerValue, value)
}

func (m whyTUIModel) renderColumnHeader() string {
	if len(m.data.Rows) == 0 {
		return ""
	}

	line := "  " + strings.Join([]string{
		whyColumn("TIME", whyTimeMaxWidth),
		whyColumn("AUTHOR", whyAuthorMaxWidth),
		whyColumn("COMMIT", whyCommitColumnWidth),
		whyColumn("CHECKPOINT", whyTUICheckpointMaxWidth),
		whyColumn("LINE", m.lineWidth),
	}, " ") + " | CODE"
	line = fitWhyTUILine(line, m.width)
	return m.styles.render(m.styles.columnHead, line) + "\n"
}

func (m whyTUIModel) renderRows() string {
	if len(m.data.Rows) == 0 {
		return "No blame lines for this file."
	}

	codeWidth := max(m.width-whyGutterWidth(m.lineWidth), 0)
	sourceLines := make([]string, len(m.data.Rows))
	for i, row := range m.data.Rows {
		sourceLines[i] = row.Source
	}
	highlightedLines := highlightWhyCodeLines(m.data.GitPath, sourceLines, m.styles.colorEnabled, codeWidth)

	var b strings.Builder
	for i, row := range m.data.Rows {
		line := m.renderGutter(row, m.lineWidth)
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
	return "  " + strings.Join([]string{
		m.styles.render(m.styles.time, whyColumn(whyStaticTime(row), whyTimeMaxWidth)),
		m.styles.render(m.styles.author, whyColumn(whyStaticAuthor(row), whyAuthorMaxWidth)),
		m.renderCommitHash(whyColumn(whyStaticCommit(row), whyCommitColumnWidth), row.CommitHash, m.styles.commit),
		m.styles.render(m.styles.checkpoint, whyColumn(whyTUICheckpoint(info), whyTUICheckpointMaxWidth)),
		m.styles.render(m.styles.lineNo, whyColumn(lineNo, lineWidth)),
	}, " ") + " | "
}

func (m whyTUIModel) renderCommitHash(text, commitHash string, style lipgloss.Style) string {
	rendered := m.styles.render(style, text)
	if !m.styles.colorEnabled || commitHash == "" {
		return rendered
	}
	return lipgloss.NewStyle().Hyperlink(whyTUICommitURL(commitHash)).Render(rendered)
}

func whyTUICommitURL(commitHash string) string {
	return whyTUICommitURLPrefix + commitHash
}

// whyGutterWidth derives the gutter width from the fixed column widths plus
// the variable-width line-number column. Five fixed columns are joined with
// single spaces, prefixed with "  ", and suffixed with " | ".
func whyGutterWidth(lineWidth int) int {
	const (
		leadingPad  = 2
		columnGaps  = 4
		trailingSep = 3
	)
	return leadingPad + whyTimeMaxWidth + whyAuthorMaxWidth + whyCommitColumnWidth +
		whyTUICheckpointMaxWidth + lineWidth + columnGaps + trailingSep
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

func (s whyTUIStyles) render(style lipgloss.Style, text string) string {
	if !s.colorEnabled {
		return text
	}
	return style.Render(text)
}

func (s whyTUIStyles) helpItem(keyLabel, desc string) string {
	return s.render(s.helpKey, keyLabel) + " " + desc
}

func whyTUICheckpoint(info whyCommitInfo) string {
	return truncateDisplayWidth(whyStaticCheckpoint(info), whyTUICheckpointMaxWidth, "...")
}

func fitWhyTUILine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	return cutWhyTUILine(line, width)
}

func cutWhyTUILine(line string, width int) string {
	var b strings.Builder
	visibleWidth := 0
	sawSGR := false
	sawHyperlink := false

	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			next, isSGR, isHyperlink := consumeWhyTUIEscape(line, i)
			sawSGR = sawSGR || isSGR
			sawHyperlink = sawHyperlink || isHyperlink
			b.WriteString(line[i:next])
			i = next
			continue
		}

		r, size := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		runeWidth := lipgloss.Width(string(r))
		if visibleWidth+runeWidth > width {
			if sawSGR {
				b.WriteString(whyTUIReset)
			}
			if sawHyperlink {
				b.WriteString(whyTUIResetHyperlink)
			}
			return b.String()
		}
		b.WriteRune(r)
		visibleWidth += runeWidth
		i += size
	}
	return b.String()
}

func consumeWhyTUIEscape(line string, start int) (int, bool, bool) {
	if start+1 >= len(line) {
		return start + 1, false, false
	}
	switch line[start+1] {
	case '[':
		return consumeWhyTUICSI(line, start), true, false
	case ']':
		return consumeWhyTUIOSC(line, start), false, true
	default:
		return start + 1, false, false
	}
}

func consumeWhyTUICSI(line string, start int) int {
	for i := start + 2; i < len(line); i++ {
		if line[i] >= 0x40 && line[i] <= 0x7e {
			return i + 1
		}
	}
	return len(line)
}

func consumeWhyTUIOSC(line string, start int) int {
	for i := start + 2; i < len(line); i++ {
		if line[i] == '\a' {
			return i + 1
		}
		if line[i] == '\x1b' && i+1 < len(line) && line[i+1] == '\\' {
			return i + 2
		}
	}
	return len(line)
}
