package insightsdb_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
)

func TestQueryLastNSessions_EmptyWhenNone(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	sessions, err := db.QueryLastNSessions(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestQueryLastNSessions_ReturnsOrderedByCreatedAtDesc(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		row := insightsdb.SessionRow{
			CheckpointID: "chk-order",
			SessionID:    "sess-order",
			SessionIndex: i,
			CreatedAt:    base.Add(time.Duration(i) * time.Hour),
		}
		require.NoError(t, db.InsertSession(ctx, row))
	}

	sessions, err := db.QueryLastNSessions(ctx, 5)
	require.NoError(t, err)
	require.Len(t, sessions, 5)

	// Newest first
	for i := 1; i < len(sessions); i++ {
		assert.True(t,
			sessions[i-1].CreatedAt.After(sessions[i].CreatedAt) ||
				sessions[i-1].CreatedAt.Equal(sessions[i].CreatedAt),
			"sessions should be ordered newest first",
		)
	}
}

func TestQueryLastNSessions_RespectsLimit(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	for i := range 10 {
		require.NoError(t, db.InsertSession(ctx, minimalSessionRow("chk-limit", "sess", i)))
	}

	sessions, err := db.QueryLastNSessions(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, sessions, 3)
}

func TestQueryByAgent_FiltersCorrectly(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	agents := []string{"claude-code", "claude-code", "gemini-cli"}
	for i, agent := range agents {
		row := minimalSessionRow("chk-agent-"+agent, "sess", i)
		row.Agent = agent
		require.NoError(t, db.InsertSession(ctx, row))
	}

	claudeSessions, err := db.QueryByAgent(ctx, "claude-code", 10)
	require.NoError(t, err)
	assert.Len(t, claudeSessions, 2)
	for _, s := range claudeSessions {
		assert.Equal(t, "claude-code", s.Agent)
	}

	geminiSessions, err := db.QueryByAgent(ctx, "gemini-cli", 10)
	require.NoError(t, err)
	assert.Len(t, geminiSessions, 1)
}

func TestQueryByAgent_RespectsLimit(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	for i := range 5 {
		row := minimalSessionRow("chk-al", "sess", i)
		row.Agent = "claude-code"
		require.NoError(t, db.InsertSession(ctx, row))
	}

	sessions, err := db.QueryByAgent(ctx, "claude-code", 2)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
}

func TestQueryByAgent_EmptyWhenAgentNotFound(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	sessions, err := db.QueryByAgent(ctx, "unknown-agent", 10)
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestQueryByCheckpointPrefix_MatchesPrefix(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	// Insert sessions with different checkpoint IDs
	for i, cpID := range []string{"a1b2c3d4e5f6", "a1b2c3ffffff", "eeeeeeeeeeee"} {
		row := minimalSessionRow(cpID, "sess", i)
		require.NoError(t, db.InsertSession(ctx, row))
	}

	// Exact match
	sessions, err := db.QueryByCheckpointPrefix(ctx, "eeeeeeeeeeee")
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "eeeeeeeeeeee", sessions[0].CheckpointID)

	// Prefix match
	sessions, err = db.QueryByCheckpointPrefix(ctx, "a1b2c3")
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// No match
	sessions, err = db.QueryByCheckpointPrefix(ctx, "ffffff")
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestQueryRecurringFriction_ReturnsThemesAboveMinCount(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	// Insert 3 sessions with DIFFERENT checkpoint IDs but same friction text
	for i := range 3 {
		cpID := fmt.Sprintf("chk-friction-%d", i)
		row := minimalSessionRow(cpID, "sess", 0)
		row.Friction = []string{"tool call failed", "unique friction " + string(rune('A'+i))}
		require.NoError(t, db.InsertSession(ctx, row))
	}

	themes, err := db.QueryRecurringFriction(ctx, 2)
	require.NoError(t, err)
	require.Len(t, themes, 1)
	assert.Equal(t, "tool call failed", themes[0].Text)
	assert.Equal(t, 3, themes[0].Count)
	assert.Len(t, themes[0].Sessions, 3)
}

func TestQueryRecurringFriction_ExcludesBelowMinCount(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	row := minimalSessionRow("chk-rare", "sess", 0)
	row.Friction = []string{"rare friction"}
	require.NoError(t, db.InsertSession(ctx, row))

	themes, err := db.QueryRecurringFriction(ctx, 2)
	require.NoError(t, err)
	assert.Empty(t, themes)
}

func TestQueryRecurringFriction_OrderedByCountDesc(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	// "common" appears 3 times, "less common" appears 2 times
	for i := range 3 {
		row := minimalSessionRow("chk-order-f", "sess", i)
		row.Friction = []string{"common friction"}
		if i < 2 {
			row.Friction = append(row.Friction, "less common friction")
		}
		require.NoError(t, db.InsertSession(ctx, row))
	}

	themes, err := db.QueryRecurringFriction(ctx, 2)
	require.NoError(t, err)
	require.Len(t, themes, 2)
	assert.Equal(t, "common friction", themes[0].Text)
	assert.Equal(t, 3, themes[0].Count)
	assert.Equal(t, "less common friction", themes[1].Text)
	assert.Equal(t, 2, themes[1].Count)
}

func TestQuerySessionsWithFriction_PatternMatch(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	row1 := minimalSessionRow("chk-pf1", "sess", 0)
	row1.Friction = []string{"tool call failed: timeout"}

	row2 := minimalSessionRow("chk-pf2", "sess", 0)
	row2.Friction = []string{"tool call failed: rate limit"}

	row3 := minimalSessionRow("chk-pf3", "sess", 0)
	row3.Friction = []string{"unrelated issue"}

	require.NoError(t, db.InsertSession(ctx, row1))
	require.NoError(t, db.InsertSession(ctx, row2))
	require.NoError(t, db.InsertSession(ctx, row3))

	ids, err := db.QuerySessionsWithFriction(ctx, "%tool call failed%")
	require.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, "chk-pf1")
	assert.Contains(t, ids, "chk-pf2")
}

func TestQuerySessionsWithFriction_EmptyWhenNoMatch(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	ids, err := db.QuerySessionsWithFriction(ctx, "%nonexistent%")
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestSessionCount_Zero(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	count, err := db.SessionCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestSessionCount_AfterInserts(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	for i := range 5 {
		require.NoError(t, db.InsertSession(ctx, minimalSessionRow("chk-count", "sess", i)))
	}

	count, err := db.SessionCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestQueryLastNSessions_LoadsStructuredFacets(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	row := minimalSessionRow("chk-fq", "sess", 0)
	row.Facets = facets.SessionFacets{
		RepeatedUserInstructions: []facets.RepeatedInstruction{
			{Instruction: "Run tests before committing", Evidence: []string{"User asked twice"}},
		},
		MissingContext: []facets.MissingContextSignal{
			{Item: "Repo requires canary after prompt changes", Evidence: []string{"Vogon parser mismatch"}},
		},
		ReviewDerivedRules: []facets.ReviewDerivedRule{
			{
				Rule:        "Prefer package-private helpers unless a shared API is required",
				Evidence:    []string{"Review comment asked to avoid exporting a test-only helper"},
				SourceKind:  "pr_comment",
				Strength:    "strong",
				WhyReusable: "This is a recurring org style preference rather than a one-off file fix.",
			},
		},
	}
	require.NoError(t, db.InsertSession(ctx, row))

	sessions, err := db.QueryLastNSessions(ctx, 1)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.True(t, sessions[0].HasFacets)
	require.Len(t, sessions[0].Facets.RepeatedUserInstructions, 1)
	assert.Equal(t, "Run tests before committing", sessions[0].Facets.RepeatedUserInstructions[0].Instruction)
	require.Len(t, sessions[0].Facets.MissingContext, 1)
	assert.Equal(t, "Repo requires canary after prompt changes", sessions[0].Facets.MissingContext[0].Item)
	require.Len(t, sessions[0].Facets.ReviewDerivedRules, 1)
	assert.Equal(t, "Prefer package-private helpers unless a shared API is required", sessions[0].Facets.ReviewDerivedRules[0].Rule)
	assert.Equal(t, "pr_comment", sessions[0].Facets.ReviewDerivedRules[0].SourceKind)
}
