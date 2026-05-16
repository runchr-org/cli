package strategy

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCondense_WritesLegacyBranchAndBothNewRefs_NoV2 drives a single
// condensation without setting checkpoints_v2 and asserts:
//   - the legacy branch refs/heads/entire/checkpoints/v1 still gets a
//     full v1-style tree (full.jsonl);
//   - refs/entire/checkpoints/v1/full is aliased to the same commit hash
//     as the legacy branch (write-alias contract);
//   - refs/entire/checkpoints/v1/main carries a compact tree
//     (transcript.jsonl) at an independent commit;
//   - refs/entire/checkpoints/v2/full/current does NOT exist.
func TestCondense_WritesLegacyBranchAndBothNewRefs_NoV2(t *testing.T) {
	dir := setupGitRepo(t)
	t.Chdir(dir)

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)

	s := &ManualCommitStrategy{}
	sessionID := "test-v11-dualwrite-nov2"

	setupSessionWithCheckpoint(t, s, repo, dir, sessionID)

	state, err := s.loadSessionState(context.Background(), sessionID)
	require.NoError(t, err)
	state.Phase = session.PhaseIdle
	// Set a real agent type so the compact-transcript builder produces a
	// non-empty transcript.jsonl.
	state.AgentType = agent.AgentTypeClaudeCode
	require.NoError(t, s.saveSessionState(context.Background(), state))

	commitWithCheckpointTrailer(t, repo, dir, "a1b2c3d4e5f6")

	require.NoError(t, s.PostCommit(context.Background()))

	// Legacy branch must exist (today's behavior, unchanged).
	legacyRef, err := repo.Reference(
		plumbing.NewBranchReferenceName(paths.MetadataBranchName), true)
	require.NoError(t, err, "legacy branch must exist after condensation")
	require.False(t, legacyRef.Hash().IsZero())

	// The legacy tree must contain full.jsonl for the just-condensed session.
	cpPath := "a1/b2c3d4e5f6"
	assert.True(t, refTreeContainsFile(t, repo, legacyRef.Hash(),
		cpPath+"/0/"+paths.TranscriptFileName),
		"legacy branch tree must contain full.jsonl")

	// v1/full must exist AND point at the same commit hash as the legacy
	// branch (write-alias contract).
	fullRef, err := repo.Reference(
		plumbing.ReferenceName(paths.MetadataFullRefName), true)
	require.NoError(t, err, "full ref must exist after condensation")
	require.Equal(t, legacyRef.Hash(), fullRef.Hash(),
		"refs/entire/checkpoints/v1/full must point at the same commit as the legacy branch")

	// v1/main must exist and carry transcript.jsonl. It must NOT share its
	// commit hash with the legacy branch (it's an independent commit chain).
	compactRef, err := repo.Reference(
		plumbing.ReferenceName(paths.MetadataCompactRefName), true)
	require.NoError(t, err, "compact ref must exist after condensation")
	assert.True(t, refTreeContainsFile(t, repo, compactRef.Hash(),
		cpPath+"/0/"+paths.CompactTranscriptFileName),
		"compact ref tree must contain transcript.jsonl")
	assert.NotEqual(t, legacyRef.Hash(), compactRef.Hash(),
		"compact ref is an independent commit chain, not an alias of the legacy branch")

	// v2 /full/current must NOT exist (v2 was not enabled).
	_, err = repo.Reference(
		plumbing.ReferenceName(paths.V2FullCurrentRefName), true)
	require.Error(t, err, "v2 /full/current must NOT exist when v2 disabled")
}

// refTreeContainsFile reports whether the tree at the given commit hash
// contains an entry at the given path.
func refTreeContainsFile(t *testing.T, repo *git.Repository, commitHash plumbing.Hash, path string) bool {
	t.Helper()
	commit, err := repo.CommitObject(commitHash)
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)
	_, err = tree.File(path)
	return err == nil
}
