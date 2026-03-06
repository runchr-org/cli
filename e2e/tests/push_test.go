//go:build e2e

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/entireio/cli/e2e/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPush_CheckpointsPushedToRemote validates that a manual push (outside the
// agent turn) triggers the pre-push hook which pushes entire/checkpoints/v1.
func TestPush_CheckpointsPushedToRemote(t *testing.T) {
	testutil.ForEachAgent(t, 4*time.Minute, func(t *testing.T, s *testutil.RepoState, ctx context.Context) {
		bareDir := testutil.SetupBareRemote(t, s)

		// Agent creates a file and commits, but does NOT push.
		_, err := s.RunPrompt(t, ctx,
			"create a file at docs/hello.txt about greetings, then commit. Do not push.")
		require.NoError(t, err)

		testutil.AssertFileExists(t, s.Dir, "docs/hello.txt")
		testutil.AssertNewCommits(t, s, 1)
		testutil.WaitForCheckpoint(t, s, 15*time.Second)

		cpID := testutil.AssertHasCheckpointTrailer(t, s.Dir, "HEAD")
		testutil.AssertCheckpointExists(t, s.Dir, cpID)

		// Manual push — triggers pre-push hook which pushes checkpoints.
		s.Git(t, "push", "origin", "HEAD")

		// Remote should have the user's commit.
		remoteHead := testutil.GitOutput(t, bareDir, "rev-parse", "HEAD")
		localHead := testutil.GitOutput(t, s.Dir, "rev-parse", "HEAD")
		assert.Equal(t, localHead, remoteHead, "remote HEAD should match local HEAD")

		// Remote should have the checkpoints branch with our checkpoint.
		remoteCpRef, err := testutil.GitOutputErr(bareDir, "rev-parse", "entire/checkpoints/v1")
		require.NoError(t, err, "remote should have entire/checkpoints/v1 branch")
		assert.NotEmpty(t, remoteCpRef)

		cpPath := testutil.CheckpointPath(cpID)
		remoteBlob := testutil.GitOutput(t, bareDir, "show", "entire/checkpoints/v1:"+cpPath+"/0/full.jsonl")
		assert.NotEmpty(t, remoteBlob, "remote should have the transcript for checkpoint %s", cpID)
	})
}

// TestPush_TurnEndPushesFinalizedTranscripts validates the turn-end push flow:
// when checkpoints are pushed mid-turn, HandleTurnEnd pushes the finalized
// transcript to keep the remote consistent.
//
// In Vogon's runTurn, the sequence is:
//  1. appendTranscript("user", prompt)
//  2. executeActions → commit (post-commit condenses checkpoint) → push (pre-push pushes provisional transcript)
//  3. appendTranscript("assistant", "Done.") ← added AFTER push
//  4. fireHook("stop") → HandleTurnEnd re-reads transcript, pushes finalized version
//
// The remote transcript should contain the assistant response, proving the
// turn-end push updated it beyond the provisional version from step 2.
func TestPush_TurnEndPushesFinalizedTranscripts(t *testing.T) {
	testutil.ForEachAgent(t, 4*time.Minute, func(t *testing.T, s *testutil.RepoState, ctx context.Context) {
		bareDir := testutil.SetupBareRemote(t, s)

		// Agent creates a file, commits, and pushes — all in one turn.
		_, err := s.RunPrompt(t, ctx,
			"create a file at docs/story.txt about a short story, then commit and push")
		require.NoError(t, err)

		testutil.WaitForCheckpoint(t, s, 15*time.Second)
		cpID := testutil.AssertHasCheckpointTrailer(t, s.Dir, "HEAD")

		// HandleTurnEnd finalizes the local checkpoint (re-reads complete
		// transcript via store.UpdateCommitted). If the turn-end push worked,
		// the remote transcript should match the local finalized version.
		// If it didn't push, the remote would still have the provisional
		// transcript from the mid-turn pre-push (missing the trailing
		// conversation added after the push).
		cpPath := testutil.CheckpointPath(cpID)
		transcriptBlob := "entire/checkpoints/v1:" + cpPath + "/0/full.jsonl"
		localTranscript := testutil.GitOutput(t, s.Dir, "show", transcriptBlob)
		remoteTranscript := testutil.GitOutput(t, bareDir, "show", transcriptBlob)
		assert.Equal(t, localTranscript, remoteTranscript,
			"remote transcript should match local finalized transcript")
	})
}

// TestPush_NoPushDuringTurn_NoTurnEndPush validates that when no push happens
// during a turn, HandleTurnEnd does NOT push checkpoints to the remote.
// This respects user intent — if they haven't pushed, we shouldn't either.
func TestPush_NoPushDuringTurn_NoTurnEndPush(t *testing.T) {
	testutil.ForEachAgent(t, 4*time.Minute, func(t *testing.T, s *testutil.RepoState, ctx context.Context) {
		bareDir := testutil.SetupBareRemote(t, s)

		// Record the remote checkpoint branch state after setup.
		// SetupBareRemote's git push triggers the pre-push hook which may
		// push the initial checkpoint branch, so we snapshot here.
		remoteCpBefore := testutil.GitOutput(t, bareDir, "rev-parse", "entire/checkpoints/v1")

		// Agent creates a file and commits, but does NOT push.
		_, err := s.RunPrompt(t, ctx,
			"create a file at docs/local.txt about local changes, then commit. Do not push.")
		require.NoError(t, err)

		testutil.AssertFileExists(t, s.Dir, "docs/local.txt")
		testutil.AssertNewCommits(t, s, 1)
		testutil.WaitForCheckpoint(t, s, 15*time.Second)

		// Local checkpoint should have advanced.
		testutil.AssertCheckpointAdvanced(t, s)

		// Remote checkpoint branch should NOT have advanced — no push happened.
		remoteCpAfter := testutil.GitOutput(t, bareDir, "rev-parse", "entire/checkpoints/v1")
		assert.Equal(t, remoteCpBefore, remoteCpAfter,
			"remote checkpoint branch should not advance when no push happened during turn")
	})
}
