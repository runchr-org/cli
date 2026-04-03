package summarytui

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/stretchr/testify/require"
)

func TestRenderDetailContent_ShowsSummaryAndFacets(t *testing.T) {
	t.Parallel()

	row := sampleRowsForTest()[0]
	s := newStyles()
	content := renderDetailContent(s, row, 60)

	require.Contains(t, content, "Fix flaky tests")
	require.Contains(t, content, "Stabilized the failing integration test")
	require.Contains(t, content, "Fixture setup was duplicated across tests")
	require.Contains(t, content, "Always use repo root for git-relative paths")
	require.Contains(t, content, "Run tests before committing")
}

func TestRenderDetailContent_EmptyState(t *testing.T) {
	t.Parallel()

	row := insightsdb.SessionRow{
		SessionID:  "empty",
		HasSummary: false,
		HasFacets:  false,
	}
	s := newStyles()
	content := renderDetailContent(s, row, 60)

	require.Contains(t, content, "No summary or insights cached")
}

func TestRenderDetailContent_SummaryOnly(t *testing.T) {
	t.Parallel()

	row := insightsdb.SessionRow{
		SessionID:  "summary-only",
		HasSummary: true,
		HasFacets:  false,
		Intent:     "Test intent",
		Outcome:    "Test outcome",
	}
	s := newStyles()
	content := renderDetailContent(s, row, 60)

	require.Contains(t, content, "Test intent")
	require.Contains(t, content, "Test outcome")
	require.NotContains(t, content, "INSIGHTS")
}

func TestRenderDetailContent_OmitsEmptyFrictionAndLearnings(t *testing.T) {
	t.Parallel()

	row := insightsdb.SessionRow{
		SessionID:  "no-friction",
		HasSummary: true,
		HasFacets:  true,
		Intent:     "Some intent",
		Friction:   nil,
		Learnings:  nil,
		Facets: facets.SessionFacets{
			RepoGotchas: []string{"a gotcha"},
		},
	}
	s := newStyles()
	content := renderDetailContent(s, row, 60)

	require.Contains(t, content, "SUMMARY")
	require.NotContains(t, content, "FRICTION")
	require.NotContains(t, content, "LEARNINGS")
	require.Contains(t, content, "INSIGHTS")
	require.Contains(t, content, "a gotcha")
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

func TestRenderInsightsBox_MergesAllFacetTypes(t *testing.T) {
	t.Parallel()

	row := sampleRowsForTest()[0]
	s := newStyles()
	content := renderInsightsBox(s, row, 60)

	require.Contains(t, content, "Repo Gotcha:")
	require.Contains(t, content, "Workflow Gap:")
	require.Contains(t, content, "Failure Loop:")
	require.Contains(t, content, "Missing Context:")
	require.Contains(t, content, "Repeated:")
	require.Contains(t, content, "Skill:")
	require.Contains(t, content, "Rule:")
}

func TestRenderInsightsBox_ReturnsEmptyForNoFacets(t *testing.T) {
	t.Parallel()

	row := insightsdb.SessionRow{
		HasFacets: true,
		Facets:    facets.SessionFacets{},
	}
	s := newStyles()
	content := renderInsightsBox(s, row, 60)

	require.Empty(t, content)
}

func TestFormatTokensForDetail(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0", formatTokensForDetail(0))
	require.Equal(t, "500", formatTokensForDetail(500))
	require.Equal(t, "3.2k", formatTokensForDetail(3200))
	require.Equal(t, "1.5M", formatTokensForDetail(1500000))
}

func TestRenderSummaryBox_WithScoresAndStats(t *testing.T) {
	t.Parallel()

	row := insightsdb.SessionRow{
		HasSummary:     true,
		Intent:         "Fix flaky tests",
		Outcome:        "Stabilized 3 tests",
		OverallScore:   7.2,
		ScoreTokenEff:  8.1,
		ScoreFirstPass: 6.0,
		ScoreFriction:  7.5,
		ScoreFocus:     7.8,
		InputTokens:    45200,
		CacheTokens:    12100,
		OutputTokens:   8300,
		APICallCount:   23,
		DurationMs:     272000,
		AgentPct:       82.0,
	}
	s := newStyles()
	content := renderSummaryBox(s, row, 80)

	require.Contains(t, content, "SUMMARY")
	require.Contains(t, content, "Fix flaky tests")
	require.Contains(t, content, "Stabilized 3 tests")
	require.Contains(t, content, "7.2")
	require.Contains(t, content, "tok:8.1")
	require.Contains(t, content, "1st:6.0")
	require.Contains(t, content, "fric:7.5")
	require.Contains(t, content, "foc:7.8")
	require.Contains(t, content, "45.2k in")
	require.Contains(t, content, "12.1k cache")
	require.Contains(t, content, "8.3k out")
	require.Contains(t, content, "23 calls")
	require.Contains(t, content, "4m 32s")
	require.Contains(t, content, "82%")
}

func TestRenderSummaryBox_OmitsScoresWhenZero(t *testing.T) {
	t.Parallel()

	row := insightsdb.SessionRow{
		HasSummary: true,
		Intent:     "Quick fix",
	}
	s := newStyles()
	content := renderSummaryBox(s, row, 80)

	require.Contains(t, content, "Quick fix")
	require.NotContains(t, content, "Score:")
	require.NotContains(t, content, "Tokens:")
}

func TestRenderSummaryBox_EmptyWhenNoData(t *testing.T) {
	t.Parallel()

	row := insightsdb.SessionRow{}
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
