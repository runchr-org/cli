package checkpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

func TestGmetaStore_WriteCommitted_SingleSession(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID:     cpID,
		SessionID:        "session-001",
		Strategy:         "manual-commit",
		Branch:           "main",
		Transcript:       []byte(`{"type":"text","content":"hello"}`),
		Prompts:          []string{"build a feature"},
		FilesTouched:     []string{"src/foo.go", "src/bar.go"},
		CheckpointsCount: 3,
		AuthorName:       "Test",
		AuthorEmail:      "test@test.com",
		Agent:            agent.AgentTypeClaudeCode,
		Model:            "claude-opus-4-6",
	})
	require.NoError(t, err)

	// Verify ref exists
	ref, err := repo.Reference(plumbing.ReferenceName(GmetaRefName), true)
	require.NoError(t, err)

	// Read the tree and verify structure
	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)

	tree, err := commit.Tree()
	require.NoError(t, err)

	// Verify the target path exists: change-id/<fanout>/<checkpoint-id>/
	targetPath := gmetaTargetPath(cpID)
	targetTree, err := tree.Tree(targetPath)
	require.NoError(t, err)

	// Flatten and check entries
	entries := make(map[string]string)
	flattenTreeToStrings(t, repo, targetTree, "", entries)

	// Check checkpoint-level fields
	assert.Equal(t, "manual-commit", entries["entire/strategy/__value"])
	assert.Equal(t, "main", entries["entire/branch/__value"])
	assert.Equal(t, "3", entries["entire/checkpoints-count/__value"])
	assert.NotEmpty(t, entries["entire/cli-version/__value"])

	// Check files-touched set entries exist (2 files)
	setCount := 0
	for key := range entries {
		if strings.HasPrefix(key, "entire/files-touched/__set/") {
			setCount++
		}
	}
	assert.Equal(t, 2, setCount, "expected 2 files-touched set entries")

	// Check session agent info
	assert.Equal(t, string(agent.AgentTypeClaudeCode), entries["session/session-001/agent/name/__value"])
	assert.Equal(t, "claude-opus-4-6", entries["session/session-001/agent/model/__value"])

	// Check prompt
	assert.Equal(t, "build a feature", entries["session/session-001/prompt/__value"])

	// Check transcript list has entries
	transcriptCount := 0
	for key := range entries {
		if strings.HasPrefix(key, "session/session-001/transcript/__list/") {
			transcriptCount++
		}
	}
	assert.Positive(t, transcriptCount, "expected transcript list entries")

	// Check session IDs list
	idsCount := 0
	for key, val := range entries {
		if strings.HasPrefix(key, "session/ids/__list/") {
			idsCount++
			assert.Equal(t, "session-001", val)
		}
	}
	assert.Equal(t, 1, idsCount, "expected 1 session ID list entry")
}

func TestGmetaStore_WriteCommitted_MultiSession(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write first session
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text","content":"hello"}`),
		Prompts:      []string{"first prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
		Agent:        agent.AgentTypeClaudeCode,
	})
	require.NoError(t, err)

	// Write second session to same checkpoint
	err = store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-002",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text","content":"world"}`),
		Prompts:      []string{"second prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
		Agent:        agent.AgentTypeGemini,
	})
	require.NoError(t, err)

	// Read back and verify both sessions exist
	ref, err := repo.Reference(plumbing.ReferenceName(GmetaRefName), true)
	require.NoError(t, err)
	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)

	targetPath := gmetaTargetPath(cpID)
	targetTree, err := tree.Tree(targetPath)
	require.NoError(t, err)

	entries := make(map[string]string)
	flattenTreeToStrings(t, repo, targetTree, "", entries)

	// Both sessions should exist
	assert.Equal(t, string(agent.AgentTypeClaudeCode), entries["session/session-001/agent/name/__value"])
	assert.Equal(t, string(agent.AgentTypeGemini), entries["session/session-002/agent/name/__value"])
	assert.Equal(t, "first prompt", entries["session/session-001/prompt/__value"])
	assert.Equal(t, "second prompt", entries["session/session-002/prompt/__value"])

	// Session IDs list should have 2 entries
	idsCount := 0
	for key := range entries {
		if strings.HasPrefix(key, "session/ids/__list/") {
			idsCount++
		}
	}
	assert.Equal(t, 2, idsCount, "expected 2 session ID list entries")
}

func TestGmetaStore_UpdateCommitted(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write initial checkpoint
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text","content":"initial"}`),
		Prompts:      []string{"initial prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
		Agent:        agent.AgentTypeClaudeCode,
	})
	require.NoError(t, err)

	// Update with final transcript
	err = store.UpdateCommitted(ctx, UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Transcript:   []byte(`{"type":"text","content":"final complete transcript"}`),
		Prompts:      []string{"updated prompt"},
		Agent:        agent.AgentTypeClaudeCode,
	})
	require.NoError(t, err)

	// Read back and verify updated data
	ref, err := repo.Reference(plumbing.ReferenceName(GmetaRefName), true)
	require.NoError(t, err)
	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)

	targetPath := gmetaTargetPath(cpID)
	targetTree, err := tree.Tree(targetPath)
	require.NoError(t, err)

	entries := make(map[string]string)
	flattenTreeToStrings(t, repo, targetTree, "", entries)

	// Prompt should be updated
	assert.Equal(t, "updated prompt", entries["session/session-001/prompt/__value"])

	// Transcript should be replaced (only new entries)
	transcriptCount := 0
	for key := range entries {
		if strings.HasPrefix(key, "session/session-001/transcript/__list/") {
			transcriptCount++
		}
	}
	assert.Positive(t, transcriptCount, "expected transcript list entries after update")
}

func TestGmetaStore_UpdateCommitted_NotFound(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	// Ensure ref exists but no checkpoints
	require.NoError(t, store.ensureRef(plumbing.ReferenceName(GmetaRefName)))

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")
	err := store.UpdateCommitted(ctx, UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Transcript:   []byte(`{"type":"text"}`),
		Agent:        agent.AgentTypeClaudeCode,
	})
	assert.ErrorIs(t, err, ErrCheckpointNotFound)
}

func TestGmetaStore_WriteCommitted_TaskCheckpoint(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write a task checkpoint
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID:   cpID,
		SessionID:      "session-001",
		Strategy:       "manual-commit",
		IsTask:         true,
		ToolUseID:      "toolu_abc123",
		AgentID:        "subagent-1",
		CheckpointUUID: "uuid-123-456",
		AuthorName:     "Test",
		AuthorEmail:    "test@test.com",
		Agent:          agent.AgentTypeClaudeCode,
	})
	require.NoError(t, err)

	// Read back and verify task entries
	ref, err := repo.Reference(plumbing.ReferenceName(GmetaRefName), true)
	require.NoError(t, err)
	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)

	targetPath := gmetaTargetPath(cpID)
	targetTree, err := tree.Tree(targetPath)
	require.NoError(t, err)

	entries := make(map[string]string)
	flattenTreeToStrings(t, repo, targetTree, "", entries)

	taskPrefix := "session/session-001/task/toolu_abc123/"
	assert.Equal(t, "subagent-1", entries[taskPrefix+"agent-id/__value"])
	assert.Equal(t, "uuid-123-456", entries[taskPrefix+"checkpoint-uuid/__value"])
}

func TestGmetaStore_WriteCommitted_IncrementalTask(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write an incremental task checkpoint
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID:        cpID,
		SessionID:           "session-001",
		Strategy:            "manual-commit",
		IsTask:              true,
		ToolUseID:           "toolu_abc123",
		IsIncremental:       true,
		IncrementalSequence: 1,
		IncrementalType:     "write_file",
		IncrementalData:     []byte(`{"file":"test.go","content":"package main"}`),
		AuthorName:          "Test",
		AuthorEmail:         "test@test.com",
		Agent:               agent.AgentTypeClaudeCode,
	})
	require.NoError(t, err)

	// Read back and verify incremental list entry
	ref, err := repo.Reference(plumbing.ReferenceName(GmetaRefName), true)
	require.NoError(t, err)
	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)

	targetPath := gmetaTargetPath(cpID)
	targetTree, err := tree.Tree(targetPath)
	require.NoError(t, err)

	entries := make(map[string]string)
	flattenTreeToStrings(t, repo, targetTree, "", entries)

	// Should have incremental list entry
	incrementalCount := 0
	for key := range entries {
		if strings.HasPrefix(key, "session/session-001/task/toolu_abc123/incremental/__list/") {
			incrementalCount++
		}
	}
	assert.Equal(t, 1, incrementalCount, "expected 1 incremental list entry")
}

func TestGmetaFanout(t *testing.T) {
	t.Parallel()

	// Verify fanout is SHA-1 based, not prefix-based
	fanout := gmetaFanout("a3b2c4d5e6f7")
	assert.Len(t, fanout, 2)
	// Should NOT be "a3" (that would be prefix-based)
	assert.NotEqual(t, "a3", fanout, "fanout should be SHA-1 hash, not checkpoint ID prefix")
}

func TestGmetaTargetPath(t *testing.T) {
	t.Parallel()
	cpID := id.MustCheckpointID("a3b2c4d5e6f7")
	path := gmetaTargetPath(cpID)

	assert.True(t, strings.HasPrefix(path, "change-id/"))
	parts := strings.Split(path, "/")
	assert.Len(t, parts, 3)
	assert.Equal(t, "change-id", parts[0])
	assert.Len(t, parts[1], 2, "fanout should be 2 hex chars")
	assert.Equal(t, "a3b2c4d5e6f7", parts[2])
}

func TestGmetaStore_MultipleCheckpoints_Preserved(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID1 := id.MustCheckpointID("aaaaaaaaaaaa")
	cpID2 := id.MustCheckpointID("bbbbbbbbbbbb")

	// Write two different checkpoints
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID1,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Prompts:      []string{"first"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	err = store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID2,
		SessionID:    "session-002",
		Strategy:     "manual-commit",
		Prompts:      []string{"second"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	// Both should be readable
	ref, err := repo.Reference(plumbing.ReferenceName(GmetaRefName), true)
	require.NoError(t, err)
	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)

	// Both target paths should exist
	_, err = tree.Tree(gmetaTargetPath(cpID1))
	require.NoError(t, err, "first checkpoint should exist")

	_, err = tree.Tree(gmetaTargetPath(cpID2))
	require.NoError(t, err, "second checkpoint should exist")
}

// --- Read method tests ---

func TestGmetaStore_ReadCommitted_RoundTrip(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID:     cpID,
		SessionID:        "session-001",
		Strategy:         "manual-commit",
		Branch:           "main",
		Transcript:       []byte(`{"type":"text","content":"hello"}`),
		Prompts:          []string{"build a feature"},
		FilesTouched:     []string{"src/foo.go", "src/bar.go"},
		CheckpointsCount: 3,
		AuthorName:       "Test",
		AuthorEmail:      "test@test.com",
		Agent:            agent.AgentTypeClaudeCode,
		Model:            "claude-opus-4-6",
	})
	require.NoError(t, err)

	// Read back
	summary, err := store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	require.NotNil(t, summary)

	assert.Equal(t, cpID, summary.CheckpointID)
	assert.Equal(t, "manual-commit", summary.Strategy)
	assert.Equal(t, "main", summary.Branch)
	assert.Equal(t, 3, summary.CheckpointsCount)
	assert.Len(t, summary.FilesTouched, 2)
	assert.Contains(t, summary.FilesTouched, "src/foo.go")
	assert.Contains(t, summary.FilesTouched, "src/bar.go")
	assert.Len(t, summary.Sessions, 1)
}

func TestGmetaStore_ReadCommitted_NotFound(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")
	summary, err := store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	assert.Nil(t, summary)
}

func TestGmetaStore_ReadSessionContent_RoundTrip(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")
	transcript := []byte(`{"type":"text","content":"hello world"}`)

	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID:     cpID,
		SessionID:        "session-001",
		Strategy:         "manual-commit",
		Branch:           "feature",
		Transcript:       transcript,
		Prompts:          []string{"fix the bug"},
		FilesTouched:     []string{"main.go"},
		CheckpointsCount: 1,
		AuthorName:       "Test",
		AuthorEmail:      "test@test.com",
		Agent:            agent.AgentTypeClaudeCode,
		Model:            "claude-opus-4-6",
	})
	require.NoError(t, err)

	// Read session content
	content, err := store.ReadSessionContent(ctx, cpID, "session-001")
	require.NoError(t, err)
	require.NotNil(t, content)

	assert.Equal(t, cpID, content.Metadata.CheckpointID)
	assert.Equal(t, "session-001", content.Metadata.SessionID)
	assert.Equal(t, agent.AgentTypeClaudeCode, content.Metadata.Agent)
	assert.Equal(t, "claude-opus-4-6", content.Metadata.Model)
	assert.Equal(t, "manual-commit", content.Metadata.Strategy)
	assert.Equal(t, "feature", content.Metadata.Branch)
	assert.Equal(t, 1, content.Metadata.CheckpointsCount)
	assert.Contains(t, content.Metadata.FilesTouched, "main.go")
	assert.Equal(t, "fix the bug", content.Prompts)
	assert.NotEmpty(t, content.Transcript, "transcript should be non-empty")
}

func TestGmetaStore_TokenUsage_RoundTrip(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text","content":"hello"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
		Agent:        agent.AgentTypeClaudeCode,
		TokenUsage: &agent.TokenUsage{
			InputTokens:         8500,
			OutputTokens:        3400,
			CacheReadTokens:     2100,
			CacheCreationTokens: 1200,
			APICallCount:        15,
		},
	})
	require.NoError(t, err)

	// Read session content — token usage should be present
	content, err := store.ReadSessionContent(ctx, cpID, "session-001")
	require.NoError(t, err)
	require.NotNil(t, content.Metadata.TokenUsage)
	assert.Equal(t, 8500, content.Metadata.TokenUsage.InputTokens)
	assert.Equal(t, 3400, content.Metadata.TokenUsage.OutputTokens)
	assert.Equal(t, 2100, content.Metadata.TokenUsage.CacheReadTokens)
	assert.Equal(t, 1200, content.Metadata.TokenUsage.CacheCreationTokens)
	assert.Equal(t, 15, content.Metadata.TokenUsage.APICallCount)

	// Read checkpoint summary — aggregated token usage
	summary, err := store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	require.NotNil(t, summary.TokenUsage)
	assert.Equal(t, 8500, summary.TokenUsage.InputTokens)
	assert.Equal(t, 3400, summary.TokenUsage.OutputTokens)
}

func TestGmetaStore_TokenUsage_MultiSession_Aggregated(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Two sessions with different token usage
	for _, tc := range []struct {
		sid   string
		input int
	}{
		{"session-001", 5000},
		{"session-002", 3000},
	} {
		err := store.WriteCommitted(ctx, WriteCommittedOptions{
			CheckpointID: cpID,
			SessionID:    tc.sid,
			Strategy:     "manual-commit",
			Transcript:   []byte(`{"type":"text"}`),
			AuthorName:   "Test",
			AuthorEmail:  "test@test.com",
			TokenUsage:   &agent.TokenUsage{InputTokens: tc.input, OutputTokens: 1000},
		})
		require.NoError(t, err)
	}

	summary, err := store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	require.NotNil(t, summary.TokenUsage)
	assert.Equal(t, 8000, summary.TokenUsage.InputTokens, "input should be aggregated")
	assert.Equal(t, 2000, summary.TokenUsage.OutputTokens, "output should be aggregated")
}

func TestGmetaStore_TokenUsage_Nil_WhenAbsent(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write without token usage
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	content, err := store.ReadSessionContent(ctx, cpID, "session-001")
	require.NoError(t, err)
	assert.Nil(t, content.Metadata.TokenUsage, "should be nil when no usage written")

	summary, err := store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	assert.Nil(t, summary.TokenUsage, "should be nil when no usage written")
}

func TestGmetaStore_ReadSessionContent_NotFound(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write one session
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	// Read non-existent session
	_, err = store.ReadSessionContent(ctx, cpID, "session-999")
	assert.ErrorIs(t, err, ErrCheckpointNotFound)
}

func TestGmetaStore_GetSessionLog_RoundTrip(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text","content":"log content"}`),
		Prompts:      []string{"do something"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
		Agent:        agent.AgentTypeClaudeCode,
	})
	require.NoError(t, err)

	transcript, sessionID, err := store.GetSessionLog(ctx, cpID)
	require.NoError(t, err)
	assert.Equal(t, "session-001", sessionID)
	assert.NotEmpty(t, transcript)
}

func TestGmetaStore_GetSessionLog_MultiSession_ReturnsLatest(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write two sessions
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text","content":"first"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	err = store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-002",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"text","content":"second"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	_, sessionID, err := store.GetSessionLog(ctx, cpID)
	require.NoError(t, err)
	assert.Equal(t, "session-002", sessionID, "should return latest session")
}

func TestGmetaStore_ReadCommitted_MultiSession(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewGmetaStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a3b2c4d5e6f7")

	// Write two sessions
	for _, sid := range []string{"session-001", "session-002"} {
		err := store.WriteCommitted(ctx, WriteCommittedOptions{
			CheckpointID: cpID,
			SessionID:    sid,
			Strategy:     "manual-commit",
			Transcript:   []byte(`{"type":"text"}`),
			AuthorName:   "Test",
			AuthorEmail:  "test@test.com",
		})
		require.NoError(t, err)
	}

	summary, err := store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Len(t, summary.Sessions, 2, "should have 2 sessions")
}

// flattenTreeToStrings recursively flattens a tree into a map of path -> blob content.
func flattenTreeToStrings(t *testing.T, repo interface {
	BlobObject(hash plumbing.Hash) (*object.Blob, error)
	TreeObject(hash plumbing.Hash) (*object.Tree, error)
}, tree *object.Tree, prefix string, out map[string]string) {
	t.Helper()
	for _, entry := range tree.Entries {
		path := entry.Name
		if prefix != "" {
			path = prefix + "/" + entry.Name
		}

		if entry.Mode.IsFile() {
			blob, err := repo.BlobObject(entry.Hash)
			if err != nil {
				t.Logf("warning: failed to read blob at %s: %v", path, err)
				continue
			}
			reader, err := blob.Reader()
			if err != nil {
				t.Logf("warning: failed to get reader at %s: %v", path, err)
				continue
			}
			content := make([]byte, blob.Size)
			if _, readErr := reader.Read(content); readErr != nil {
				_ = reader.Close()
				t.Logf("warning: failed to read content at %s: %v", path, readErr)
				continue
			}
			_ = reader.Close()
			out[path] = string(content)
		} else {
			subtree, err := repo.TreeObject(entry.Hash)
			if err != nil {
				t.Logf("warning: failed to read subtree at %s: %v", path, err)
				continue
			}
			flattenTreeToStrings(t, repo, subtree, path, out)
		}
	}
}
