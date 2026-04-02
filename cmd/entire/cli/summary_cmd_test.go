package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/summarytui"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewRootCmd_RegistersSummaryCommand(t *testing.T) {
	t.Parallel()

	root := NewRootCmd()

	var found bool
	for _, cmd := range root.Commands() {
		if cmd.Name() == "summary" {
			found = true
			break
		}
	}

	require.True(t, found, "expected 'summary' command to be registered")
}

func TestSummaryCmd_HasExpectedMetadata(t *testing.T) {
	t.Parallel()

	cmd := newSummaryCmd()
	require.Equal(t, "summary", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotNil(t, cmd.RunE)
}

//nolint:musttag // nested external structs are part of the intended JSON payload
func TestRenderSummaryJSON_EncodesSessionSummaryAndFacets(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	rows := []insightsdb.SessionRow{sampleSummarySessionRow()}

	require.NoError(t, renderSummaryJSON(&buf, rows))

	var decoded []summarySessionView
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Len(t, decoded, 1)
	require.Equal(t, "chk-summary", decoded[0].CheckpointID)
	require.Equal(t, "Fix flaky tests", decoded[0].Summary.Intent)
	require.Len(t, decoded[0].Facets.MissingContext, 1)
	require.Equal(t, "Run canary after prompt wording changes", decoded[0].Facets.MissingContext[0].Item)
}

func TestRenderSummaryText_ShowsSummaryAndFacetSections(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderSummaryText(&buf, []insightsdb.SessionRow{sampleSummarySessionRow()})

	out := buf.String()
	require.Contains(t, out, "Session Summary")
	require.Contains(t, out, "Fix flaky tests")
	require.Contains(t, out, "Repeated Instructions")
	require.Contains(t, out, "Run tests before committing")
	require.Contains(t, out, "Review-Derived Rules")
	require.Contains(t, out, "Keep helper functions package-private unless reuse is proven")
}

func TestRenderSummaryText_ShowsAuthorAndAllFacetSections(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderSummaryText(&buf, []insightsdb.SessionRow{sampleSummarySessionRow()})

	out := buf.String()
	require.Contains(t, out, "Author: alishakawaguchi")
	require.Contains(t, out, "Author Name: Alisha Kawaguchi")
	require.Contains(t, out, "Author Email: alisha@example.com")
	require.Contains(t, out, "Failure Loops")
	require.Contains(t, out, "Skill Signals")
	require.Contains(t, out, "Repo Gotchas")
	require.Contains(t, out, "Workflow Gaps")
}

func TestRunSummary_AccessibleDoesNotStartTUI(t *testing.T) {
	tmpDir := t.TempDir()
	setupSummaryTestRepo(t, tmpDir)
	insertSummaryTestSession(t, tmpDir, sampleSummarySessionRow())

	t.Setenv("ACCESSIBLE", "1")

	originalRun := runSummaryTUI
	t.Cleanup(func() { runSummaryTUI = originalRun })

	var called bool
	runSummaryTUI = func(_ context.Context, _ []insightsdb.SessionRow, _ string, _ summarytui.GenerateFunc) error {
		called = true
		return nil
	}

	var buf bytes.Buffer
	err := runSummary(context.Background(), &buf, summaryOptions{Last: 10})
	require.NoError(t, err)
	require.False(t, called, "accessible mode should not launch the TUI")
	require.Contains(t, buf.String(), "Session Summary")
}

func TestRunSummary_PassesCurrentBranchToTUI(t *testing.T) {
	tmpDir := t.TempDir()
	setupSummaryTestRepo(t, tmpDir)
	testutil.GitCheckoutNewBranch(t, tmpDir, "feature/summary-browser")
	insertSummaryTestSession(t, tmpDir, sampleSummarySessionRow())

	originalRun := runSummaryTUI
	t.Cleanup(func() { runSummaryTUI = originalRun })

	var (
		called        bool
		currentBranch string
	)
	runSummaryTUI = func(_ context.Context, _ []insightsdb.SessionRow, branch string, _ summarytui.GenerateFunc) error {
		called = true
		currentBranch = branch
		return nil
	}

	var buf bytes.Buffer
	err := runSummary(context.Background(), &buf, summaryOptions{Last: 10})
	require.NoError(t, err)
	require.True(t, called, "non-accessible mode should launch the TUI")
	require.Equal(t, "feature/summary-browser", currentBranch)
}

func TestLoadSummarySessions_FilterByBranch(t *testing.T) {
	tmpDir := t.TempDir()
	setupSummaryTestRepo(t, tmpDir)

	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	insertSummaryTestSession(t, tmpDir, insightsdb.SessionRow{
		CheckpointID: "chk-main",
		SessionID:    "sess-main",
		SessionIndex: 0,
		Agent:        "Claude Code",
		Branch:       "main",
		CreatedAt:    now,
		HasSummary:   true,
		Intent:       "Main branch task",
	})
	insertSummaryTestSession(t, tmpDir, insightsdb.SessionRow{
		CheckpointID: "chk-feature",
		SessionID:    "sess-feature",
		SessionIndex: 0,
		Agent:        "Claude Code",
		Branch:       "feature/summary-browser",
		CreatedAt:    now.Add(-time.Hour),
		HasSummary:   true,
		Intent:       "Feature branch task",
	})

	rows, err := loadSummarySessions(context.Background(), summaryOptions{
		Last:   10,
		Branch: "main",
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "sess-main", rows[0].SessionID)
}

func TestLoadSummarySessions_CapsToMostRecent200(t *testing.T) {
	tmpDir := t.TempDir()
	setupSummaryTestRepo(t, tmpDir)

	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	for i := range 205 {
		insertSummaryTestSession(t, tmpDir, insightsdb.SessionRow{
			CheckpointID: fmt.Sprintf("chk-%03d", i),
			SessionID:    fmt.Sprintf("sess-%03d", i),
			SessionIndex: 0,
			Agent:        "Claude Code",
			Branch:       "feature/summary-browser",
			CreatedAt:    now.Add(-time.Duration(i) * time.Minute),
			HasSummary:   true,
			Intent:       fmt.Sprintf("task-%03d", i),
		})
	}

	rows, err := loadSummarySessions(context.Background(), summaryOptions{
		Last: 500,
	})
	require.NoError(t, err)
	require.Len(t, rows, 200)
	require.Equal(t, "sess-000", rows[0].SessionID)
	require.Equal(t, "sess-199", rows[len(rows)-1].SessionID)
}

func TestLoadSummarySessions_PopulatedCacheSkipsRefreshAndUsesVisibleBackfillLimit(t *testing.T) {
	tmpDir := t.TempDir()
	setupSummaryTestRepo(t, tmpDir)
	insertSummaryTestSession(t, tmpDir, sampleSummarySessionRow())

	originalRefresh := summaryRefreshCacheIfStale
	originalBackfillSummaries := summaryBackfillSummaries
	originalBackfillFacets := summaryBackfillFacets
	t.Cleanup(func() {
		summaryRefreshCacheIfStale = originalRefresh
		summaryBackfillSummaries = originalBackfillSummaries
		summaryBackfillFacets = originalBackfillFacets
	})

	refreshCalled := false
	summaryLimit := 0
	facetLimit := 0

	summaryRefreshCacheIfStale = func(context.Context, *insightsdb.InsightsDB) error {
		refreshCalled = true
		return nil
	}
	summaryBackfillSummaries = func(_ context.Context, _ io.Writer, _ *insightsdb.InsightsDB, lastN int) {
		summaryLimit = lastN
	}
	summaryBackfillFacets = func(_ context.Context, _ io.Writer, _ *insightsdb.InsightsDB, lastN int) {
		facetLimit = lastN
	}

	rows, err := loadSummarySessions(context.Background(), summaryOptions{Last: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, refreshCalled)
	require.Equal(t, 10, summaryLimit)
	require.Equal(t, 10, facetLimit)
}

func sampleSummarySessionRow() insightsdb.SessionRow {
	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	return insightsdb.SessionRow{
		CheckpointID: "chk-summary",
		SessionID:    "sess-summary",
		SessionIndex: 0,
		Agent:        "Claude Code",
		Model:        "sonnet",
		Branch:       "feature/summary-browser",
		OwnerName:    "Alisha Kawaguchi",
		OwnerID:      "alishakawaguchi",
		OwnerEmail:   "alisha@example.com",
		CreatedAt:    now,
		TotalTokens:  3210,
		TurnCount:    7,
		HasSummary:   true,
		HasFacets:    true,
		Intent:       "Fix flaky tests",
		Outcome:      "Stabilized the failing integration test",
		Friction: []string{
			"Fixture setup was duplicated across tests",
		},
		Learnings: []insightsdb.LearningRow{
			{Scope: "repo", Finding: "Canary tests must run after prompt wording changes"},
			{Scope: "workflow", Finding: "Write the regression test before adjusting helper code"},
			{Scope: "code", Path: "cmd/entire/cli/summary_cmd.go", Finding: "Keep loader and rendering separate"},
		},
		Facets: facets.SessionFacets{
			RepeatedUserInstructions: []facets.RepeatedInstruction{
				{Instruction: "Run tests before committing", Evidence: []string{"Please run the focused tests first"}},
			},
			MissingContext: []facets.MissingContextSignal{
				{Item: "Run canary after prompt wording changes", Evidence: []string{"Always rerun the canary after changing prompts"}},
			},
			ReviewDerivedRules: []facets.ReviewDerivedRule{
				{
					Rule:        "Keep helper functions package-private unless reuse is proven",
					SourceKind:  "pr_comment",
					Strength:    "strong",
					WhyReusable: "The repo prefers a narrow exported API surface by default.",
				},
			},
			FailureLoops: []facets.FailureLoop{
				{Description: "Repeated test reruns without changing the failing setup", Count: 2},
			},
			SkillSignals: []facets.SkillSignal{
				{SkillName: "test-driven-development"},
			},
			RepoGotchas: []string{
				"Always use repo root for git-relative paths",
			},
			WorkflowGaps: []string{
				"Run focused tests before broad verification",
			},
		},
	}
}

func setupSummaryTestRepo(t *testing.T, tmpDir string) {
	t.Helper()

	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	entireDir := filepath.Join(tmpDir, paths.EntireDir)
	require.NoError(t, os.MkdirAll(entireDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(entireDir, "settings.json"), []byte(`{
  "enabled": true,
  "strategy_options": {
    "summarize": {
      "enabled": true
    }
  }
}`), 0o644))
}

func insertSummaryTestSession(t *testing.T, tmpDir string, row insightsdb.SessionRow) {
	t.Helper()

	db, err := insightsdb.Open(filepath.Join(tmpDir, paths.EntireDir, "insights.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.InsertSession(context.Background(), row))
}
