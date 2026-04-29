package cli

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/go-git/go-git/v6/plumbing"
)

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
				Hash:       hashA,
				Subject:    "initial",
				Author:     "Pat Example",
				AuthorTime: time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
				Summary:    "initial",
			},
			hashB: {
				Hash:       hashB,
				Subject:    "update main",
				Author:     "Sam Example",
				AuthorTime: time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
				Summary:    "update main",
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

func TestWhyTUIModel_GutterShowsBlameMetadataColumns(t *testing.T) {
	t.Parallel()

	hash := plumbing.NewHash("c56b7ac719000000000000000000000000000000")
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	firstRow := testWhyTUIRow(hash, 15, "first")
	firstRow.BlockIndex = 0
	secondRow := testWhyTUIRow(hash, 16, "second")
	secondRow.BlockIndex = 0
	data := whyViewData{
		GitPath: "cmd/main.go",
		Rows:    []whyBlameRow{firstRow, secondRow},
		Blocks: []whyBlameBlock{
			{CommitHash: hash.String(), StartLine: 15, EndLine: 16, StartRow: 0, EndRow: 1},
		},
		Commits: map[plumbing.Hash]whyCommitInfo{
			hash: {
				Hash:         hash,
				Author:       "Example Author",
				AuthorTime:   time.Now().Add(-6 * 24 * time.Hour),
				CheckpointID: cpID,
				Checkpoint: whyCheckpointInfo{
					Agents: []types.AgentType{types.AgentType("Claude Code")},
				},
			},
		},
	}
	m := newWhyTUIModel(data, statusStyles{colorEnabled: false, width: 140})

	gutter := m.renderGutter(0, firstRow)
	for _, want := range []string{"ago", "Example Author", "(Claude Code)", "c56b7ac719", cpID.String(), "15"} {
		if !strings.Contains(gutter, want) {
			t.Fatalf("gutter missing %q: %q", want, gutter)
		}
	}
	if !strings.Contains(gutter, "Example Author (Claude Code) c56b7ac719 "+cpID.String()) {
		t.Fatalf("gutter should use compact single-space metadata columns: %q", gutter)
	}
	for _, unwanted := range []string{"Example Author  ", "(Claude Code)  ", "c56b7ac719  "} {
		if strings.Contains(gutter, unwanted) {
			t.Fatalf("gutter contains unnecessary internal spacing %q: %q", unwanted, gutter)
		}
	}

	continuation := m.renderGutter(1, secondRow)
	for _, hidden := range []string{"Example Author", "(Claude Code)", "c56b7ac719", cpID.String()} {
		if strings.Contains(continuation, hidden) {
			t.Fatalf("continuation gutter should not repeat %q: %q", hidden, continuation)
		}
	}
	if !strings.Contains(continuation, "16") {
		t.Fatalf("continuation gutter missing line number: %q", continuation)
	}
}

func TestWhyTUIAgents_RendersAllCheckpointAgents(t *testing.T) {
	t.Parallel()

	info := whyCommitInfo{
		Checkpoint: whyCheckpointInfo{
			Agents: []types.AgentType{types.AgentType("claude"), types.AgentType("Codex")},
		},
	}

	if got := whyTUIAgents(info); got != "(Claude Code, Codex)" {
		t.Fatalf("whyTUIAgents() = %q, want (Claude Code, Codex)", got)
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

func TestWhyTUIAgentDisplayUsesActivityPalette(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		agent string
		id    string
	}{
		{name: "claude", agent: "Claude Code", id: "claude"},
		{name: "gemini", agent: "Gemini CLI", id: "gemini"},
		{name: "copilot", agent: "Copilot CLI", id: "copilot"},
		{name: "droid", agent: "Factory AI Droid", id: "droid"},
		{name: "unknown", agent: "Mystery Agent", id: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := whyAgentDisplay(tt.agent)
			want := agentDisplayMap[tt.id]
			if got.Color != want.Color || got.Label != want.Label {
				t.Fatalf("whyAgentDisplay(%q) = %#v, want %#v", tt.agent, got, want)
			}
		})
	}
}
