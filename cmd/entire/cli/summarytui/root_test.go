package summarytui

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/paginator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/stretchr/testify/require"
)

func TestRootView_RendersSessionTableColumns(t *testing.T) {
	t.Parallel()

	out := newRootModelForTest().View()

	require.Contains(t, out, "TIME")
	require.Contains(t, out, "AGENT")
	require.Contains(t, out, "BRANCH")
	require.Contains(t, out, "TOKENS")
	// Verify removed columns are not in the table header
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "TIME") && strings.Contains(line, "AGENT") {
			// This is the header line — verify removed columns are absent
			require.NotContains(t, line, "SUMMARY")
			require.NotContains(t, line, "FACETS")
			require.NotContains(t, line, "TURNS")
			break
		}
	}
	require.Contains(t, out, "Cursor") // sess-2 on main is visible with Repo filter
	require.Contains(t, out, "main")   // branch column
	require.Contains(t, out, "Repo")   // filter chip
	require.Contains(t, out, "Current Branch")
	require.Contains(t, out, "All")
	require.Contains(t, out, "Page 1/1")
	require.Contains(t, out, "enter detail")
}

func TestNewRootModel_DefaultsToRepoFilter(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser", nil)

	require.Equal(t, filterRepo, root.filter)
	require.Len(t, root.filteredRows, 1)
	require.Equal(t, "sess-2", root.filteredRows[0].SessionID)
}

func TestRootUpdate_CycleFilterRebuildsVisibleRows(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser", nil)
	// Default is filterRepo — shows only main/master rows
	require.Equal(t, filterRepo, root.filter)
	require.Len(t, root.filteredRows, 1)
	require.Equal(t, "sess-2", root.filteredRows[0].SessionID)

	// First cycle: Repo → Current Branch
	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	root = requireRootModel(t, next)
	require.Equal(t, filterCurrentBranch, root.filter)
	require.Len(t, root.filteredRows, 1)
	require.Equal(t, "sess-1", root.filteredRows[0].SessionID)

	// Second cycle: Current Branch → All
	next, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	root = requireRootModel(t, next)
	require.Equal(t, filterAllBranches, root.filter)
	require.Len(t, root.filteredRows, 2)
}

func TestRootUpdate_FilterChangeResetsPaginatorPage(t *testing.T) {
	t.Parallel()

	root := newRootModel(paginatedRowsForTest(), "feature/summary-browser", nil)
	root.pageSize = 2
	root.rebuildFilteredRows()
	root.paginator.Page = 1
	root.rebuildTable()

	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	root = requireRootModel(t, next)

	require.Equal(t, 0, root.paginator.Page)
	require.Equal(t, filterCurrentBranch, root.filter)
}

func TestRootUpdate_PageNavigationMovesBetweenFilteredPages(t *testing.T) {
	t.Parallel()

	root := newRootModel(paginatedRowsForTest(), "feature/summary-browser", nil)
	root.filter = filterAllBranches
	root.pageSize = 2
	root.paginator = paginator.New()
	root.rebuildFilteredRows()

	require.Equal(t, 3, root.paginator.TotalPages)
	require.Equal(t, 0, root.paginator.Page)
	require.Equal(t, "sess-1", root.selectedRow().SessionID)

	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyRight})
	root = requireRootModel(t, next)

	require.Equal(t, 1, root.paginator.Page)
	require.Equal(t, "sess-3", root.selectedRow().SessionID)
}

func TestRootUpdate_WindowSizeSetsTableDimensions(t *testing.T) {
	t.Parallel()

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser", nil)

	next, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	root = requireRootModel(t, next)

	require.Equal(t, 120, root.width)
	require.Equal(t, 32, root.height)
	require.Equal(t, 120, root.table.Width())
	require.Positive(t, root.table.Height())
}

func TestRootUpdate_WindowSizeExpandsPageSizeBeyondDefault(t *testing.T) {
	t.Parallel()

	root := newRootModel(manyCurrentBranchRowsForTest(20), "feature/summary-browser", nil)
	root.filter = filterCurrentBranch
	root.rebuildFilteredRows()
	require.Equal(t, defaultPageSize, root.pageSize)

	next, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	root = requireRootModel(t, next)

	require.Greater(t, root.pageSize, defaultPageSize)
	require.Equal(t, 1, root.paginator.TotalPages)
}

func TestRootUpdate_WindowSizePreservesSelectedSessionWhenPossible(t *testing.T) {
	t.Parallel()

	root := newRootModel(manyCurrentBranchRowsForTest(12), "feature/summary-browser", nil)
	root.filter = filterCurrentBranch
	root.pageSize = 3
	root.rebuildFilteredRows()
	root.nextPage()
	root.table.SetCursor(1)

	selected := root.selectedRow()
	require.NotNil(t, selected)
	require.Equal(t, "sess-5", selected.SessionID)

	next, _ := root.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	root = requireRootModel(t, next)

	selected = root.selectedRow()
	require.NotNil(t, selected)
	require.Equal(t, "sess-5", selected.SessionID)
}

func TestRootUpdate_EnterOpensSessionDetailPage(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()

	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)

	nextRoot := requireRootModel(t, next)
	next, _ = nextRoot.Update(cmd())
	updated := requireRootModel(t, next)

	require.NotNil(t, updated.detailPage)
	// Default filter is Repo (main), so first visible row is sess-2
	require.Equal(t, "sess-2", updated.detailPage.row.SessionID)
}

func TestRootUpdate_EscapeClosesSessionDetailPage(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()
	root.detailPage = newDetailModel(root.styles, sampleRowsForTest()[0], false)

	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, cmd)

	nextRoot := requireRootModel(t, next)
	next, _ = nextRoot.Update(cmd())
	updated := requireRootModel(t, next)

	require.Nil(t, updated.detailPage)
	require.Equal(t, 0, updated.table.Cursor())
}

func TestDetailView_RendersSummaryAndFacets(t *testing.T) {
	t.Parallel()

	detail := newDetailModel(newStyles(), sampleRowsForTest()[0], false)
	require.Contains(t, detail.view(), "SESSION DETAIL")

	view := detail.renderContent()

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

	view := newDetailModel(newStyles(), row, false).renderContent()

	require.Contains(t, view, "No summary cached")
	require.Contains(t, view, "No facets cached")
}

func TestRootUpdate_QKeyQuitsFromTable(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()

	_, cmd := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, cmd)
	// tea.Quit returns a special quit message
	msg := cmd()
	require.IsType(t, tea.QuitMsg{}, msg)
}

func TestRootUpdate_QKeyQuitsFromDetail(t *testing.T) {
	t.Parallel()

	root := newRootModelForTest()
	root.detailPage = newDetailModel(root.styles, sampleRowsForTest()[0], false)

	_, cmd := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	require.NotNil(t, cmd)
	msg := cmd()
	require.IsType(t, tea.QuitMsg{}, msg)
}

func TestRootUpdate_GenerateFromDetail(t *testing.T) {
	t.Parallel()

	generateCalled := false
	generateFn := func(_ context.Context, row insightsdb.SessionRow) (insightsdb.SessionRow, error) {
		generateCalled = true
		row.HasSummary = true
		row.Intent = "Generated intent"
		return row, nil
	}

	root := newRootModel(sampleRowsForTest(), "feature/summary-browser", generateFn)
	root.filter = filterAllBranches
	root.rebuildFilteredRows()
	root.detailPage = newDetailModel(root.styles, sampleRowsForTest()[0], true)

	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	root = requireRootModel(t, next)

	require.True(t, root.generating)
	require.NotNil(t, cmd)

	// Execute the command and feed result back
	msg := cmd()
	next, _ = root.Update(msg)
	root = requireRootModel(t, next)

	require.True(t, generateCalled)
	require.False(t, root.generating)
	require.NotNil(t, root.detailPage)
	require.Equal(t, "Generated intent", root.detailPage.row.Intent)
	require.Equal(t, "Generated", root.detailPage.status)
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
	m := newRootModel(sampleRowsForTest(), "feature/summary-browser", nil)
	m.width = 100
	m.height = 30
	m.table.SetWidth(100)
	m.table.SetHeight(12)
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
			CheckpointID: "chk-1",
			SessionID:    "sess-1",
			Agent:        "Claude Code",
			Model:        "sonnet",
			Branch:       "feature/summary-browser",
			OwnerName:    "Alisha Kawaguchi",
			OwnerID:      "alishakawaguchi",
			OwnerEmail:   "alisha@example.com",
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
				MissingContext: []facets.MissingContextSignal{
					{Item: "Run canary after prompt wording changes"},
				},
				FailureLoops: []facets.FailureLoop{
					{Description: "Repeated test reruns without changing setup", Count: 2},
				},
				SkillSignals: []facets.SkillSignal{
					{SkillName: "test-driven-development"},
				},
				ReviewDerivedRules: []facets.ReviewDerivedRule{
					{Rule: "Keep helper functions package-private unless reuse is proven"},
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

func manyFacetRowForTest() insightsdb.SessionRow {
	row := sampleRowsForTest()[0]
	row.Friction = make([]string, 0, 40)
	row.Learnings = make([]insightsdb.LearningRow, 0, 40)
	row.Facets.RepoGotchas = make([]string, 0, 40)
	row.Facets.WorkflowGaps = make([]string, 0, 40)

	for i := range 40 {
		row.Friction = append(row.Friction, "Friction item "+strconv.Itoa(i+1))
		row.Learnings = append(row.Learnings, insightsdb.LearningRow{
			Scope:   "repo",
			Finding: "Learning item " + strconv.Itoa(i+1),
		})
		row.Facets.RepoGotchas = append(row.Facets.RepoGotchas, "Repo gotcha "+strconv.Itoa(i+1))
		row.Facets.WorkflowGaps = append(row.Facets.WorkflowGaps, "Workflow gap "+strconv.Itoa(i+1))
	}

	return row
}
