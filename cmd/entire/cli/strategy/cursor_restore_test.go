package strategy

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
	_ "modernc.org/sqlite"
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

	testutil.InitRepo(t, dir)
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("git open: %v", err)
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

	// Cursor itself resolves symlinks when computing the workspace hash/slug
	// (e.g. on macOS /var/folders → /private/var/folders). Match that so test
	// assertions align with what RestoreCheckpointFiles wrote.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}

	// ~/.cursor/chats: assert store.db + wal written at the expected md5-hashed dir.
	wantHash := cursor.WorkspaceHash(resolvedDir)
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

	// ~/.cursor/projects: assert transcript JSONL written at the slug/nested path.
	wantSlug := cursor.WorkspaceSlug(resolvedDir)
	txPath := filepath.Join(home, ".cursor", "projects", wantSlug, "agent-transcripts", sessionID, sessionID+".jsonl")
	txBytes, err := os.ReadFile(txPath)
	if err != nil {
		t.Fatalf("expected transcript at %s: %v", txPath, err)
	}
	if !strings.Contains(string(txBytes), `"role":"user"`) {
		t.Errorf("transcript content mismatch: got %q", txBytes)
	}
}

// TestRewind_RestoresCursorChatArchiveV3 seeds a checkpoint with a v3 JSONL
// archive (cursor-chat.jsonl) and asserts Rewind rebuilds store.db with the
// same meta + blobs rows the source dumped.
func TestRewind_RestoresCursorChatArchiveV3(t *testing.T) {
	// Cannot t.Parallel: t.Chdir + t.Setenv mutate process globals.
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(dir)
	paths.ClearWorktreeRootCache()

	testutil.InitRepo(t, dir)
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("git open: %v", err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	author := &object.Signature{Name: "t", Email: "t@t", When: time.Now()}

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

	// Seed v3 archive. Use distinctive bytes so we can prove roundtrip.
	sessionID := "2026-04-24-v3session"
	metaJSON := []byte(`{"agentId":"` + sessionID + `","latestRootBlobId":"root","name":"V3"}`)
	blobContents := map[string][]byte{
		"blob-a": []byte(`{"role":"user","content":"first"}`),
		"blob-b": []byte(`{"role":"assistant","content":"second"}`),
	}

	var jsonlBuf strings.Builder
	metaLine, err := json.Marshal(map[string]any{
		"t": "meta",
		"k": "0",
		"v": json.RawMessage(metaJSON),
	})
	if err != nil {
		t.Fatalf("marshal meta line: %v", err)
	}
	jsonlBuf.Write(metaLine)
	jsonlBuf.WriteByte('\n')
	for _, id := range []string{"blob-a", "blob-b"} {
		line, err := json.Marshal(map[string]any{
			"t":    "blob",
			"id":   id,
			"data": base64.StdEncoding.EncodeToString(blobContents[id]),
		})
		if err != nil {
			t.Fatalf("marshal blob line: %v", err)
		}
		jsonlBuf.Write(line)
		jsonlBuf.WriteByte('\n')
	}

	metadataDir := filepath.Join(dir, entireDir, "metadata", sessionID)
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	archiveRel := filepath.Join(entireDir, "metadata", sessionID, "cursor-chat.jsonl")
	if err := os.WriteFile(filepath.Join(dir, archiveRel), []byte(jsonlBuf.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := worktree.Add(archiveRel); err != nil {
		t.Fatalf("add archive: %v", err)
	}
	checkpointHash, err := worktree.Commit(
		"Checkpoint\n\nEntire-Session: "+sessionID,
		&git.CommitOptions{Author: author},
	)
	if err != nil {
		t.Fatalf("checkpoint commit: %v", err)
	}

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

	if err := worktree.Reset(&git.ResetOptions{Commit: initial, Mode: git.HardReset}); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if err := s.Rewind(context.Background(), io.Discard, io.Discard, RewindPoint{
		ID:      checkpointHash.String(),
		Message: "Checkpoint",
		Date:    time.Now(),
	}); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	wantHash := cursor.WorkspaceHash(resolvedDir)
	dbPath := filepath.Join(home, ".cursor", "chats", wantHash, sessionID, "store.db")

	// Open the reconstructed DB and assert both tables match the source.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer db.Close()

	var metaHex string
	if err := db.QueryRowContext(context.Background(), "SELECT value FROM meta WHERE key = '0'").Scan(&metaHex); err != nil {
		t.Fatalf("select meta: %v", err)
	}
	metaBytes, err := hex.DecodeString(metaHex)
	if err != nil {
		t.Fatalf("hex decode meta: %v", err)
	}
	if !strings.Contains(string(metaBytes), `"name":"V3"`) {
		t.Errorf("meta roundtrip: got %s, want value containing \"name\":\"V3\"", metaBytes)
	}

	rows, err := db.QueryContext(context.Background(), "SELECT id, data FROM blobs ORDER BY id")
	if err != nil {
		t.Fatalf("select blobs: %v", err)
	}
	defer rows.Close()
	got := map[string][]byte{}
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			t.Fatalf("scan blob: %v", err)
		}
		got[id] = data
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("blobs iter: %v", err)
	}
	for id, want := range blobContents {
		have, ok := got[id]
		if !ok {
			t.Errorf("blob %q missing from restored db", id)
			continue
		}
		if string(have) != string(want) {
			t.Errorf("blob %q roundtrip mismatch: got %q want %q", id, have, want)
		}
	}
	if len(got) != len(blobContents) {
		t.Errorf("restored blob count = %d, want %d", len(got), len(blobContents))
	}
}
