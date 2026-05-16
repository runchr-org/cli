package strategy

import (
	"context"
	"os/exec"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrePush_PushesLegacyBranchAndBothNewRefs asserts that PrePush sends
// the legacy branch, refs/entire/checkpoints/v1/main, and
// refs/entire/checkpoints/v1/full to the configured remote when all three
// exist locally.
//
// Not parallel: uses t.Chdir() to switch into the test repo.
func TestPrePush_PushesLegacyBranchAndBothNewRefs(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")

	repo, err := git.PlainOpen(tmpDir)
	require.NoError(t, err)

	emptyTree, err := checkpoint.BuildTreeFromEntries(ctx, repo, map[string]object.TreeEntry{})
	require.NoError(t, err)
	commitHash, err := checkpoint.CreateCommit(ctx, repo, emptyTree, plumbing.ZeroHash,
		"seed", "Test", "test@test.com")
	require.NoError(t, err)

	// Seed all three refs at the same commit.
	for _, refName := range []plumbing.ReferenceName{
		plumbing.NewBranchReferenceName(paths.MetadataBranchName),
		plumbing.ReferenceName(paths.MetadataCompactRefName),
		plumbing.ReferenceName(paths.MetadataFullRefName),
	} {
		require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(refName, commitHash)))
	}

	bareDir := t.TempDir()
	initCmd := exec.CommandContext(ctx, "git", "init", "--bare")
	initCmd.Dir = bareDir
	initCmd.Env = testutil.GitIsolatedEnv()
	require.NoError(t, initCmd.Run())

	// Configure origin to point at the bare remote so resolvePushSettings
	// uses it. Use the actual remote name "origin" — strategy.PrePush
	// receives the remote that's pushing.
	addRemoteCmd := exec.CommandContext(ctx, "git", "remote", "add", "origin", bareDir)
	addRemoteCmd.Dir = tmpDir
	addRemoteCmd.Env = testutil.GitIsolatedEnv()
	require.NoError(t, addRemoteCmd.Run())

	t.Chdir(tmpDir)

	s := &ManualCommitStrategy{}
	require.NoError(t, s.PrePush(ctx, "origin"))

	bareRepo, err := git.PlainOpen(bareDir)
	require.NoError(t, err)

	_, err = bareRepo.Reference(plumbing.NewBranchReferenceName(paths.MetadataBranchName), true)
	assert.NoError(t, err, "legacy branch must be pushed")

	_, err = bareRepo.Reference(plumbing.ReferenceName(paths.MetadataCompactRefName), true)
	assert.NoError(t, err, "v1.1 compact ref must be pushed")

	_, err = bareRepo.Reference(plumbing.ReferenceName(paths.MetadataFullRefName), true)
	assert.NoError(t, err, "v1.1 full ref must be pushed")
}

// TestPrePush_SkipsMissingV11Refs verifies that PrePush silently skips
// the v1.1 custom refs when they don't exist locally — no error.
//
// Not parallel: uses t.Chdir().
func TestPrePush_SkipsMissingV11Refs(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")

	bareDir := t.TempDir()
	initCmd := exec.CommandContext(ctx, "git", "init", "--bare")
	initCmd.Dir = bareDir
	initCmd.Env = testutil.GitIsolatedEnv()
	require.NoError(t, initCmd.Run())

	addRemoteCmd := exec.CommandContext(ctx, "git", "remote", "add", "origin", bareDir)
	addRemoteCmd.Dir = tmpDir
	addRemoteCmd.Env = testutil.GitIsolatedEnv()
	require.NoError(t, addRemoteCmd.Run())

	t.Chdir(tmpDir)

	s := &ManualCommitStrategy{}
	require.NoError(t, s.PrePush(ctx, "origin"), "PrePush should succeed even when v1.1 refs are missing")
}
