package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/summarytui"
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
	require.Equal(t, "summary [checkpoint-id]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotNil(t, cmd.RunE)
}

func TestSummaryCmd_AcceptsCheckpointArg(t *testing.T) {
	t.Parallel()

	cmd := newSummaryCmd()
	// Should accept 0 or 1 args
	require.NoError(t, cmd.Args(cmd, []string{}))
	require.NoError(t, cmd.Args(cmd, []string{"a1b2c3"}))
	require.Error(t, cmd.Args(cmd, []string{"a1b2c3", "extra"}))
}

func TestRenderSummaryJSON_EncodesSessionData(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	rows := []summarytui.SessionData{sampleSummarySessionData()}

	require.NoError(t, renderSummaryJSON(&buf, rows))

	var decoded []summarySessionView
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Len(t, decoded, 1)
	require.Equal(t, "chk-summary", decoded[0].CheckpointID)
	require.Equal(t, "Fix flaky tests", decoded[0].Summary.Intent)
}

func TestRenderSummaryText_ShowsSummarySection(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderSummaryText(&buf, []summarytui.SessionData{sampleSummarySessionData()})

	out := buf.String()
	require.Contains(t, out, "Session Summary")
	require.Contains(t, out, "Fix flaky tests")
	require.Contains(t, out, "Stabilized the failing integration test")
	require.Contains(t, out, "Friction")
	require.Contains(t, out, "Fixture setup was duplicated across tests")
	require.Contains(t, out, "Learnings")
	require.Contains(t, out, "[repo]")
	require.Contains(t, out, "Canary tests must run after prompt wording changes")
}

func TestRenderSummaryText_ShowsBranchAndOpenItems(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderSummaryText(&buf, []summarytui.SessionData{sampleSummarySessionData()})

	out := buf.String()
	require.Contains(t, out, "Branch: feature/summary-browser")
	require.Contains(t, out, "Open Items")
	require.Contains(t, out, "Run focused tests before broad verification")
}

func TestRunSummary_AccessibleDoesNotStartTUI(t *testing.T) {
	t.Setenv("ACCESSIBLE", "1")

	originalRun := runSummaryTUI
	t.Cleanup(func() { runSummaryTUI = originalRun })

	var called bool
	runSummaryTUI = func(_ context.Context, _ []summarytui.SessionData, _ string, _ []summarytui.SessionData, _ summarytui.GenerateFunc) error {
		called = true
		return nil
	}

	var buf bytes.Buffer
	rows := []summarytui.SessionData{sampleSummarySessionData()}
	renderSummaryText(&buf, rows)

	require.False(t, called, "accessible mode should not launch the TUI")
	require.Contains(t, buf.String(), "Session Summary")
}

func TestRenderSummaryText_EmptySessionsShowsMessage(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderSummaryText(&buf, nil)

	out := buf.String()
	require.Contains(t, out, "No sessions found.")
}

func TestRenderSummaryText_NoSummaryShowsMessage(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderSummaryText(&buf, []summarytui.SessionData{
		{
			CheckpointID: "chk-nosummary",
			SessionID:    "sess-nosummary",
			Agent:        "Claude Code",
			CreatedAt:    time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC),
		},
	})

	out := buf.String()
	require.Contains(t, out, "No summary cached")
}

func TestSessionDataToView_ConvertsProperly(t *testing.T) {
	t.Parallel()

	data := sampleSummarySessionData()
	view := sessionDataToView(data)

	require.Equal(t, "chk-summary", view.CheckpointID)
	require.Equal(t, "sess-summary", view.SessionID)
	require.Equal(t, "Claude Code", view.Agent)
	require.Equal(t, "sonnet", view.Model)
	require.Equal(t, 3210, view.Tokens)
	require.Equal(t, 7, view.Turns)
	require.True(t, view.HasSummary)
	require.Equal(t, "Fix flaky tests", view.Summary.Intent)
}

func TestNormalizedSummarySessionLimit(t *testing.T) {
	t.Parallel()

	require.Equal(t, defaultSummarySessionLimit, normalizedSummarySessionLimit(0))
	require.Equal(t, defaultSummarySessionLimit, normalizedSummarySessionLimit(-1))
	require.Equal(t, 5, normalizedSummarySessionLimit(5))
	require.Equal(t, maxSummaryRecentSessions, normalizedSummarySessionLimit(500))
}

func sampleSummarySessionData() summarytui.SessionData {
	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	return summarytui.SessionData{
		CheckpointID: "chk-summary",
		SessionID:    "sess-summary",
		SessionIndex: 0,
		Agent:        "Claude Code",
		Model:        "sonnet",
		Branch:       "feature/summary-browser",
		CreatedAt:    now,
		TotalTokens:  3210,
		TurnCount:    7,
		FilesTouched: []string{"cmd/cli/strategy/common.go"},
		Summary: &checkpoint.Summary{
			Intent:  "Fix flaky tests",
			Outcome: "Stabilized the failing integration test",
			Friction: []string{
				"Fixture setup was duplicated across tests",
			},
			Learnings: checkpoint.LearningsSummary{
				Repo:     []string{"Canary tests must run after prompt wording changes"},
				Workflow: []string{"Write the regression test before adjusting helper code"},
				Code: []checkpoint.CodeLearning{
					{Path: "cmd/entire/cli/summary_cmd.go", Finding: "Keep loader and rendering separate"},
				},
			},
			OpenItems: []string{
				"Run focused tests before broad verification",
			},
		},
	}
}
