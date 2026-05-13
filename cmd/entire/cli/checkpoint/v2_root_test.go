package checkpoint

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/stretchr/testify/require"
)

// Pinned SHA for the deterministic /full/root commit. Any accidental change
// to the inputs in v2_root.go (author, time, message, encoding) flips this.
// Updating it on purpose creates a cross-version migration problem — old
// and new clients would produce different SHAs and the race-resolves-to-no-op
// property would no longer hold.
const expectedV2FullRootHash = "c095af40b171ff4c3c4a781abacd39aa499e183b"

func TestBuildV2FullRootCommit_WellKnownSHA(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)

	hash, err := buildV2FullRootCommit(context.Background(), repo)
	require.NoError(t, err)

	require.Equal(t, expectedV2FullRootHash, hash.String(),
		"deterministic root commit SHA changed — see comment on expectedV2FullRootHash")
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
	require.Equal(t, expectedV2FullRootHash, hash.String())

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

func TestEnsureV2FullRoot_PreservesPreexistingRef(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo, "origin")

	first, err := store.ensureV2FullRoot(context.Background())
	require.NoError(t, err)

	second, err := store.ensureV2FullRoot(context.Background())
	require.NoError(t, err)
	require.Equal(t, first, second)

	ref, err := repo.Reference(plumbing.ReferenceName(paths.V2FullRootRefName), true)
	require.NoError(t, err)
	require.Equal(t, first, ref.Hash())
}
