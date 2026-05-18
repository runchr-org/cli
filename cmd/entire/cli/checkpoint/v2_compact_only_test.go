package checkpoint

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/redact"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/stretchr/testify/require"
)

func TestV2GitStore_WriteCompactOnly_WritesCompactRefAndSkipsFullCurrent(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	store := NewV2GitStore(repo, "origin")

	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	opts := WriteCommittedOptions{
		CheckpointID:      cpID,
		SessionID:         "compact-only-session-A",
		Strategy:          "manual-commit",
		Transcript:        redact.AlreadyRedacted([]byte(`{"event":"start"}` + "\n")),
		Prompts:           []string{"hello"},
		FilesTouched:      []string{"a.txt"},
		AuthorName:        "Test",
		AuthorEmail:       "test@example.com",
		CompactTranscript: []byte(`{"event":"start"}` + "\n"),
	}

	idx, err := store.WriteCompactOnly(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, 0, idx)

	// Compact ref must exist and point at a real commit.
	mainRef, err := repo.Reference(plumbing.ReferenceName(paths.MetadataCompactRefName), true)
	require.NoError(t, err, "compact ref must exist after WriteCompactOnly")
	require.False(t, mainRef.Hash().IsZero())

	// /full/current must NOT have been created by this call.
	_, err = repo.Reference(plumbing.ReferenceName(paths.V2FullCurrentRefName), true)
	require.Error(t, err, "WriteCompactOnly must not touch /full/current")
}

func TestV2GitStore_WriteFullOnly_WritesFullCurrentForExistingMain(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	store := NewV2GitStore(repo, "origin")

	cpID := id.MustCheckpointID("b2c3d4e5f6a1")
	opts := WriteCommittedOptions{
		CheckpointID:      cpID,
		SessionID:         "session-B",
		Strategy:          "manual-commit",
		Transcript:        redact.AlreadyRedacted([]byte(`{"event":"start"}` + "\n")),
		Prompts:           []string{"hi"},
		FilesTouched:      []string{"b.txt"},
		AuthorName:        "Test",
		AuthorEmail:       "test@example.com",
		CompactTranscript: []byte(`{"event":"start"}` + "\n"),
	}

	// Seed /main and capture the session index for the /full write.
	sessionIndex, err := store.WriteCompactOnly(context.Background(), opts)
	require.NoError(t, err)

	// /full/current does not exist yet.
	_, err = repo.Reference(plumbing.ReferenceName(paths.V2FullCurrentRefName), true)
	require.Error(t, err)

	require.NoError(t, store.WriteFullOnly(context.Background(), opts, sessionIndex))

	fullRef, err := repo.Reference(plumbing.ReferenceName(paths.V2FullCurrentRefName), true)
	require.NoError(t, err, "/full/current must exist after WriteFullOnly")
	require.False(t, fullRef.Hash().IsZero())
}
