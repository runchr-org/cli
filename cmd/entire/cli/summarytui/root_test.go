package summarytui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/stretchr/testify/require"
)

func TestRootView_RendersSessionTableColumns(t *testing.T) {
	t.Parallel()

	out := newRootModelForTest().View()

	require.Contains(t, out, "SUMMARY")
	require.Contains(t, out, "FACETS")
	require.Contains(t, out, "Claude Code")
	require.Contains(t, out, "feature/summary-browser")
	require.Contains(t, out, "Current Branch")
	require.Contains(t, out, "Page 1/1")
}

func TestNewRootModel_DefaultsToCurrentBranchFilter(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser")

	require.Equal(t, filterCurrentBranch, root.filter)
	require.Len(t, root.filteredRows, 1)
	require.Equal(t, "sess-1", root.filteredRows[0].SessionID)
}

func TestRootUpdate_CycleFilterRebuildsVisibleRows(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser")

	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	root = next.(rootModel)
	require.Equal(t, filterMainBranch, root.filter)
	require.Len(t, root.filteredRows, 1)
	require.Equal(t, "sess-2", root.filteredRows[0].SessionID)

	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	root = next.(rootModel)
	require.Equal(t, filterAllBranches, root.filter)
	require.Len(t, root.filteredRows, 2)
}

func TestRootUpdate_FilterChangeResetsPaginatorPage(t *testing.T) {
	t.Parallel()

	root := newRootModel(paginatedRowsForTest(), "feature/summary-browser")
	root.pageSize = 2
	root.rebuildFilteredRows()
	root.paginator.Page = 2
	root.rebuildTable()

	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	root = next.(rootModel)

	require.Equal(t, 0, root.paginator.Page)
	require.Equal(t, filterMainBranch, root.filter)
}

func TestRootUpdate_PageNavigationMovesBetweenFilteredPages(t *testing.T) {
	t.Parallel()

	root := newRootModel(paginatedRowsForTest(), "feature/summary-browser")
	root.filter = filterAllBranches
	root.pageSize = 2
	root.paginator = paginator.New()
	root.rebuildFilteredRows()

	require.Equal(t, 3, root.paginator.TotalPages)
	require.Equal(t, 0, root.paginator.Page)
	require.Equal(t, "sess-1", root.selectedRow().SessionID)

	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRight})
	root = next.(rootModel)

	require.Equal(t, 1, root.paginator.Page)
	require.Equal(t, "sess-3", root.selectedRow().SessionID)
}

func TestRootUpdate_WindowSizeSetsTableDimensions(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser")

	next, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	root = next.(rootModel)

	require.Equal(t, 120, root.width)
	require.Equal(t, 32, root.height)
	require.Equal(t, 120, root.table.Width())
	require.Greater(t, root.table.Height(), 0)
}

func TestRootUpdate_WindowSizeExpandsPageSizeBeyondDefault(t *testing.T) {
	t.Parallel()

	root := newRootModel(manyCurrentBranchRowsForTest(20), "feature/summary-browser")
	require.Equal(t, defaultPageSize, root.pageSize)

	next, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	root = next.(rootModel)

	require.Greater(t, root.pageSize, defaultPageSize)
	require.Equal(t, 1, root.paginator.TotalPages)
}

func TestRootUpdate_WindowSizePreservesSelectedSessionWhenPossible(t *testing.T) {
	t.Parallel()

	root := newRootModel(manyCurrentBranchRowsForTest(12), "feature/summary-browser")
	root.pageSize = 3
	root.rebuildFilteredRows()
	root.nextPage()
	root.table.SetCursor(1)

	selected := root.selectedRow()
	require.NotNil(t, selected)
	require.Equal(t, "sess-5", selected.SessionID)

	next, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	root = next.(rootModel)

	selected = root.selectedRow()
	require.NotNil(t, selected)
	require.Equal(t, "sess-5", selected.SessionID)
}

func TestRootUpdate_EnterOpensSessionDetailPage(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()

	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)

	next, _ = next.(rootModel).Update(cmd())
	updated := next.(rootModel)

	require.NotNil(t, updated.detailPage)
	require.Equal(t, "sess-1", updated.detailPage.row.SessionID)
}

func TestRootUpdate_EscapeClosesSessionDetailPage(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()
	root.detailPage = newDetailModel(root.styles, sampleRowsForTest()[0])

	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, cmd)

	next, _ = next.(rootModel).Update(cmd())
	updated := next.(rootModel)

	require.Nil(t, updated.detailPage)
	require.Equal(t, 0, updated.table.Cursor())
}

func TestDetailView_RendersSummaryAndFacets(t *testing.T) {
	t.Parallel()

	view := newDetailModel(newStyles(), sampleRowsForTest()[0]).view()

	require.Contains(t, view, "SESSION DETAIL")
	require.Contains(t, view, "Fix flaky tests")
	require.Contains(t, view, "Repeated Instructions")
	require.Contains(t, view, "Run tests before committing")
	require.Contains(t, view, "Review-Derived Rules")
}

func TestDetailView_EmptyStatesAreExplicit(t *testing.T) {
	t.Parallel()

	row := sampleRowsForTest()[0]
	row.HasSummary = false
	row.HasFacets = false
	row.Intent = ""
	row.Outcome = ""
	row.Friction = nil
	row.Learnings = nil
	row.Facets = facets.SessionFacets{}

	view := newDetailModel(newStyles(), row).view()

	require.Contains(t, view, "No summary cached")
	require.Contains(t, view, "No facets cached")
}

func TestRootView_RespectsWidth(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()
	root.width = 90
	out := root.View()
	for _, line := range strings.Split(out, "\n") {
		require.LessOrEqual(t, len(line), 120)
	}
}

func newRootModelForTest() rootModel {
	m := newRootModel(sampleRowsForTest(), "feature/summary-browser")
	m.width = 100
	m.height = 30
	m.table.SetWidth(100)
	m.table.SetHeight(12)
	return m
}

func sampleRowsForTest() []insightsdb.SessionRow {
	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	return []insightsdb.SessionRow{
		{
			CheckpointID: "chk-1",
			SessionID:    "sess-1",
			Agent:        "Claude Code",
			Branch:       "feature/summary-browser",
			CreatedAt:    now,
			TotalTokens:  3200,
			TurnCount:    7,
			HasSummary:   true,
			HasFacets:    true,
			Intent:       "Fix flaky tests",
			Outcome:      "Stabilized the failing integration test",
			Friction:     []string{"Fixture setup was duplicated across tests"},
			Facets: facets.SessionFacets{
				RepeatedUserInstructions: []facets.RepeatedInstruction{
					{Instruction: "Run tests before committing"},
				},
				ReviewDerivedRules: []facets.ReviewDerivedRule{
					{Rule: "Keep helper functions package-private unless reuse is proven"},
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
