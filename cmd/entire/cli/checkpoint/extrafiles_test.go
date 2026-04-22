package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/redact"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// TestWriteCommitted_ReadSessionContent_RoundtripsExtraFiles verifies that
// agent-contributed extra files survive the checkpoint write/read roundtrip
// on the metadata branch. This is the commit→resume path that lets an agent's
// CheckpointRestorer recover its native data (e.g., Cursor's store.db).
func TestWriteCommitted_ReadSessionContent_RoundtripsExtraFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("init", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t"},
	}); err != nil {
		t.Fatal(err)
	}

	store := NewGitStore(repo)
	checkpointID := id.MustCheckpointID("abc123def456")

	sessionID := "2026-04-22-zzz"
	extras := map[string][]byte{
		sessionID + ".cursor-chat.json": []byte(`{"format":"cursor-chat-export","version":2}`),
	}

	if err := store.WriteCommitted(context.Background(), WriteCommittedOptions{
		CheckpointID: checkpointID,
		SessionID:    sessionID,
		Strategy:     "manual-commit",
		Agent:        types.AgentType("Cursor"),
		Transcript:   redact.AlreadyRedacted([]byte(`{"role":"user","content":"hi"}` + "\n")),
		AuthorName:   "t",
		AuthorEmail:  "t@t",
		ExtraFiles:   extras,
	}); err != nil {
		t.Fatalf("WriteCommitted: %v", err)
	}

	content, err := store.ReadSessionContent(context.Background(), checkpointID, 0)
	if err != nil {
		t.Fatalf("ReadSessionContent: %v", err)
	}
	if content.ExtraFiles == nil {
		t.Fatalf("expected ExtraFiles to be populated, got nil")
	}
	got, ok := content.ExtraFiles[sessionID+".cursor-chat.json"]
	if !ok {
		t.Fatalf("expected key %q in ExtraFiles, got %v", sessionID+".cursor-chat.json", content.ExtraFiles)
	}
	if string(got) != string(extras[sessionID+".cursor-chat.json"]) {
		t.Errorf("ExtraFiles bytes mismatch")
	}
}
