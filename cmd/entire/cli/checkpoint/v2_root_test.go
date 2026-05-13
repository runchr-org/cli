package checkpoint

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/stretchr/testify/require"
)

// Any accidental change to the inputs in v2_root.go (author, time, message,
// encoding) flips v2FullRootHash. Updating it on purpose creates a
// cross-version migration problem — old and new clients would produce
// different SHAs and the race-resolves-to-no-op property would no longer hold.

func TestBuildV2FullRootCommit_WellKnownSHA(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)

	hash, err := buildV2FullRootCommit(context.Background(), repo)
	require.NoError(t, err)

	require.Equal(t, v2FullRootHash, hash.String(),
		"deterministic root commit SHA changed — see comment on v2FullRootHash")
}

func TestBuildV2FullRootCommit_AcrossDifferentRepos(t *testing.T) {
	t.Parallel()
	repoA := initTestRepo(t)
	repoB := initTestRepo(t)

	hashA, err := buildV2FullRootCommit(context.Background(), repoA)
	require.NoError(t, err)

	hashB, err := buildV2FullRootCommit(context.Background(), repoB)
	require.NoError(t, err)

	require.Equal(t, hashA, hashB,
		"different repos must produce identical root commit SHA")
}

func TestEnsureV2FullRoot_CreatesRefAndCommit(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo, "origin")

	hash, err := store.ensureV2FullRoot(context.Background())
	require.NoError(t, err)
	require.Equal(t, v2FullRootHash, hash.String())

	ref, err := repo.Reference(plumbing.ReferenceName(paths.V2FullRootRefName), true)
	require.NoError(t, err)
	require.Equal(t, hash, ref.Hash(),
		"local ref must point at the deterministic commit")
}

func TestEnsureV2FullRoot_IsIdempotent(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo, "origin")

	first, err := store.ensureV2FullRoot(context.Background())
	require.NoError(t, err)

	second, err := store.ensureV2FullRoot(context.Background())
	require.NoError(t, err)

	require.Equal(t, first, second,
		"repeated calls must return the same hash without changing the ref")
}

// A /full/root ref pointing at a non-deterministic commit (e.g. corruption,
// or a misguided manual repointing) must surface as a warning, not silently
// take over the anchor for future generations. The function returns the
// existing hash so callers continue to operate; the warning is the signal.
func TestEnsureV2FullRoot_WarnsOnUnexpectedHash(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo, "origin")

	bogusTreeHash, err := BuildTreeFromEntries(context.Background(), repo, map[string]object.TreeEntry{})
	require.NoError(t, err)
	bogusHash, err := CreateCommit(context.Background(), repo, bogusTreeHash, plumbing.ZeroHash,
		"unexpected root", "Tamperer", "tamper@example.com")
	require.NoError(t, err)

	require.NoError(t, repo.Storer.SetReference(
		plumbing.NewHashReference(plumbing.ReferenceName(paths.V2FullRootRefName), bogusHash),
	))

	returned, err := store.ensureV2FullRoot(context.Background())
	require.NoError(t, err)
	require.Equal(t, bogusHash, returned,
		"existing ref must be honored (function returns the bogus hash, doesn't auto-repair)")
	require.NotEqual(t, v2FullRootHash, bogusHash.String(),
		"sanity: bogus commit's SHA must differ from the canonical one")
}
