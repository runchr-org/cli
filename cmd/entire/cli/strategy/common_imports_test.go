package strategy

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/entireio/cli/redact"
)

func TestListCheckpointsWithImports_UnionsAndFlags(t *testing.T) {
	// Not parallel: uses t.Chdir for CWD-based repo resolution.
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, repoDir, "f.txt", "x")
	if _, err := wt.Add("f.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("init", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repoDir)

	// Write one imported checkpoint directly to the imports ref.
	store := checkpoint.NewGitStore(repo, checkpoint.ImportsRefs())
	red, err := redact.JSONLBytes([]byte(`{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), checkpoint.Session(checkpoint.WriteOptions{
		CheckpointID: id.MustCheckpointID("aabbccddeeff"), SessionID: "s",
		Strategy: "import", Kind: "imported", Transcript: red, Prompts: []string{"hi"}, CheckpointsCount: 1,
	})); err != nil {
		t.Fatal(err)
	}

	infos, err := ListCheckpointsWithImports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var importedCount int
	for _, in := range infos {
		if in.Imported {
			importedCount++
		}
	}
	if importedCount != 1 {
		t.Fatalf("want 1 imported in union, got %d (total %d)", importedCount, len(infos))
	}

	// Default ListCheckpoints (v1 only) must NOT include imports.
	v1, err := ListCheckpoints(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range v1 {
		if in.Imported {
			t.Fatal("imports leaked into v1-only ListCheckpoints")
		}
	}
}
