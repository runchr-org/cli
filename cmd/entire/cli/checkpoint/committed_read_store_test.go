package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/entireio/cli/redact"
)

// newTestRepo creates an isolated repo with a single "init" commit and returns
// its directory, an open handle, and the commit hash.
func newTestRepo(t *testing.T) (string, *git.Repository, plumbing.Hash) {
	t.Helper()
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)
	return dir, repo, commitFile(t, repo, dir, "f.txt", "init", "init")
}

// commitFile adds path with content to the worktree and commits it, returning
// the new commit hash. Successive calls build a linear ancestry chain.
func commitFile(t *testing.T, repo *git.Repository, dir, path, content, msg string) plumbing.Hash {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644))
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add(path)
	require.NoError(t, err)
	h, err := wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com"},
	})
	require.NoError(t, err)
	return h
}

func setRef(t *testing.T, repo *git.Repository, name plumbing.ReferenceName, hash plumbing.Hash) {
	t.Helper()
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(name, hash)))
}

func v1BranchRef() plumbing.ReferenceName {
	return plumbing.NewBranchReferenceName(paths.MetadataBranchName)
}
func customRef() plumbing.ReferenceName { return plumbing.ReferenceName(paths.MetadataRefName) }
func originV1Ref() plumbing.ReferenceName {
	return plumbing.NewRemoteReferenceName("origin", paths.MetadataBranchName)
}

func customRefHash(t *testing.T, repo *git.Repository) (plumbing.Hash, bool) {
	t.Helper()
	ref, err := repo.Reference(customRef(), true)
	if err != nil {
		return plumbing.ZeroHash, false
	}
	return ref.Hash(), true
}

// writeV1Checkpoint writes a committed checkpoint to the v1 branch via the
// default store.
func writeV1Checkpoint(t *testing.T, repo *git.Repository, cpID id.CheckpointID, sessionID string) {
	t.Helper()
	require.NoError(t, NewGitStore(repo).WriteCommitted(context.Background(), WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    sessionID,
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted([]byte("transcript\n")),
		Prompts:      []string{"prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))
}

// enableV11 chdirs into dir and writes settings opting into checkpoints v1.1.
func enableV11(t *testing.T, dir string) {
	t.Helper()
	t.Chdir(dir)
	writeSettings(t, dir, `"1.1"`)
}

// writeSettings writes .entire/settings.json with the given checkpoints_version
// (empty string omits the option) into dir.
func writeSettings(t *testing.T, dir, version string) {
	t.Helper()
	body := `{"enabled": true}`
	if version != "" {
		body = `{"enabled": true, "strategy_options": {"checkpoints_version": ` + version + `}}`
	}
	entireDir := filepath.Join(dir, ".entire")
	require.NoError(t, os.MkdirAll(entireDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(entireDir, paths.SettingsFileName), []byte(body), 0o644))
}

func TestNewGitStore_DefaultsToV1Branch(t *testing.T) {
	t.Parallel()
	assert.Equal(t, v1BranchRef(), NewGitStore(nil).CommittedReadRef())
}

func TestNewGitStoreWithRef(t *testing.T) {
	t.Parallel()
	assert.Equal(t, customRef(), NewGitStoreWithRef(nil, customRef()).CommittedReadRef())
}

func TestSyncV1CustomRefForRead_SeedsWhenMissing(t *testing.T) {
	t.Parallel()
	_, repo, h := newTestRepo(t)
	setRef(t, repo, v1BranchRef(), h)

	syncV1CustomRefForRead(context.Background(), repo)

	got, ok := customRefHash(t, repo)
	require.True(t, ok, "custom ref should be seeded from v1")
	assert.Equal(t, h, got)
}

func TestSyncV1CustomRefForRead_SeedsFromOriginWhenLocalV1Missing(t *testing.T) {
	t.Parallel()
	_, repo, h := newTestRepo(t)
	setRef(t, repo, originV1Ref(), h) // only the remote-tracking branch exists

	syncV1CustomRefForRead(context.Background(), repo)

	got, ok := customRefHash(t, repo)
	require.True(t, ok, "custom ref should be seeded from origin v1")
	assert.Equal(t, h, got)
}

func TestSyncV1CustomRefForRead_NoopWhenEqual(t *testing.T) {
	t.Parallel()
	_, repo, h := newTestRepo(t)
	setRef(t, repo, v1BranchRef(), h)
	setRef(t, repo, customRef(), h)

	syncV1CustomRefForRead(context.Background(), repo)

	got, _ := customRefHash(t, repo)
	assert.Equal(t, h, got)
}

func TestSyncV1CustomRefForRead_AdvancesWhenAncestor(t *testing.T) {
	t.Parallel()
	dir, repo, old := newTestRepo(t)
	setRef(t, repo, customRef(), old)
	newHash := commitFile(t, repo, dir, "f2.txt", "more", "second")
	setRef(t, repo, v1BranchRef(), newHash)

	syncV1CustomRefForRead(context.Background(), repo)

	got, _ := customRefHash(t, repo)
	assert.Equal(t, newHash, got, "custom ref should advance to the v1 tip")
}

func TestSyncV1CustomRefForRead_LeavesNonAncestorRef(t *testing.T) {
	t.Parallel()
	dir, repo, first := newTestRepo(t)
	second := commitFile(t, repo, dir, "f2.txt", "more", "second")
	// v1 at the parent, custom ref ahead at the child: not an ancestor of v1.
	setRef(t, repo, v1BranchRef(), first)
	setRef(t, repo, customRef(), second)

	syncV1CustomRefForRead(context.Background(), repo)

	got, _ := customRefHash(t, repo)
	assert.Equal(t, second, got, "non-ancestor custom ref must not be rewound")
}

func TestSyncV1CustomRefForRead_NoV1TipNoOp(t *testing.T) {
	t.Parallel()
	_, repo, _ := newTestRepo(t) // no v1 branch, local or origin

	syncV1CustomRefForRead(context.Background(), repo)

	_, ok := customRefHash(t, repo)
	assert.False(t, ok, "custom ref must not be created without a v1 tip")
}

func TestSyncV1CustomRefForRead_WriteFailureLeavesRefUnset(t *testing.T) {
	t.Parallel()
	dir, repo, h := newTestRepo(t)
	setRef(t, repo, v1BranchRef(), h)
	blockCustomRefWrite(t, dir)

	syncV1CustomRefForRead(context.Background(), repo) // must not panic

	_, ok := customRefHash(t, repo)
	assert.False(t, ok, "custom ref must not exist when the write was blocked")
}

// blockCustomRefWrite occupies refs/entire with a file so refs/entire/* cannot
// be created.
func blockCustomRefWrite(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "refs", "entire"), []byte("blocked"), 0o644))
}

// Not parallel: uses t.Chdir() so settings.Load resolves the test repo.
func TestNewCommittedReadStore_SelectsRefByVersion(t *testing.T) {
	dir, repo, h := newTestRepo(t)
	setRef(t, repo, v1BranchRef(), h)
	t.Chdir(dir)

	writeSettings(t, dir, "") // v1 only
	assert.Equal(t, v1BranchRef(), NewCommittedReadStore(context.Background(), repo).CommittedReadRef())

	writeSettings(t, dir, `"1.1"`)
	assert.Equal(t, customRef(), NewCommittedReadStore(context.Background(), repo).CommittedReadRef())
}

// Not parallel: uses t.Chdir().
func TestNewCommittedReadStore_ReadsMirroredData(t *testing.T) {
	dir, repo, _ := newTestRepo(t)
	enableV11(t, dir)

	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	writeV1Checkpoint(t, repo, cpID, "session-001")

	// The v1.1 read store sync-then-reads the same checkpoint; equivalence below
	// proves the custom ref was populated from v1.
	readStore := NewCommittedReadStore(context.Background(), repo)
	require.Equal(t, customRef(), readStore.CommittedReadRef())

	v1Summary, err := NewGitStore(repo).ReadCommitted(context.Background(), cpID)
	require.NoError(t, err)
	v11Summary, err := readStore.ReadCommitted(context.Background(), cpID)
	require.NoError(t, err)
	assert.Equal(t, v1Summary, v11Summary)
}

// v1 metadata exists only as origin/entire/checkpoints/v1: v1.1 mode seeds the
// custom ref from the remote tip and reads through it, never falling back to v1.
//
// Not parallel: uses t.Chdir().
func TestNewCommittedReadStore_ReadsV11ForRemoteOnlyMetadata(t *testing.T) {
	dir, repo, _ := newTestRepo(t)
	enableV11(t, dir)

	cpID := id.MustCheckpointID("b1b2c3d4e5f6")
	writeV1Checkpoint(t, repo, cpID, "session-remote")

	// Make v1 metadata origin-only: mirror the branch to the remote ref, drop it.
	v1Ref, err := repo.Reference(v1BranchRef(), true)
	require.NoError(t, err)
	setRef(t, repo, originV1Ref(), v1Ref.Hash())
	require.NoError(t, repo.Storer.RemoveReference(v1BranchRef()))

	readStore := NewCommittedReadStore(context.Background(), repo)
	require.Equal(t, customRef(), readStore.CommittedReadRef(), "must read the custom ref, not fall back to v1")

	summary, err := readStore.ReadCommitted(context.Background(), cpID)
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, cpID, summary.CheckpointID)
}

// When the custom ref cannot be written, v1.1 mode still binds reads to it (no
// v1 fallback), so a v1-only checkpoint is not found.
//
// Not parallel: uses t.Chdir().
func TestNewCommittedReadStore_BindsV11WhenSyncFails(t *testing.T) {
	dir, repo, _ := newTestRepo(t)
	enableV11(t, dir)

	cpID := id.MustCheckpointID("c1c2c3d4e5f6")
	writeV1Checkpoint(t, repo, cpID, "session-write-fails")
	blockCustomRefWrite(t, dir)

	readStore := NewCommittedReadStore(context.Background(), repo)
	require.Equal(t, customRef(), readStore.CommittedReadRef(), "must not fall back to v1 when sync fails")

	summary, err := readStore.ReadCommitted(context.Background(), cpID)
	require.NoError(t, err)
	assert.Nil(t, summary, "must not fall back to v1 when the custom ref is unavailable")
}

// A diverged custom ref is read as-is; the v1-only checkpoint is not found.
//
// Not parallel: uses t.Chdir().
func TestNewCommittedReadStore_BindsV11WhenCustomRefDiverges(t *testing.T) {
	dir, repo, _ := newTestRepo(t)
	divergedHash := commitFile(t, repo, dir, "other.txt", "diverged", "worktree commit")
	enableV11(t, dir)

	cpID := id.MustCheckpointID("d1d2c3d4e5f6")
	writeV1Checkpoint(t, repo, cpID, "session-diverged")
	setRef(t, repo, customRef(), divergedHash) // not an ancestor of v1

	readStore := NewCommittedReadStore(context.Background(), repo)
	require.Equal(t, customRef(), readStore.CommittedReadRef(), "must read the diverged custom ref, not fall back to v1")

	summary, err := readStore.ReadCommitted(context.Background(), cpID)
	require.NoError(t, err)
	assert.Nil(t, summary, "diverged custom ref read must not fall back to v1")
}
