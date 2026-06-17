package cli

import (
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// TestFetchMetadataTreeOnly_DoesNotShallowRepo is a regression test for the
// shallow-metadata false-disconnect.
//
// FetchMetadataTreeOnly resolves the latest checkpoint on resume/explain/attach.
// It used to fetch with --depth=1, which adds the fetched tip to .git/shallow.
// Once the metadata tip is a shallow boundary, a later `git merge-base` against
// refs/remotes/origin/entire/checkpoints/v1 can't reach the real common
// ancestor (it's below the boundary) and the disconnection check falsely
// reports "no common ancestor" — aborting push and looping doctor.
//
// The fix drops --depth=1 and relies on blob filtering for cheapness, so the
// fetch never creates a shallow boundary.
func TestFetchMetadataTreeOnly_DoesNotShallowRepo(t *testing.T) {
	// Uses t.Chdir() — cannot run in parallel.

	tmpDir := t.TempDir()
	bareDir := filepath.Join(tmpDir, "bare.git")
	localDir := filepath.Join(tmpDir, "local")

	runGit(t, tmpDir, "init", "--bare", bareDir)

	// Seed: a main commit plus an orphan metadata branch with two checkpoint
	// commits, so a --depth=1 fetch would visibly truncate to one.
	testutil.InitRepo(t, localDir)
	testutil.WriteFile(t, localDir, "README.md", "hello")
	testutil.GitAdd(t, localDir, "README.md")
	testutil.GitCommit(t, localDir, "init")
	runGit(t, localDir, "branch", "-M", "main")
	runGit(t, localDir, "remote", "add", "origin", bareDir)
	runGit(t, localDir, "checkout", "--orphan", paths.MetadataBranchName)
	runGit(t, localDir, "rm", "-rf", ".")
	testutil.WriteFile(t, localDir, "a/metadata.json", `{"checkpoint_id":"deadbeef0001"}`)
	testutil.GitAdd(t, localDir, "a/metadata.json")
	testutil.GitCommit(t, localDir, "Checkpoint: deadbeef0001")
	testutil.WriteFile(t, localDir, "b/metadata.json", `{"checkpoint_id":"deadbeef0002"}`)
	testutil.GitAdd(t, localDir, "b/metadata.json")
	testutil.GitCommit(t, localDir, "Checkpoint: deadbeef0002")
	runGit(t, localDir, "checkout", "main")
	runGit(t, localDir, "push", "origin", "HEAD:refs/heads/main", paths.MetadataBranchName)
	runGit(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/main")

	originTip := gitOutput(t, bareDir, "rev-parse", "refs/heads/"+paths.MetadataBranchName)

	clonedDir := filepath.Join(tmpDir, "cloned")
	runGit(t, tmpDir, "clone", bareDir, clonedDir)
	runGit(t, clonedDir, "config", "user.email", "test@example.com")
	runGit(t, clonedDir, "config", "user.name", "Test")

	t.Chdir(clonedDir)

	if err := FetchMetadataTreeOnly(t.Context()); err != nil {
		t.Fatalf("FetchMetadataTreeOnly: %v", err)
	}

	// The fix: the tip-read must not leave the repo shallow. Under the old
	// --depth=1 behavior this would be "true".
	if shallow := gitOutput(t, clonedDir, "rev-parse", "--is-shallow-repository"); shallow != "false" {
		t.Errorf("repo is shallow after tree-only fetch (--is-shallow-repository=%q); the tip-read must not create a shallow boundary", shallow)
	}

	// The full metadata history is present (two commits), not truncated to one.
	originRef := "refs/remotes/origin/" + paths.MetadataBranchName
	if n := gitOutput(t, clonedDir, "rev-list", "--count", originRef); n != "2" {
		t.Errorf("origin metadata history has %s commit(s), want 2 (full depth)", n)
	}

	// The local primary ref is advanced to the tip so reads work.
	localRef := "refs/heads/" + paths.MetadataBranchName
	if got := gitOutput(t, clonedDir, "rev-parse", localRef); got != originTip {
		t.Errorf("local primary ref %s = %q, want origin tip %q", localRef, got, originTip)
	}
}
