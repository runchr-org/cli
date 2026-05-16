package checkpoint

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/stretchr/testify/require"
)

// seedRef creates a commit (with optional payload file) and points the named
// ref at it. Returns the new commit hash. Used to seed distinct refs with
// distinct trees so tests can verify which ref the tree-getters resolve to.
func seedRef(t *testing.T, repo *git.Repository, refName plumbing.ReferenceName, payloadPath, payloadContent string) plumbing.Hash {
	t.Helper()
	ctx := context.Background()

	entries := map[string]object.TreeEntry{}
	if payloadPath != "" {
		blobHash, err := CreateBlobFromContent(repo, []byte(payloadContent))
		require.NoError(t, err)
		entries[payloadPath] = object.TreeEntry{
			Name: payloadPath,
			Mode: 0o100644,
			Hash: blobHash,
		}
	}

	treeHash, err := BuildTreeFromEntries(ctx, repo, entries)
	require.NoError(t, err)

	commitHash, err := CreateCommit(ctx, repo, treeHash, plumbing.ZeroHash,
		"seed "+string(refName), "Test", "test@test.com")
	require.NoError(t, err)

	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(refName, commitHash)))
	return commitHash
}

func TestGetMetadataTree_PrefersCompactRef(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	store := &GitStore{repo: repo}

	seedRef(t, repo, plumbing.NewBranchReferenceName(paths.MetadataBranchName), "legacy.txt", "legacy")
	seedRef(t, repo, plumbing.ReferenceName(paths.MetadataFullRefName), "full.txt", "full")
	compactHash := seedRef(t, repo, plumbing.ReferenceName(paths.MetadataCompactRefName), "compact.txt", "compact")

	tree, err := store.getMetadataTree()
	require.NoError(t, err)
	require.NotNil(t, tree)

	compactCommit, err := repo.CommitObject(compactHash)
	require.NoError(t, err)
	require.Equal(t, compactCommit.TreeHash, tree.Hash,
		"compact ref tree must win over full ref and legacy branch trees")
}

func TestGetMetadataTree_FallsBackToFullRef(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	store := &GitStore{repo: repo}

	seedRef(t, repo, plumbing.NewBranchReferenceName(paths.MetadataBranchName), "legacy.txt", "legacy")
	fullHash := seedRef(t, repo, plumbing.ReferenceName(paths.MetadataFullRefName), "full.txt", "full")

	tree, err := store.getMetadataTree()
	require.NoError(t, err)
	require.NotNil(t, tree)

	fullCommit, err := repo.CommitObject(fullHash)
	require.NoError(t, err)
	require.Equal(t, fullCommit.TreeHash, tree.Hash,
		"full ref tree must win when compact ref is absent")
}

func TestGetMetadataTree_FallsBackToLegacyBranch(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	store := &GitStore{repo: repo}

	legacyHash := seedRef(t, repo, plumbing.NewBranchReferenceName(paths.MetadataBranchName), "legacy.txt", "legacy")

	tree, err := store.getMetadataTree()
	require.NoError(t, err)
	require.NotNil(t, tree)

	legacyCommit, err := repo.CommitObject(legacyHash)
	require.NoError(t, err)
	require.Equal(t, legacyCommit.TreeHash, tree.Hash,
		"legacy branch tree must be the last fallback")
}

func TestGetFullTranscriptTree_PrefersLegacyBranchOverFullRef(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	store := &GitStore{repo: repo}

	seedRef(t, repo, plumbing.ReferenceName(paths.MetadataCompactRefName), "compact.txt", "compact")
	legacyHash := seedRef(t, repo, plumbing.NewBranchReferenceName(paths.MetadataBranchName), "legacy.txt", "legacy")
	seedRef(t, repo, plumbing.ReferenceName(paths.MetadataFullRefName), "full.txt", "full")

	tree, err := store.getFullTranscriptTree()
	require.NoError(t, err)
	require.NotNil(t, tree)

	legacyCommit, err := repo.CommitObject(legacyHash)
	require.NoError(t, err)
	require.Equal(t, legacyCommit.TreeHash, tree.Hash,
		"legacy branch must win over full ref — legacy is in-band authoritative in v1.1")
}

func TestGetFullTranscriptTree_FallsBackToFullRef(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	store := &GitStore{repo: repo}

	// No legacy branch, only the v1/full ref.
	fullHash := seedRef(t, repo, plumbing.ReferenceName(paths.MetadataFullRefName), "full.txt", "full")

	tree, err := store.getFullTranscriptTree()
	require.NoError(t, err)
	require.NotNil(t, tree)

	fullCommit, err := repo.CommitObject(fullHash)
	require.NoError(t, err)
	require.Equal(t, fullCommit.TreeHash, tree.Hash,
		"v1/full ref should be the fallback when legacy branch is missing")
}

func TestGetFullTranscriptTree_NeverConsultsCompactRef(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	store := &GitStore{repo: repo}

	// Only the compact ref exists. Full-transcript reader must NOT
	// pick up its tree (silent wrong-type read).
	seedRef(t, repo, plumbing.ReferenceName(paths.MetadataCompactRefName), "compact.txt", "compact")

	_, err := store.getFullTranscriptTree()
	require.Error(t, err,
		"getFullTranscriptTree must fail rather than fall through to the compact ref")
}
