package cli

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/go-git/go-git/v6/plumbing"
)

var whyANSIRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func testWhyTUIModel() whyTUIModel {
	hashA := plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	hashB := plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	rows := []whyBlameRow{
		testWhyTUIRow(hashA, 1, "package main"),
		testWhyTUIRow(hashA, 2, ""),
		testWhyTUIRow(hashB, 3, "func main() {"),
		testWhyTUIRow(hashB, 4, "\tprintln(\"hi\")"),
		testWhyTUIRow(hashB, 5, "}"),
		testWhyTUIRow(hashA, 6, "// end"),
	}
	data := whyViewData{
		GitPath: "cmd/main.go",
		Rows:    rows,
		Commits: map[plumbing.Hash]whyCommitInfo{
			hashA: {
				Hash: hashA,
			},
			hashB: {
				Hash: hashB,
			},
		},
	}

	m := newWhyTUIModel(data, statusStyles{colorEnabled: false, width: 80})
	m.height = 6
	return m.refreshViewport()
}

func testWhyTUIRow(hash plumbing.Hash, line int, source string) whyBlameRow {
	return whyBlameRow{
		whyBlameLine: whyBlameLine{
			CommitHash: hash.String(),
			FinalLine:  line,
			Author:     "Fallback Author",
			Source:     source,
		},
	}
}

func updateWhyTUIModel(t *testing.T, m whyTUIModel, msg tea.Msg) (whyTUIModel, tea.Cmd) {
	t.Helper()

	updated, cmd := m.Update(msg)
	result, ok := updated.(whyTUIModel)
	if !ok {
		t.Fatalf("Update returned %T, want whyTUIModel", updated)
	}
	return result, cmd
}

func whyRuneKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestWhyTUIModel_DownKeysMoveSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "arrow down", key: tea.KeyMsg{Type: tea.KeyDown}},
		{name: "vim down", key: whyRuneKey('j')},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, _ := updateWhyTUIModel(t, testWhyTUIModel(), tt.key)
			if m.selected != 1 {
				t.Fatalf("selected = %d, want 1", m.selected)
			}
		})
	}
}

func TestWhyTUIModel_UpKeysMoveSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "arrow up", key: tea.KeyMsg{Type: tea.KeyUp}},
		{name: "vim up", key: whyRuneKey('k')},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := testWhyTUIModel()
			m.selected = 2
			m = m.refreshViewport()
			m, _ = updateWhyTUIModel(t, m, tt.key)
			if m.selected != 1 {
				t.Fatalf("selected = %d, want 1", m.selected)
			}
		})
	}
}

func TestWhyTUIModel_TopBottomKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  tea.KeyMsg
		want int
	}{
		{name: "home", key: tea.KeyMsg{Type: tea.KeyHome}, want: 0},
		{name: "vim top", key: whyRuneKey('g'), want: 0},
		{name: "end", key: tea.KeyMsg{Type: tea.KeyEnd}, want: 5},
		{name: "vim bottom", key: whyRuneKey('G'), want: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := testWhyTUIModel()
			if tt.want == 0 {
				m.selected = len(m.data.Rows) - 1
				m = m.refreshViewport()
			}
			m, _ = updateWhyTUIModel(t, m, tt.key)
			if m.selected != tt.want {
				t.Fatalf("selected = %d, want %d", m.selected, tt.want)
			}
		})
	}
}

func TestWhyTUIModel_QuitKey(t *testing.T) {
	t.Parallel()

	_, cmd := updateWhyTUIModel(t, testWhyTUIModel(), whyRuneKey('q'))
	if cmd == nil {
		t.Fatal("expected quit command, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected QuitMsg")
	}
}

func TestWhyTUIModel_SelectedLineRemainsVisible(t *testing.T) {
	t.Parallel()

	m := testWhyTUIModel()
	for range 5 {
		var cmd tea.Cmd
		m, cmd = updateWhyTUIModel(t, m, whyRuneKey('j'))
		if cmd != nil {
			t.Fatalf("unexpected command while moving selection")
		}
	}

	if m.selected != 5 {
		t.Fatalf("selected = %d, want 5", m.selected)
	}
	if m.selected < m.viewport.YOffset || m.selected >= m.viewport.YOffset+m.viewport.Height {
		t.Fatalf("selected row %d outside viewport offset %d height %d", m.selected, m.viewport.YOffset, m.viewport.Height)
	}
}

func TestWhyTUIModel_ViewMarksSelectedRow(t *testing.T) {
	t.Parallel()

	m := testWhyTUIModel()
	view := m.View()
	firstLine := whyTUIViewLineContaining(t, view, "package main")
	if !strings.HasPrefix(firstLine, "> ") {
		t.Fatalf("selected line should start with marker: %q", firstLine)
	}

	var cmd tea.Cmd
	for range 2 {
		m, cmd = updateWhyTUIModel(t, m, whyRuneKey('j'))
		if cmd != nil {
			t.Fatalf("unexpected command while moving selection")
		}
	}

	view = m.View()
	newLine := whyTUIViewLineContaining(t, view, "func main()")
	if !strings.HasPrefix(newLine, "> ") {
		t.Fatalf("new selected line should start with marker: %q", newLine)
	}
}

func TestWhyTUIModel_ViewHighlightsSelectedRow(t *testing.T) {
	t.Parallel()

	m := newWhyTUIModel(testWhyTUIModel().data, statusStyles{colorEnabled: true, width: 80})
	m.height = 6
	m = m.refreshViewport()

	line := whyTUIViewLineContaining(t, m.View(), "package main")
	if !strings.Contains(line, "\x1b[48;5;236m") {
		t.Fatalf("selected line should include highlight background: %q", line)
	}
}

func TestWhyTUIModel_ViewShowsStickyGutterHeader(t *testing.T) {
	t.Parallel()

	m := testWhyTUIModel()
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 3 {
		t.Fatalf("view should include dynamic header, padding, and gutter header:\n%s", view)
	}
	if lines[1] != "" {
		t.Fatalf("expected blank padding line between dynamic and gutter headers, got %q", lines[1])
	}

	headerLine := whyTUIViewLineContaining(t, view, "TIME")
	for _, want := range []string{"AUTHOR", "COMMIT", "CHECKPOINT", "LINE", "| CODE"} {
		if !strings.Contains(headerLine, want) {
			t.Fatalf("gutter header missing %q: %q", want, headerLine)
		}
	}
	if strings.Index(view, "TIME") > strings.Index(view, "package main") {
		t.Fatalf("gutter header should render above file rows:\n%s", view)
	}

	m, _ = updateWhyTUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	view = m.View()
	headerLine = whyTUIViewLineContaining(t, view, "TIME")
	if !strings.Contains(headerLine, "CHECKPOINT") {
		t.Fatalf("scrolled gutter header missing checkpoint label: %q", headerLine)
	}
	if strings.Index(view, "TIME") > strings.Index(view, "// end") {
		t.Fatalf("gutter header should stay above scrolled file rows:\n%s", view)
	}
}

func TestWhyTUIModel_GutterHeaderUsesHighlightedStyle(t *testing.T) {
	t.Parallel()

	styles := newWhyTUIStyles(statusStyles{colorEnabled: true})
	if !styles.columnHead.GetBold() {
		t.Fatal("gutter header style should be bold")
	}
	if styles.columnHead.GetForeground() == nil {
		t.Fatal("gutter header style should define a foreground color")
	}
	if styles.columnHead.GetFaint() {
		t.Fatal("gutter header style should not be dim/faint")
	}

	m := newWhyTUIModel(testWhyTUIModel().data, statusStyles{colorEnabled: true, width: 80})
	m.styles.columnHead = lipgloss.NewStyle().Transform(func(s string) string {
		return "highlighted:" + s
	})
	if header := m.renderColumnHeader(); !strings.Contains(header, "highlighted:") {
		t.Fatalf("gutter header should render through column header style: %q", header)
	}
}

func TestWhyTUIModel_SelectedMarkerPreservesLeadingANSI(t *testing.T) {
	t.Parallel()

	m := newWhyTUIModel(testWhyTUIModel().data, statusStyles{colorEnabled: true, width: 80})
	line := "\x1b[38;2;230;237;243m\"github.com/entireio/cli/redact\""
	got := m.renderSelectedViewportLine(line)

	if strings.Contains(got, ">[38;") {
		t.Fatalf("selected marker corrupted leading ANSI escape: %q", got)
	}
	if !strings.Contains(got, "\x1b[38;2;230;237;243m>") {
		t.Fatalf("selected marker should be inserted after leading ANSI escape: %q", got)
	}
}

func TestWhyTUIModel_GutterShowsBlameColumnsInRequestedOrder(t *testing.T) {
	t.Parallel()

	hash := plumbing.NewHash("c56b7ac719000000000000000000000000000000")
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	firstRow := testWhyTUIRow(hash, 15, "first")
	firstRow.Author = whyTestAuthor
	firstRow.AuthorTime = time.Now().Add(-6 * 24 * time.Hour)
	secondRow := testWhyTUIRow(hash, 16, "second")
	secondRow.Author = whyTestAuthor
	secondRow.AuthorTime = firstRow.AuthorTime
	data := whyViewData{
		GitPath: "cmd/main.go",
		Rows:    []whyBlameRow{firstRow, secondRow},
		Commits: map[plumbing.Hash]whyCommitInfo{
			hash: {
				Hash:         hash,
				CheckpointID: cpID,
			},
		},
	}
	m := newWhyTUIModel(data, statusStyles{colorEnabled: false, width: 140})
	lineWidth := whyLineColumnWidth(m.data.Rows)

	gutter := m.renderGutter(firstRow, lineWidth)
	for _, want := range []string{"6d ago", whyTestAuthor, "c56b7ac719", cpID.String(), "15"} {
		if !strings.Contains(gutter, want) {
			t.Fatalf("gutter missing %q: %q", want, gutter)
		}
	}
	if strings.Index(gutter, "6d ago") > strings.Index(gutter, whyTestAuthor) ||
		strings.Index(gutter, whyTestAuthor) > strings.Index(gutter, "c56b7ac719") ||
		strings.Index(gutter, "c56b7ac719") > strings.Index(gutter, cpID.String()) ||
		strings.Index(gutter, cpID.String()) > strings.Index(gutter, "15") {
		t.Fatalf("gutter columns rendered out of order: %q", gutter)
	}

	next := m.renderGutter(secondRow, lineWidth)
	for _, want := range []string{"6d ago", whyTestAuthor, "c56b7ac719", cpID.String(), "16"} {
		if !strings.Contains(next, want) {
			t.Fatalf("next gutter missing %q: %q", want, next)
		}
	}
}

func TestWhyTUIModel_GutterColumnsHaveFixedWidths(t *testing.T) {
	t.Parallel()

	hashA := plumbing.NewHash("c56b7ac719000000000000000000000000000000")
	hashB := plumbing.NewHash("d56b7ac719000000000000000000000000000000")
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	firstRow := testWhyTUIRow(hashA, 7, "short := true")
	firstRow.Author = whyTestAuthor
	firstRow.AuthorTime = time.Now().Add(-6 * 24 * time.Hour)
	secondRow := testWhyTUIRow(hashB, 100, "longer := false")
	secondRow.Author = "A"
	data := whyViewData{
		GitPath: "cmd/main.go",
		Rows:    []whyBlameRow{firstRow, secondRow},
		Commits: map[plumbing.Hash]whyCommitInfo{
			hashA: {
				Hash:         hashA,
				CheckpointID: cpID,
			},
			hashB: {
				Hash: hashB,
			},
		},
	}
	m := newWhyTUIModel(data, statusStyles{colorEnabled: false, width: 140})
	lineWidth := whyLineColumnWidth(m.data.Rows)
	firstGutter := m.renderGutter(firstRow, lineWidth)
	secondGutter := m.renderGutter(secondRow, lineWidth)

	if got, want := lipgloss.Width(firstGutter), lipgloss.Width(secondGutter); got != want {
		t.Fatalf("gutter widths differ: first=%d second=%d\nfirst:  %q\nsecond: %q", got, want, firstGutter, secondGutter)
	}
}

func TestWhyTUIModel_FooterDocumentsVisibleControls(t *testing.T) {
	t.Parallel()

	m := testWhyTUIModel()
	footer := m.renderFooter()
	wantParts := []string{
		"↑/↓, j/k scroll",
		"home/end, g/G top/bottom",
		"q quit",
	}
	lastIndex := -1
	for _, want := range wantParts {
		idx := strings.Index(footer, want)
		if idx == -1 {
			t.Fatalf("footer missing %q: %q", want, footer)
		}
		if idx < lastIndex {
			t.Fatalf("footer control %q rendered out of order: %q", want, footer)
		}
		lastIndex = idx
	}
	if got := lipgloss.Width(footer); got != m.width {
		t.Fatalf("footer width = %d, want %d: %q", got, m.width, footer)
	}
}

func TestWhyTUIModel_FooterFallsBackWithoutOverflow(t *testing.T) {
	t.Parallel()

	m := testWhyTUIModel()
	m.width = 50
	m.viewport.Width = 50
	footer := m.renderFooter()
	if got := lipgloss.Width(footer); got != m.width {
		t.Fatalf("compact footer width = %d, want %d: %q", got, m.width, footer)
	}
	for _, hidden := range []string{"j/k", "g/G"} {
		if strings.Contains(footer, hidden) {
			t.Fatalf("compact footer should drop %q: %q", hidden, footer)
		}
	}

	m.width = 20
	m.viewport.Width = 20
	footer = m.renderFooter()
	if got := lipgloss.Width(footer); got != m.width {
		t.Fatalf("narrow footer width = %d, want %d: %q", got, m.width, footer)
	}
}

func whyTUIViewLineContaining(t *testing.T, view, needle string) string {
	t.Helper()

	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(whyANSIRe.ReplaceAllString(line, ""), needle) {
			return line
		}
	}
	t.Fatalf("view missing %q:\n%s", needle, view)
	return ""
}
