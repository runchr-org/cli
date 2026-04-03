package summarytui

import (
	"context"
	"strconv"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/stretchr/testify/require"
)

func TestRootView_RendersSplitPaneLayout(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()
	out := root.View()

	// Filter bar present
	require.Contains(t, out, "TIME:")
	require.Contains(t, out, "BRANCH:")
	require.Contains(t, out, "sessions")

	// List header columns
	require.Contains(t, out, "TIME")
	require.Contains(t, out, "CKPT")
	require.Contains(t, out, "AGENT")

	// Status bar
	require.Contains(t, out, "j/k navigate")
	require.Contains(t, out, "1 time")
	require.Contains(t, out, "2 branch")
	require.Contains(t, out, "q quit")

	// List pane header should not contain detail-only columns
	listHeader := root.formatListRow("TIME", "CKPT", "AGENT", root.listWidth())
	require.NotContains(t, listHeader, "TOKENS")
	require.NotContains(t, listHeader, "BRANCH")
}

func TestRootView_ShowsDetailForSelectedRow(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()
	root.branchFilter = filterRepo
	root.rebuildFilteredRows()
	root.updateDetailViewport()

	out := root.View()

	// Detail pane should show the selected row's data
	// First row is sess-1 with intent "Fix flaky tests"
	require.Contains(t, out, "Fix flaky tests")
}

func TestNewRootModel_DefaultsToCurrentBranchFilter(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser", nil, nil)

	require.Equal(t, filterCurrentBranch, root.branchFilter)
	require.Len(t, root.filteredRows, 1)
	require.Equal(t, "sess-1", root.filteredRows[0].SessionID)
}

func TestRootUpdate_CycleBranchFilterRebuildsVisibleRows(t *testing.T) {
	t.Parallel()

	rows := sampleRowsForTest()
	root := newRootModel(rows, "feature/summary-browser", rows, nil)
	require.Equal(t, filterCurrentBranch, root.branchFilter)
	require.Len(t, root.filteredRows, 1)

	// First cycle: Current Branch → Repo (key "2")
	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	root = requireRootModel(t, next)
	require.Equal(t, filterRepo, root.branchFilter)
	require.Len(t, root.filteredRows, 2)

	// Second cycle: Repo → Current Branch (wraps around)
	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	root = requireRootModel(t, next)
	require.Equal(t, filterCurrentBranch, root.branchFilter)
	require.Len(t, root.filteredRows, 1)
	require.Equal(t, "sess-1", root.filteredRows[0].SessionID)
}

func TestRootUpdate_CycleTimeFilter(t *testing.T) {
	t.Parallel()

	now := time.Now()
	rows := []insightsdb.SessionRow{
		{SessionID: "recent", Agent: "Claude", Branch: "main", CreatedAt: now.Add(-1 * time.Hour)},
		{SessionID: "old-week", Agent: "Claude", Branch: "main", CreatedAt: now.Add(-3 * 24 * time.Hour)},
		{SessionID: "old-month", Agent: "Claude", Branch: "main", CreatedAt: now.Add(-15 * 24 * time.Hour)},
	}

	root := newRootModel(rows, "", nil, nil)
	require.Equal(t, timeFilterAll, root.timeFilter)
	require.Len(t, root.filteredRows, 3)

	// Cycle to 24h
	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	root = requireRootModel(t, next)
	require.Equal(t, timeFilter24h, root.timeFilter)
	require.Len(t, root.filteredRows, 1)
	require.Equal(t, "recent", root.filteredRows[0].SessionID)

	// Cycle to 7d
	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	root = requireRootModel(t, next)
	require.Equal(t, timeFilter7d, root.timeFilter)
	require.Len(t, root.filteredRows, 2)

	// Cycle to 30d
	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	root = requireRootModel(t, next)
	require.Equal(t, timeFilter30d, root.timeFilter)
	require.Len(t, root.filteredRows, 3)

	// Cycle to all
	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	root = requireRootModel(t, next)
	require.Equal(t, timeFilterAll, root.timeFilter)
	require.Len(t, root.filteredRows, 3)
}

func TestRootUpdate_FilterChangeResetsCursor(t *testing.T) {
	t.Parallel()

	root := newRootModel(paginatedRowsForTest(), "feature/summary-browser", paginatedRowsForTest(), nil)
	root.branchFilter = filterRepo
	root.rebuildFilteredRows()
	root.moveCursor(2) // move to third row

	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	root = requireRootModel(t, next)

	require.Equal(t, 0, root.cursor)
	require.Equal(t, 0, root.paginator.Page)
}

func TestRootUpdate_CursorMovement(t *testing.T) {
	t.Parallel()

	root := newRootModel(paginatedRowsForTest(), "feature/summary-browser", paginatedRowsForTest(), nil)
	root.branchFilter = filterRepo
	root.rebuildFilteredRows()
	require.Equal(t, 0, root.cursor)

	// Move down
	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	root = requireRootModel(t, next)
	require.Equal(t, 1, root.cursor)

	// Move down again
	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	root = requireRootModel(t, next)
	require.Equal(t, 2, root.cursor)

	// Move up
	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	root = requireRootModel(t, next)
	require.Equal(t, 1, root.cursor)
}

func TestRootUpdate_CursorDoesNotExceedBounds(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser", nil, nil)
	require.Len(t, root.filteredRows, 1) // repo filter, only main row

	// Move up from 0 — should stay at 0
	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	root = requireRootModel(t, next)
	require.Equal(t, 0, root.cursor)

	// Move down past end — should stay at last
	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	root = requireRootModel(t, next)
	require.Equal(t, 0, root.cursor) // only 1 row
}

func TestRootUpdate_CursorChangeUpdatesDetail(t *testing.T) {
	t.Parallel()

	rows := sampleRowsForTest()
	root := newRootModel(rows, "feature/summary-browser", rows, nil)
	root.branchFilter = filterRepo
	root.cursor = 0
	root.rebuildFilteredRows()
	root.resize(100, 30)
	// Force cursor to 0 after resize (resize may restore to previous selection)
	root.cursor = 0
	root.updateDetailViewport()

	// Initially shows first row's content (sess-1: Fix flaky tests)
	require.Equal(t, "sess-1", root.selectedRow().SessionID)
	content := renderDetailContent(root.styles, *root.selectedRow(), root.detailWidth())
	require.Contains(t, content, "Fix flaky tests")

	// Move to second row
	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	root = requireRootModel(t, next)

	require.Equal(t, "sess-2", root.selectedRow().SessionID)
	content = renderDetailContent(root.styles, *root.selectedRow(), root.detailWidth())
	require.Contains(t, content, "Update docs")
}

func TestRootUpdate_PageNavigation(t *testing.T) {
	t.Parallel()

	root := newRootModel(paginatedRowsForTest(), "feature/summary-browser", paginatedRowsForTest(), nil)
	root.branchFilter = filterRepo
	root.pageSize = 2
	root.rebuildFilteredRows()
	root.updateDetailViewport()

	require.Equal(t, 3, root.paginator.TotalPages)
	require.Equal(t, 0, root.paginator.Page)
	require.Equal(t, "sess-1", root.selectedRow().SessionID)

	// Next page
	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRight})
	root = requireRootModel(t, next)
	require.Equal(t, 1, root.paginator.Page)
	require.Equal(t, "sess-3", root.selectedRow().SessionID)
}

func TestRootUpdate_WindowSizeRecalculates(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser", nil, nil)

	next, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	root = requireRootModel(t, next)

	require.Equal(t, 120, root.width)
	require.Equal(t, 32, root.height)
	require.Equal(t, 36, root.listWidth())   // 30% of 120
	require.Equal(t, 83, root.detailWidth()) // 120 - 36 - 1
}

func TestRootUpdate_WindowSizePreservesSelection(t *testing.T) {
	t.Parallel()

	root := newRootModel(manyCurrentBranchRowsForTest(12), "feature/summary-browser", nil, nil)
	root.branchFilter = filterCurrentBranch
	root.pageSize = 3
	root.rebuildFilteredRows()
	root.nextPage()
	root.moveCursor(1)

	selected := root.selectedRow()
	require.NotNil(t, selected)
	require.Equal(t, "sess-5", selected.SessionID)

	next, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	root = requireRootModel(t, next)

	selected = root.selectedRow()
	require.NotNil(t, selected)
	require.Equal(t, "sess-5", selected.SessionID)
}

func TestRootUpdate_QKeyQuits(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()

	_, cmd := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, cmd)
	msg := cmd()
	require.IsType(t, tea.QuitMsg{}, msg)
}

func TestRootUpdate_Generate(t *testing.T) {
	t.Parallel()

	generateCalled := false
	generateFn := func(_ context.Context, row insightsdb.SessionRow) (insightsdb.SessionRow, error) {
		generateCalled = true
		row.HasSummary = true
		row.Intent = "Generated intent"
		return row, nil
	}

	rows := sampleRowsForTest()
	root := newRootModel(rows, "feature/summary-browser", rows, generateFn)
	root.branchFilter = filterRepo
	root.rebuildFilteredRows()
	root.updateDetailViewport()

	// Press g to generate
	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	root = requireRootModel(t, next)

	require.True(t, root.generating)
	require.Equal(t, "Generating...", root.genStatus)
	require.NotNil(t, cmd)

	// Execute the command and feed result back
	msg := cmd()
	next, _ = root.Update(msg)
	root = requireRootModel(t, next)

	require.True(t, generateCalled)
	require.False(t, root.generating)
	require.Equal(t, "Generated", root.genStatus)

	// Detail should reflect updated data
	content := renderDetailContent(root.styles, *root.selectedRow(), root.detailWidth())
	require.Contains(t, content, "Generated intent")
}

func TestRootView_FilterBarShowsActiveValues(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()
	out := root.renderFilterBar()

	// Default: timeFilterAll active, filterCurrentBranch active
	require.Contains(t, out, "all")     // time
	require.Contains(t, out, "current") // branch
}

func TestRootView_GenerateStatusShown(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()
	root.genStatus = "Generating..."
	out := root.renderStatusBar()

	require.Contains(t, out, "Generating...")
}

func TestRootView_EmptyFilteredRows(t *testing.T) {
	t.Parallel()

	root := newRootModel(nil, "", nil, nil)
	out := root.View()

	require.Contains(t, out, "No sessions to display")
}

// --- Helpers ---

func newRootModelForTest() rootModel {
	rows := sampleRowsForTest()
	m := newRootModel(rows, "feature/summary-browser", rows, nil)
	m.width = 100
	m.height = 30
	m.pageSize = 10
	m.rebuildFilteredRows()
	m.updateDetailViewport()
	return m
}

func requireRootModel(t *testing.T, model tea.Model) rootModel {
	t.Helper()

	root, ok := model.(rootModel)
	require.True(t, ok)
	return root
}

func sampleRowsForTest() []insightsdb.SessionRow {
	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	return []insightsdb.SessionRow{
		{
			CheckpointID:            "chk-1",
			SessionID:               "sess-1",
			Agent:                   "Claude Code",
			Model:                   "sonnet",
			Branch:                  "feature/summary-browser",
			OwnerName:               "Alisha Kawaguchi",
			OwnerID:                 "alishakawaguchi",
			OwnerEmail:              "alisha@example.com",
			CreatedAt:               now,
			TotalTokens:             3200,
			TurnCount:               7,
			InputTokens:             2000,
			CacheTokens:             500,
			OutputTokens:            700,
			APICallCount:            15,
			DurationMs:              180000,
			AgentPct:                75.0,
			OverallScore:            7.5,
			ScoreTokenEff:           8.0,
			ScoreFirstPass:          6.5,
			ScoreFriction:           7.0,
			ScoreFocus:              8.0,
			HasSummary:              true,
			HasFacets:               true,
			Intent:                  "Fix flaky tests",
			Outcome:                 "Stabilized the failing integration test",
			FilesTouched:            []string{"cmd/cli/strategy/common.go", "cmd/cli/lifecycle.go"},
			ToolCounts:              map[string]int{"Edit": 8, "Read": 5, "Bash": 3},
			ImplementationRationale: []string{"Used testutil helpers for isolation"},
			Tradeoffs:               []string{"Slower test setup vs guaranteed isolation"},
			CodebasePatterns:        []string{"Use repo root for git-relative paths"},
			Friction:                []string{"Fixture setup was duplicated across tests"},
			Learnings: []insightsdb.LearningRow{
				{Scope: "repo", Finding: "Always use repo root for git-relative paths"},
			},
			Facets: facets.SessionFacets{
				RepeatedUserInstructions: []facets.RepeatedInstruction{
					{Instruction: "Run tests before committing", Evidence: []string{"I said run tests"}},
				},
				MissingContext: []facets.MissingContextSignal{
					{Item: "Run canary after prompt wording changes", Evidence: []string{"canary broke after wording change"}},
				},
				FailureLoops: []facets.FailureLoop{
					{Description: "Repeated test reruns without changing setup", Count: 2, Evidence: []string{"same failure 3 times"}},
				},
				SkillSignals: []facets.SkillSignal{
					{SkillName: "test-driven-development", Friction: []string{"wrote impl first"}, MissingInstruction: "enforce test-first"},
				},
				ReviewDerivedRules: []facets.ReviewDerivedRule{
					{Rule: "Keep helper functions package-private unless reuse is proven", SourceKind: "code-review", Strength: "strong", Evidence: []string{"reviewer flagged export"}, WhyReusable: "applies to all helpers"},
				},
				RepoGotchas: []string{
					"Always use repo root for git-relative paths",
				},
				WorkflowGaps: []string{
					"Run focused tests before broad verification",
				},
			},
		},
		{
			CheckpointID: "chk-2",
			SessionID:    "sess-2",
			Agent:        "Cursor",
			Branch:       "main",
			CreatedAt:    now.Add(-time.Hour),
			TotalTokens:  1500,
			TurnCount:    4,
			HasSummary:   true,
			HasFacets:    false,
			Intent:       "Update docs",
			Outcome:      "Docs updated",
		},
	}
}

func paginatedRowsForTest() []insightsdb.SessionRow {
	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	return []insightsdb.SessionRow{
		{
			CheckpointID: "chk-1",
			SessionID:    "sess-1",
			Agent:        "Claude Code",
			Branch:       "feature/summary-browser",
			CreatedAt:    now,
			HasSummary:   true,
		},
		{
			CheckpointID: "chk-2",
			SessionID:    "sess-2",
			Agent:        "Cursor",
			Branch:       "main",
			CreatedAt:    now.Add(-time.Minute),
			HasSummary:   true,
		},
		{
			CheckpointID: "chk-3",
			SessionID:    "sess-3",
			Agent:        "OpenCode",
			Branch:       "feature/summary-browser",
			CreatedAt:    now.Add(-2 * time.Minute),
			HasSummary:   true,
		},
		{
			CheckpointID: "chk-4",
			SessionID:    "sess-4",
			Agent:        "Codex",
			Branch:       "main",
			CreatedAt:    now.Add(-3 * time.Minute),
			HasSummary:   true,
		},
		{
			CheckpointID: "chk-5",
			SessionID:    "sess-5",
			Agent:        "Claude Code",
			Branch:       "feature/summary-browser",
			CreatedAt:    now.Add(-4 * time.Minute),
			HasSummary:   true,
		},
		{
			CheckpointID: "chk-6",
			SessionID:    "sess-6",
			Agent:        "Cursor",
			Branch:       "main",
			CreatedAt:    now.Add(-5 * time.Minute),
			HasSummary:   true,
		},
	}
}

func manyCurrentBranchRowsForTest(count int) []insightsdb.SessionRow {
	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	rows := make([]insightsdb.SessionRow, 0, count)
	for i := range count {
		rows = append(rows, insightsdb.SessionRow{
			CheckpointID: "chk-" + strconv.Itoa(i+1),
			SessionID:    "sess-" + strconv.Itoa(i+1),
			Agent:        "Claude Code",
			Branch:       "feature/summary-browser",
			CreatedAt:    now.Add(-time.Duration(i) * time.Minute),
			HasSummary:   true,
		})
	}
	return rows
}
