package summarytui

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/stretchr/testify/require"
)

func TestRenderDetailContent_ShowsSummaryAndCode(t *testing.T) {
	t.Parallel()

	row := sampleRowsForTest()[0]
	s := newStyles()
	content := renderDetailContent(s, row, 80)

	// Summary box
	require.Contains(t, content, "Fix flaky tests")
	require.Contains(t, content, "Stabilized the failing integration test")
	// Code box
	require.Contains(t, content, "CODE")
	require.Contains(t, content, "cmd/cli/strategy/common.go")
	// Learnings box
	require.Contains(t, content, "LEARNINGS")
	require.Contains(t, content, "Always use repo root for git-relative paths")
	// Signals box
	require.Contains(t, content, "SIGNALS")
	require.Contains(t, content, "Fixture setup was duplicated across tests")
}

func TestRenderDetailContent_EmptyState(t *testing.T) {
	t.Parallel()

	row := SessionData{
		SessionID: "empty",
	}
	s := newStyles()
	content := renderDetailContent(s, row, 60)

	require.Contains(t, content, "No summary or insights cached")
}

func TestRenderDetailContent_SummaryOnly(t *testing.T) {
	t.Parallel()

	row := SessionData{
		SessionID: "summary-only",
		Summary: &checkpoint.Summary{
			Intent:  "Test intent",
			Outcome: "Test outcome",
		},
	}
	s := newStyles()
	content := renderDetailContent(s, row, 60)

	require.Contains(t, content, "Test intent")
	require.Contains(t, content, "Test outcome")
	require.NotContains(t, content, "CODE")
}

func TestRenderDetailContent_OmitsEmptyBoxes(t *testing.T) {
	t.Parallel()

	row := SessionData{
		SessionID: "no-friction",
		Summary: &checkpoint.Summary{
			Intent:    "Some intent",
			OpenItems: []string{"finish tests"},
		},
	}
	s := newStyles()
	content := renderDetailContent(s, row, 80)

	require.Contains(t, content, "SUMMARY")
	require.NotContains(t, content, "CODE")
	require.NotContains(t, content, "LEARNINGS")
	require.Contains(t, content, "SIGNALS")
	require.Contains(t, content, "finish tests")
}

func TestRenderMetadataHeader_ShowsSessionInfo(t *testing.T) {
	t.Parallel()

	row := sampleRowsForTest()[0]
	s := newStyles()
	header := renderMetadataHeader(s, row, 60)

	require.Contains(t, header, "SESSION")
	require.Contains(t, header, "TOKENS")
	require.Contains(t, header, "feature/summary-browser")
	require.Contains(t, header, "sonnet")
	require.Contains(t, header, "3.2k")
}

func TestFormatTokensForDetail(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0", formatTokensForDetail(0))
	require.Equal(t, "500", formatTokensForDetail(500))
	require.Equal(t, "3.2k", formatTokensForDetail(3200))
	require.Equal(t, "1.5M", formatTokensForDetail(1500000))
}

func TestRenderSummaryBox_WithStats(t *testing.T) {
	t.Parallel()

	row := SessionData{
		Summary: &checkpoint.Summary{
			Intent:  "Fix flaky tests",
			Outcome: "Stabilized 3 tests",
		},
		InputTokens:  45200,
		CacheTokens:  12100,
		OutputTokens: 8300,
		DurationMs:   272000,
	}
	s := newStyles()
	content := renderSummaryBox(s, row, 80)

	require.Contains(t, content, "SUMMARY")
	require.Contains(t, content, "Fix flaky tests")
	require.Contains(t, content, "Stabilized 3 tests")
	require.Contains(t, content, "45.2k in")
	require.Contains(t, content, "12.1k cache")
	require.Contains(t, content, "8.3k out")
	require.Contains(t, content, "4m 32s")
}

func TestRenderSummaryBox_IntentOnlyNoStats(t *testing.T) {
	t.Parallel()

	row := SessionData{
		Summary: &checkpoint.Summary{
			Intent: "Quick fix",
		},
	}
	s := newStyles()
	content := renderSummaryBox(s, row, 80)

	require.Contains(t, content, "Quick fix")
	require.NotContains(t, content, "Tokens:")
}

func TestRenderSummaryBox_EmptyWhenNoData(t *testing.T) {
	t.Parallel()

	row := SessionData{}
	s := newStyles()
	content := renderSummaryBox(s, row, 80)

	require.Empty(t, content)
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0s", formatDuration(0))
	require.Equal(t, "5s", formatDuration(5000))
	require.Equal(t, "59s", formatDuration(59000))
	require.Equal(t, "1m 0s", formatDuration(60000))
	require.Equal(t, "4m 32s", formatDuration(272000))
	require.Equal(t, "59m 59s", formatDuration(3599000))
	require.Equal(t, "1h 0m", formatDuration(3600000))
	require.Equal(t, "2h 15m", formatDuration(8100000))
}

func TestRenderCodeBox(t *testing.T) {
	t.Parallel()

	row := SessionData{
		FilesTouched: []string{"cmd/cli/foo.go", "cmd/cli/bar.go"},
	}
	s := newStyles()
	content := renderCodeBox(s, row, 80)

	require.Contains(t, content, "CODE")
	require.Contains(t, content, "FILES TOUCHED")
	require.Contains(t, content, "cmd/cli/foo.go")
	require.Contains(t, content, "cmd/cli/bar.go")
}

func TestRenderCodeBox_ReturnsEmptyWhenNoFiles(t *testing.T) {
	t.Parallel()

	row := SessionData{}
	s := newStyles()
	content := renderCodeBox(s, row, 80)

	require.Empty(t, content)
}

func TestRenderSignalsBox_ShowsFrictionAndOpenItems(t *testing.T) {
	t.Parallel()

	row := SessionData{
		Summary: &checkpoint.Summary{
			Friction:  []string{"Fixture setup duplicated"},
			OpenItems: []string{"Add pre-commit hook for tests"},
		},
	}
	s := newStyles()
	content := renderSignalsBox(s, row, 80)

	require.Contains(t, content, "SIGNALS")
	require.Contains(t, content, "FRICTION")
	require.Contains(t, content, "Fixture setup duplicated")
	require.Contains(t, content, "OPEN ITEMS")
	require.Contains(t, content, "Add pre-commit hook for tests")
}

func TestRenderSignalsBox_ReturnsEmptyForNoSignals(t *testing.T) {
	t.Parallel()

	row := SessionData{
		Summary: &checkpoint.Summary{},
	}
	s := newStyles()
	content := renderSignalsBox(s, row, 80)

	require.Empty(t, content)
}

func TestRenderSignalsBox_ReturnsEmptyWhenNoSummary(t *testing.T) {
	t.Parallel()

	row := SessionData{}
	s := newStyles()
	content := renderSignalsBox(s, row, 80)

	require.Empty(t, content)
}

func TestRenderLearningsBox_GroupsByScope(t *testing.T) {
	t.Parallel()

	row := SessionData{
		Summary: &checkpoint.Summary{
			Learnings: checkpoint.LearningsSummary{
				Repo:     []string{"Use repo root for paths"},
				Code:     []checkpoint.CodeLearning{{Finding: "Handler needs context", Path: "cmd/handler.go"}},
				Workflow: []string{"Run tests before committing"},
			},
		},
	}
	s := newStyles()
	content := renderLearningsBox(s, row, 80)

	require.Contains(t, content, "LEARNINGS")
	require.Contains(t, content, "Use repo root for paths")
	require.Contains(t, content, "(repo)")
	require.Contains(t, content, "Handler needs context")
	require.Contains(t, content, "(cmd/handler.go)")
	require.Contains(t, content, "Run tests before committing")
	require.Contains(t, content, "(workflow)")
}

func TestRenderLearningsBox_EmptyWhenNoLearnings(t *testing.T) {
	t.Parallel()

	row := SessionData{
		Summary: &checkpoint.Summary{},
	}
	s := newStyles()
	content := renderLearningsBox(s, row, 80)

	require.Empty(t, content)
}
