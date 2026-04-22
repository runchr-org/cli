package strategy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/paths"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// TestRewind_RestoresCursorChatArchive seeds a checkpoint whose metadata
// directory contains a Cursor chat archive, calls Rewind, and asserts the
// archive was written back to ~/.cursor/chats/<hash>/<id>/store.db.
//
// This is the inverse of what CheckpointContributor writes on commit.
func TestRewind_RestoresCursorChatArchive(t *testing.T) {
	// Cannot t.Parallel: t.Chdir + t.Setenv mutate process globals.
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(dir)
	paths.ClearWorktreeRootCache()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	author := &object.Signature{Name: "t", Email: "t@t", When: time.Now()}

	// Initial commit so HEAD exists.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	initial, err := worktree.Commit("init", &git.CommitOptions{Author: author})
	if err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	// Seed checkpoint metadata: a cursor chat archive under .entire/metadata/<sessionID>.
	sessionID := "2026-04-22-abcdef"
	metadataDir := filepath.Join(dir, entireDir, "metadata", sessionID)
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	originalDB := []byte("opaque store.db bytes roundtrip")
	originalWAL := []byte("wal bytes")
	archive := cursor.ChatArchive{
		Format:     "cursor-chat-export",
		Version:    2,
		AgentID:    sessionID,
		DBBytes:    base64.StdEncoding.EncodeToString(originalDB),
		DBWALBytes: base64.StdEncoding.EncodeToString(originalWAL),
		Transcript: []json.RawMessage{
			json.RawMessage(`{"role":"user","content":"hi"}`),
		},
	}
	blob, err := json.Marshal(archive)
	if err != nil {
		t.Fatal(err)
	}
	archiveRel := filepath.Join(entireDir, "metadata", sessionID, sessionID+".cursor-chat.json")
	if err := os.WriteFile(filepath.Join(dir, archiveRel), blob, 0o600); err != nil {
		t.Fatal(err)
	}

	// Stage and commit the checkpoint tree (simulates a shadow-branch checkpoint).
	if _, err := worktree.Add(archiveRel); err != nil {
		t.Fatalf("add archive: %v", err)
	}
	checkpointMsg := "Checkpoint\n\nEntire-Session: " + sessionID
	checkpointHash, err := worktree.Commit(checkpointMsg, &git.CommitOptions{Author: author})
	if err != nil {
		t.Fatalf("checkpoint commit: %v", err)
	}

	// Save session state for the Cursor agent so Rewind can resolve the restorer.
	s := NewManualCommitStrategy()
	state := &SessionState{
		SessionID:    sessionID,
		BaseCommit:   initial.String(),
		StartedAt:    time.Now(),
		AgentType:    types.AgentType("Cursor"),
		StepCount:    1,
		WorktreePath: dir,
	}
	if err := s.saveSessionState(context.Background(), state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	// Reset working tree back to initial so Rewind has work to do.
	if err := worktree.Reset(&git.ResetOptions{
		Commit: initial,
		Mode:   git.HardReset,
	}); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// Rewind to the checkpoint.
	point := RewindPoint{
		ID:      checkpointHash.String(),
		Message: "Checkpoint",
		Date:    time.Now(),
	}
	if err := s.Rewind(context.Background(), io.Discard, io.Discard, point); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Working directory: assert the archive JSON under .entire/ was NOT restored to
	// the working tree (rewind correctly skips metadata files).
	if _, err := os.Stat(filepath.Join(dir, archiveRel)); !os.IsNotExist(err) {
		t.Errorf("archive JSON should not have been restored to working tree, got %v", err)
	}

	// ~/.cursor: assert store.db + wal written at the expected location.
	wantHash := cursor.WorkspaceHash(dir)
	dbPath := filepath.Join(home, ".cursor", "chats", wantHash, sessionID, "store.db")
	got, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("expected store.db at %s: %v", dbPath, err)
	}
	if string(got) != string(originalDB) {
		t.Errorf("store.db bytes mismatch: got %q, want %q", got, originalDB)
	}
	gotWAL, err := os.ReadFile(dbPath + "-wal")
	if err != nil {
		t.Fatalf("expected store.db-wal at %s: %v", dbPath+"-wal", err)
	}
	if string(gotWAL) != string(originalWAL) {
		t.Errorf("wal bytes mismatch")
	}
}
