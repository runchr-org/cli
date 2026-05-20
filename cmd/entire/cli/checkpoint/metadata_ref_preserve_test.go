package checkpoint_test

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// seedRef writes a synthetic hash to a ref so PreserveV1History has
// something to copy. The hash doesn't need a real commit behind it for the
// SetReference path the function exercises.
func seedRef(t *testing.T, repo *git.Repository, refName plumbing.ReferenceName, hashHex string) plumbing.Hash {
	t.Helper()
	h := plumbing.NewHash(hashHex)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(refName, h)); err != nil {
		t.Fatalf("SetReference(%s): %v", refName, err)
	}
	return h
}

func openRepo(t *testing.T, dir string) *git.Repository {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	return repo
}

func TestPreserveV1History_CopiesLegacyTipOnFirstAccess(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	t.Chdir(dir)
	writeSettings(t, dir, `{"strategy_options":{"checkpoints_version":"1.1"}}`)

	repo := openRepo(t, dir)
	legacy := plumbing.NewBranchReferenceName(paths.MetadataBranchName)
	custom := plumbing.ReferenceName(paths.MetadataRefName)

	legacyHash := seedRef(t, repo, legacy, "1111111111111111111111111111111111111111")

	if err := checkpoint.PreserveV1History(context.Background(), repo); err != nil {
		t.Fatalf("PreserveV1History: %v", err)
	}

	got, err := repo.Reference(custom, false)
	if err != nil {
		t.Fatalf("custom ref missing after preservation: %v", err)
	}
	if got.Hash() != legacyHash {
		t.Fatalf("custom hash = %s; want %s", got.Hash(), legacyHash)
	}
	// Legacy untouched.
	legacyRef, err := repo.Reference(legacy, false)
	if err != nil {
		t.Fatalf("legacy ref missing: %v", err)
	}
	if legacyRef.Hash() != legacyHash {
		t.Fatalf("legacy hash changed: got %s want %s", legacyRef.Hash(), legacyHash)
	}
}

func TestPreserveV1History_NoopWhenCustomRefAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	t.Chdir(dir)
	writeSettings(t, dir, `{"strategy_options":{"checkpoints_version":"1.1"}}`)

	repo := openRepo(t, dir)
	legacy := plumbing.NewBranchReferenceName(paths.MetadataBranchName)
	custom := plumbing.ReferenceName(paths.MetadataRefName)
	seedRef(t, repo, legacy, "1111111111111111111111111111111111111111")
	customHash := seedRef(t, repo, custom, "2222222222222222222222222222222222222222")

	if err := checkpoint.PreserveV1History(context.Background(), repo); err != nil {
		t.Fatalf("PreserveV1History: %v", err)
	}

	got, _ := repo.Reference(custom, false)
	if got.Hash() != customHash {
		t.Fatalf("custom ref was rewritten: got %s want %s", got.Hash(), customHash)
	}
}

func TestPreserveV1History_NoLegacyBranch_NoOp(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	t.Chdir(dir)
	writeSettings(t, dir, `{"strategy_options":{"checkpoints_version":"1.1"}}`)

	repo := openRepo(t, dir)
	custom := plumbing.ReferenceName(paths.MetadataRefName)

	if err := checkpoint.PreserveV1History(context.Background(), repo); err != nil {
		t.Fatalf("PreserveV1History: %v", err)
	}

	if _, err := repo.Reference(custom, false); err == nil {
		t.Fatalf("custom ref should not exist when there was nothing to preserve")
	}
}

func TestPreserveV1History_V1Mode_NoOp(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	t.Chdir(dir)
	writeSettings(t, dir, `{"strategy_options":{"checkpoints_version":1}}`)

	repo := openRepo(t, dir)
	legacy := plumbing.NewBranchReferenceName(paths.MetadataBranchName)
	custom := plumbing.ReferenceName(paths.MetadataRefName)
	seedRef(t, repo, legacy, "1111111111111111111111111111111111111111")

	if err := checkpoint.PreserveV1History(context.Background(), repo); err != nil {
		t.Fatalf("PreserveV1History: %v", err)
	}
	if _, err := repo.Reference(custom, false); err == nil {
		t.Fatalf("custom ref should not exist in v1 mode")
	}
}
