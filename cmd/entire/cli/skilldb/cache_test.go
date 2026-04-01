package skilldb_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/skilldb"
	"github.com/entireio/cli/cmd/entire/cli/testutil"

	_ "modernc.org/sqlite"

	"github.com/go-git/go-git/v6"
)

func TestResolveSkillName_ExactMatch(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"e2e": {Name: "e2e", SourceAgent: "claude-code"},
		"dev": {Name: "dev", SourceAgent: "claude-code"},
	}
	got, ok := skilldb.ResolveSkillName("e2e", m)
	assert.True(t, ok)
	assert.Equal(t, "e2e", got.Name)
}

func TestResolveSkillName_SubSkillPrefix(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"e2e": {Name: "e2e", SourceAgent: "claude-code"},
	}
	// "e2e:triage" should match "e2e" via parent prefix
	got, ok := skilldb.ResolveSkillName("e2e:triage", m)
	assert.True(t, ok)
	assert.Equal(t, "e2e", got.Name)
}

func TestResolveSkillName_LastSegment(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"writing-plans": {Name: "writing-plans", SourceAgent: "claude-code"},
	}
	// "superpowers:writing-plans" should match via last segment
	got, ok := skilldb.ResolveSkillName("superpowers:writing-plans", m)
	assert.True(t, ok)
	assert.Equal(t, "writing-plans", got.Name)
}

func TestResolveSkillName_ContainedSegment(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"e2e": {Name: "e2e", SourceAgent: "claude-code"},
	}
	// "cli-e2e-failure-fix" contains "e2e" as a delimited segment
	got, ok := skilldb.ResolveSkillName("cli-e2e-failure-fix", m)
	assert.True(t, ok)
	assert.Equal(t, "e2e", got.Name)
}

func TestResolveSkillName_NoMatch(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"e2e": {Name: "e2e", SourceAgent: "claude-code"},
		"dev": {Name: "dev", SourceAgent: "claude-code"},
	}
	_, ok := skilldb.ResolveSkillName("completely-unknown-skill", m)
	assert.False(t, ok)
}

func TestResolveSkillName_ShortNameNoFalsePositive(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"a": {Name: "a", SourceAgent: "claude-code"},
	}
	// Single-char names should be skipped in substring matching to avoid false positives
	_, ok := skilldb.ResolveSkillName("alpha-beta", m)
	assert.False(t, ok)
}

func TestResolveSkillName_NoPartialWordMatch(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"dev": {Name: "dev", SourceAgent: "claude-code"},
	}
	// "developer-tools" contains "dev" but not at a word boundary (delimiter)
	_, ok := skilldb.ResolveSkillName("developer-tools", m)
	assert.False(t, ok)
}

func TestResolveSkillName_SpaceBoundary(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"brainstorming": {Name: "brainstorming", SourceAgent: "claude-code"},
	}
	// Space after skill name should be recognized as a word boundary.
	got, ok := skilldb.ResolveSkillName("superpowers:brainstorming (visual companion)", m)
	assert.True(t, ok)
	assert.Equal(t, "brainstorming", got.Name)
}

func TestResolveSkillName_ParenthesisStrippedFromLastSegment(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"review": {Name: "review", SourceAgent: "claude-code"},
	}
	// "codex:review (branch)" — last segment is "review (branch)", should strip paren and match "review".
	got, ok := skilldb.ResolveSkillName("codex:review (branch)", m)
	assert.True(t, ok)
	assert.Equal(t, "review", got.Name)
}

func TestResolveSkillName_MultipleColonSegments(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"agent-integration": {Name: "agent-integration", SourceAgent: "claude-code"},
	}
	// "agent-integration:research" should match via parent prefix
	got, ok := skilldb.ResolveSkillName("agent-integration:research", m)
	assert.True(t, ok)
	assert.Equal(t, "agent-integration", got.Name)
}

func TestPopulateFromInsightsDB_UsesTranscriptFallbackForSkillToolSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	testutil.WriteFile(t, repoDir, "README.md", "test repo")
	testutil.GitAdd(t, repoDir, "README.md")
	testutil.GitCommit(t, repoDir, "init")

	repo, err := git.PlainOpen(repoDir)
	require.NoError(t, err)

	store := checkpoint.NewGitStore(repo)
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	transcript := []byte(
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"agent-integration"}}]}}` + "\n",
	)
	err = store.WriteCommitted(ctx, checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   transcript,
		AuthorName:   "Test User",
		AuthorEmail:  "test@example.com",
	})
	require.NoError(t, err)

	insightsPath := filepath.Join(repoDir, "insights.db")
	idb, err := insightsdb.Open(insightsPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, idb.Close()) }()

	rawInsightsDB, err := sql.Open("sqlite", insightsPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, rawInsightsDB.Close()) }()

	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err = rawInsightsDB.ExecContext(ctx, `
		INSERT INTO sessions (
			checkpoint_id, session_id, session_index,
			agent, model, branch, created_at,
			total_tokens, turn_count, overall_score
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cpID.String(), "session-001", 0,
		"Claude Code", "claude-sonnet", "main", createdAt,
		1234, 5, 88.0,
	)
	require.NoError(t, err)

	_, err = rawInsightsDB.ExecContext(ctx, `
		INSERT INTO tool_calls (checkpoint_id, session_index, tool_name, count)
		VALUES (?, ?, ?, ?)`,
		cpID.String(), 0, "Skill", 1,
	)
	require.NoError(t, err)

	skillDBPath := filepath.Join(repoDir, "skill-analytics.db")
	sdb, err := skilldb.Open(skillDBPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, sdb.Close()) }()

	popResult, err := sdb.PopulateFromInsightsDB(ctx, idb, []skilldb.SkillRow{
		{
			Name:        "agent-integration",
			SourceAgent: "claude-code",
			Path:        ".claude/skills/agent-integration/SKILL.md",
			Kind:        "skill",
		},
	}, repoDir)
	require.NoError(t, err)
	assert.Equal(t, 1, popResult.Step2Inserted)

	stats, err := sdb.SkillStats(ctx, "agent-integration", "claude-code")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.TotalSessions)
	assert.Equal(t, int64(1234), stats.TotalTokens)

	sessions, err := sdb.RecentSessions(ctx, "agent-integration", "claude-code", 10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, cpID.String(), sessions[0].CheckpointID)
	assert.Equal(t, "session-001", sessions[0].SessionID)
	assert.Equal(t, "Claude Code", sessions[0].Agent)
	assert.Equal(t, "success", sessions[0].Outcome)
}

func TestPopulateFromInsightsDB_TranscriptFallbackCarriesFriction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	testutil.WriteFile(t, repoDir, "README.md", "test repo")
	testutil.GitAdd(t, repoDir, "README.md")
	testutil.GitCommit(t, repoDir, "init")

	repo, err := git.PlainOpen(repoDir)
	require.NoError(t, err)

	store := checkpoint.NewGitStore(repo)
	cpID := id.MustCheckpointID("b1c2d3e4f5a6")
	transcript := []byte(
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"agent-integration"}}]}}` + "\n",
	)
	err = store.WriteCommitted(ctx, checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-002",
		Strategy:     "manual-commit",
		Transcript:   transcript,
		AuthorName:   "Test User",
		AuthorEmail:  "test@example.com",
	})
	require.NoError(t, err)

	insightsPath := filepath.Join(repoDir, "insights.db")
	idb, err := insightsdb.Open(insightsPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, idb.Close()) }()

	rawInsightsDB, err := sql.Open("sqlite", insightsPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, rawInsightsDB.Close()) }()

	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err = rawInsightsDB.ExecContext(ctx, `
		INSERT INTO sessions (
			checkpoint_id, session_id, session_index,
			agent, model, branch, created_at,
			total_tokens, turn_count, overall_score
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cpID.String(), "session-002", 0,
		"Claude Code", "claude-sonnet", "main", createdAt,
		2000, 8, 75.0,
	)
	require.NoError(t, err)

	_, err = rawInsightsDB.ExecContext(ctx, `
		INSERT INTO tool_calls (checkpoint_id, session_index, tool_name, count)
		VALUES (?, ?, ?, ?)`,
		cpID.String(), 0, "Skill", 1,
	)
	require.NoError(t, err)

	// Insert a skill_signal with a non-matching name but same checkpoint+session.
	// The LLM used "codex:review" but the transcript shows "agent-integration".
	_, err = rawInsightsDB.ExecContext(ctx, `
		INSERT INTO skill_signals (checkpoint_id, session_index, skill_name, friction, missing_instruction)
		VALUES (?, ?, ?, ?, ?)`,
		cpID.String(), 0, "codex:review",
		"Tool call timed out after 30s",
		"Add timeout configuration",
	)
	require.NoError(t, err)

	skillDBPath := filepath.Join(repoDir, "skill-analytics.db")
	sdb, err := skilldb.Open(skillDBPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, sdb.Close()) }()

	_, err = sdb.PopulateFromInsightsDB(ctx, idb, []skilldb.SkillRow{
		{
			Name:        "agent-integration",
			SourceAgent: "claude-code",
			Path:        ".claude/skills/agent-integration/SKILL.md",
			Kind:        "skill",
		},
	}, repoDir)
	require.NoError(t, err)

	// Session should be attributed with friction.
	sessions, err := sdb.RecentSessions(ctx, "agent-integration", "claude-code", 10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "friction", sessions[0].Outcome)
	assert.Equal(t, 1, sessions[0].FrictionCount)

	// Friction themes should be populated.
	themes, err := sdb.SkillFrictionThemes(ctx, "agent-integration", "claude-code")
	require.NoError(t, err)
	require.Len(t, themes, 1)
	assert.Equal(t, "Tool call timed out after 30s", themes[0].Text)

	// Missing instructions should be populated.
	missing, err := sdb.SkillMissingInstructions(ctx, "agent-integration", "claude-code")
	require.NoError(t, err)
	require.Len(t, missing, 1)
	assert.Equal(t, "Add timeout configuration", missing[0].Instruction)
}

func TestPopulateFromInsightsDB_TranscriptFallbackMultipleFrictionItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	testutil.WriteFile(t, repoDir, "README.md", "test repo")
	testutil.GitAdd(t, repoDir, "README.md")
	testutil.GitCommit(t, repoDir, "init")

	repo, err := git.PlainOpen(repoDir)
	require.NoError(t, err)

	store := checkpoint.NewGitStore(repo)
	cpID := id.MustCheckpointID("c2d3e4f5a6b7")
	transcript := []byte(
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"e2e"}}]}}` + "\n",
	)
	err = store.WriteCommitted(ctx, checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-003",
		Strategy:     "manual-commit",
		Transcript:   transcript,
		AuthorName:   "Test User",
		AuthorEmail:  "test@example.com",
	})
	require.NoError(t, err)

	insightsPath := filepath.Join(repoDir, "insights.db")
	idb, err := insightsdb.Open(insightsPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, idb.Close()) }()

	rawInsightsDB, err := sql.Open("sqlite", insightsPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, rawInsightsDB.Close()) }()

	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err = rawInsightsDB.ExecContext(ctx, `
		INSERT INTO sessions (
			checkpoint_id, session_id, session_index,
			agent, model, branch, created_at,
			total_tokens, turn_count, overall_score
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cpID.String(), "session-003", 0,
		"Claude Code", "claude-sonnet", "main", createdAt,
		3000, 10, 60.0,
	)
	require.NoError(t, err)

	_, err = rawInsightsDB.ExecContext(ctx, `
		INSERT INTO tool_calls (checkpoint_id, session_index, tool_name, count)
		VALUES (?, ?, ?, ?)`,
		cpID.String(), 0, "Skill", 1,
	)
	require.NoError(t, err)

	// Multiple friction items stored newline-separated.
	_, err = rawInsightsDB.ExecContext(ctx, `
		INSERT INTO skill_signals (checkpoint_id, session_index, skill_name, friction, missing_instruction)
		VALUES (?, ?, ?, ?, ?)`,
		cpID.String(), 0, "superpowers:brainstorming",
		"Tool call timed out\nIncorrect file path used",
		"Add retry logic",
	)
	require.NoError(t, err)

	skillDBPath := filepath.Join(repoDir, "skill-analytics.db")
	sdb, err := skilldb.Open(skillDBPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, sdb.Close()) }()

	_, err = sdb.PopulateFromInsightsDB(ctx, idb, []skilldb.SkillRow{
		{
			Name:        "e2e",
			SourceAgent: "claude-code",
			Path:        ".claude/skills/e2e/SKILL.md",
			Kind:        "skill",
		},
	}, repoDir)
	require.NoError(t, err)

	sessions, err := sdb.RecentSessions(ctx, "e2e", "claude-code", 10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "friction", sessions[0].Outcome)
	assert.Equal(t, 2, sessions[0].FrictionCount)

	themes, err := sdb.SkillFrictionThemes(ctx, "e2e", "claude-code")
	require.NoError(t, err)
	assert.Len(t, themes, 2)
}
