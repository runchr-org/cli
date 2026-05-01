package cli

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/testutil"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

func TestWhyEnrichCommits_NoCheckpointTrailerRecordsCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	hash := whyTestCommit(t, repoDir, "plain commit")
	repo := whyTestOpenRepo(t, repoDir)

	infoByCommit := enrichWhyCommits(ctx, repo, []whyBlameLine{{CommitHash: hash.String()}})
	info, ok := infoByCommit[hash]
	if !ok {
		t.Fatalf("missing info for commit %s", hash)
	}
	if info.Hash != hash {
		t.Fatalf("hash = %s, want %s", info.Hash, hash)
	}
	if !info.CheckpointID.IsEmpty() {
		t.Fatalf("checkpoint ID = %q, want empty", info.CheckpointID)
	}
}

func TestWhyEnrichCommits_ParsesCheckpointTrailer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	hash := whyTestCommit(t, repoDir, "linked commit\n\nEntire-Checkpoint: "+cpID.String()+"\n")
	repo := whyTestOpenRepo(t, repoDir)

	infoByCommit := enrichWhyCommits(ctx, repo, []whyBlameLine{{CommitHash: hash.String()}})
	info, ok := infoByCommit[hash]
	if !ok {
		t.Fatalf("missing info for commit %s", hash)
	}
	if info.CheckpointID != cpID {
		t.Fatalf("checkpoint ID = %q, want %q", info.CheckpointID, cpID)
	}
}

func TestWhyEnrichCommits_DeduplicatesBlameLines(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	cpID := id.MustCheckpointID("b1b2c3d4e5f6")
	hash := whyTestCommit(t, repoDir, "linked commit\n\nEntire-Checkpoint: "+cpID.String()+"\n")
	repo := whyTestOpenRepo(t, repoDir)

	infoByCommit := enrichWhyCommits(ctx, repo, []whyBlameLine{
		{CommitHash: hash.String(), FinalLine: 1},
		{CommitHash: hash.String(), FinalLine: 2},
	})
	if len(infoByCommit) != 1 {
		t.Fatalf("commit info count = %d, want 1", len(infoByCommit))
	}
	if infoByCommit[hash].CheckpointID != cpID {
		t.Fatalf("checkpoint ID = %q, want %q", infoByCommit[hash].CheckpointID, cpID)
	}
}

func whyTestCommit(t *testing.T, repoDir, message string) plumbing.Hash {
	t.Helper()

	testutil.WriteFile(t, repoDir, "file.go", "package main\n")
	testutil.GitAdd(t, repoDir, "file.go")
	testutil.GitCommit(t, repoDir, message)
	return plumbing.NewHash(testutil.GetHeadHash(t, repoDir))
}

func whyTestOpenRepo(t *testing.T, repoDir string) *git.Repository {
	t.Helper()

	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		t.Fatalf("failed to open repo: %v", err)
	}
	return repo
}
