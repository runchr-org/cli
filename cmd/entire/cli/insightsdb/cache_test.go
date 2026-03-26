package insightsdb_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
)

func openTestDB(t *testing.T) *insightsdb.InsightsDB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "insights.db")
	db, err := insightsdb.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestGetBranchTip_EmptyWhenNotSet(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	tip, err := db.GetBranchTip(ctx)
	require.NoError(t, err)
	assert.Empty(t, tip)
}

func TestSetAndGetBranchTip(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	err := db.SetBranchTip(ctx, "abc123def456")
	require.NoError(t, err)

	tip, err := db.GetBranchTip(ctx)
	require.NoError(t, err)
	assert.Equal(t, "abc123def456", tip)
}

func TestSetBranchTip_Overwrites(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.SetBranchTip(ctx, "first"))
	require.NoError(t, db.SetBranchTip(ctx, "second"))

	tip, err := db.GetBranchTip(ctx)
	require.NoError(t, err)
	assert.Equal(t, "second", tip)
}

func TestHasCheckpoint_FalseWhenAbsent(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	has, err := db.HasCheckpoint(ctx, "nonexistent")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasCheckpoint_TrueAfterInsert(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	row := minimalSessionRow("chk-001", "sess-001", 0)
	require.NoError(t, db.InsertSession(ctx, row))

	has, err := db.HasCheckpoint(ctx, "chk-001")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestInsertSession_BasicFields(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	row := insightsdb.SessionRow{
		CheckpointID: "chk-basic",
		SessionID:    "sess-basic",
		SessionIndex: 0,
		Agent:        "claude-code",
		Model:        "claude-3-5-sonnet",
		Branch:       "main",
		OwnerName:    "Test User",
		OwnerEmail:   "test@example.com",
		CreatedAt:    time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC),
		InputTokens:  1000,
		CacheTokens:  200,
		OutputTokens: 300,
		TotalTokens:  1500,
		APICallCount: 5,
		DurationMs:   30000,
		TurnCount:    3,
		Intent:       "fix bug",
		Outcome:      "success",
		AgentPct:     0.85,
	}

	err := db.InsertSession(ctx, row)
	require.NoError(t, err)

	sessions, err := db.QueryLastNSessions(ctx, 10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	got := sessions[0]
	assert.Equal(t, "chk-basic", got.CheckpointID)
	assert.Equal(t, "sess-basic", got.SessionID)
	assert.Equal(t, 0, got.SessionIndex)
	assert.Equal(t, "claude-code", got.Agent)
	assert.Equal(t, "claude-3-5-sonnet", got.Model)
	assert.Equal(t, "main", got.Branch)
	assert.Equal(t, "Test User", got.OwnerName)
	assert.Equal(t, "test@example.com", got.OwnerEmail)
	assert.Equal(t, 1000, got.InputTokens)
	assert.Equal(t, 200, got.CacheTokens)
	assert.Equal(t, 300, got.OutputTokens)
	assert.Equal(t, 1500, got.TotalTokens)
	assert.Equal(t, 5, got.APICallCount)
	assert.Equal(t, int64(30000), got.DurationMs)
	assert.Equal(t, 3, got.TurnCount)
	assert.Equal(t, "fix bug", got.Intent)
	assert.Equal(t, "success", got.Outcome)
	assert.InDelta(t, 0.85, got.AgentPct, 0.001)
}

func TestInsertSession_WithDenormalizedFields(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	row := insightsdb.SessionRow{
		CheckpointID: "chk-denorm",
		SessionID:    "sess-denorm",
		SessionIndex: 0,
		CreatedAt:    time.Now(),
		FilesTouched: []string{"main.go", "util.go", "README.md"},
		Friction:     []string{"had to retry", "tool failed twice"},
		Learnings: []insightsdb.LearningRow{
			{Scope: "repo", Finding: "uses conventional commits"},
			{Scope: "code", Finding: "helpers in util.go", Path: "util.go"},
		},
	}

	err := db.InsertSession(ctx, row)
	require.NoError(t, err)

	sessions, err := db.QueryLastNSessions(ctx, 10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	got := sessions[0]
	assert.ElementsMatch(t, []string{"main.go", "util.go", "README.md"}, got.FilesTouched)
	assert.ElementsMatch(t, []string{"had to retry", "tool failed twice"}, got.Friction)
	require.Len(t, got.Learnings, 2)
}

func TestInsertSession_WithStructuredFacets(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	row := insightsdb.SessionRow{
		CheckpointID: "chk-facets",
		SessionID:    "sess-facets",
		SessionIndex: 0,
		CreatedAt:    time.Now(),
		Facets: facets.SessionFacets{
			RepeatedUserInstructions: []facets.RepeatedInstruction{
				{Instruction: "Run golangci-lint before committing", Evidence: []string{"User repeated lint guidance"}},
			},
			MissingContext: []facets.MissingContextSignal{
				{Item: "Repo has nolint formatting gotcha", Evidence: []string{"Agent missed repo rule"}},
			},
			FailureLoops: []facets.FailureLoop{
				{Description: "Lint issue reappeared after fmt", Count: 2, Evidence: []string{"Returned after formatting"}},
			},
			SkillSignals: []facets.SkillSignal{
				{
					SkillName:          "project:go-linting",
					SkillPath:          ".codex/skills/go-linting/SKILL.md",
					Friction:           []string{"Skill missed gofmt/nolint interaction"},
					MissingInstruction: "Warn about trailing nolint comments on signatures",
				},
			},
		},
	}

	err := db.InsertSession(ctx, row)
	require.NoError(t, err)

	sessions, err := db.QueryLastNSessions(ctx, 10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	got := sessions[0]
	assert.True(t, got.HasFacets)
	require.Len(t, got.Facets.RepeatedUserInstructions, 1)
	assert.Equal(t, "Run golangci-lint before committing", got.Facets.RepeatedUserInstructions[0].Instruction)
	require.Len(t, got.Facets.SkillSignals, 1)
	assert.Equal(t, "project:go-linting", got.Facets.SkillSignals[0].SkillName)
}

func TestInsertSession_ScoreFields(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	row := insightsdb.SessionRow{
		CheckpointID:   "chk-scores",
		SessionID:      "sess-scores",
		SessionIndex:   0,
		CreatedAt:      time.Now(),
		OverallScore:   0.75,
		ScoreTokenEff:  0.80,
		ScoreFirstPass: 0.70,
		ScoreFriction:  0.65,
		ScoreFocus:     0.90,
	}

	err := db.InsertSession(ctx, row)
	require.NoError(t, err)

	sessions, err := db.QueryLastNSessions(ctx, 10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	got := sessions[0]
	assert.InDelta(t, 0.75, got.OverallScore, 0.001)
	assert.InDelta(t, 0.80, got.ScoreTokenEff, 0.001)
	assert.InDelta(t, 0.70, got.ScoreFirstPass, 0.001)
	assert.InDelta(t, 0.65, got.ScoreFriction, 0.001)
	assert.InDelta(t, 0.90, got.ScoreFocus, 0.001)
}

func TestInsertSession_MultipleSessionsSameCheckpoint(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	for i := range 3 {
		row := minimalSessionRow("chk-multi", "sess-multi-"+string(rune('A'+i)), i)
		require.NoError(t, db.InsertSession(ctx, row))
	}

	sessions, err := db.QueryLastNSessions(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, sessions, 3)
}

// minimalSessionRow creates a valid SessionRow with required fields only.
func minimalSessionRow(checkpointID, sessionID string, index int) insightsdb.SessionRow {
	return insightsdb.SessionRow{
		CheckpointID: checkpointID,
		SessionID:    sessionID,
		SessionIndex: index,
		CreatedAt:    time.Now(),
	}
}
