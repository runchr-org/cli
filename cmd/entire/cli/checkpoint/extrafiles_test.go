package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/entireio/cli/redact"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// TestCreateRedactedBlob_OpaqueArchive_NotRedacted verifies that files
// recognized as opaque agent archives bypass the redaction pass. Covers both
// the legacy v2 shape (.cursor-chat.json wrapping a base64 SQLite blob) and
// the current v3 shape (cursor-chat.jsonl, per-row JSONL with base64 blob
// payloads). Without the bypass, random byte sequences inside the base64
// payload can match a secret pattern and get replaced with "REDACTED",
// corrupting the archive so its consumer fails to base64-decode the blob.
func TestCreateRedactedBlob_OpaqueArchive_NotRedacted(t *testing.T) {
	t.Parallel()

	// A "secret-looking" token that the redactor would otherwise hit.
	secretLike := "AKIAIOSFODNN7EXAMPLE"
	cases := []struct {
		name     string
		filename string
		treePath string
		content  []byte
	}{
		{
			name:     "v2_single_json",
			filename: "session.cursor-chat.json",
			treePath: "metadata/session.cursor-chat.json",
			content:  []byte(`{"format":"cursor-chat-export","version":2,"db_bytes":"` + secretLike + `"}`),
		},
		{
			name:     "v3_jsonl",
			filename: "cursor-chat.jsonl",
			treePath: "metadata/session/cursor-chat.jsonl",
			content:  []byte(`{"t":"blob","id":"xx","data":"` + secretLike + `"}` + "\n"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tempDir := t.TempDir()
			testutil.InitRepo(t, tempDir)
			repo, err := git.PlainOpen(tempDir)
			if err != nil {
				t.Fatalf("git open: %v", err)
			}
			filePath := filepath.Join(tempDir, tc.filename)
			if err := os.WriteFile(filePath, tc.content, 0o600); err != nil {
				t.Fatal(err)
			}
			hash, _, err := createRedactedBlobFromFile(repo, filePath, tc.treePath)
			if err != nil {
				t.Fatalf("createRedactedBlobFromFile: %v", err)
			}
			blob, err := repo.BlobObject(hash)
			if err != nil {
				t.Fatalf("BlobObject: %v", err)
			}
			reader, err := blob.Reader()
			if err != nil {
				t.Fatalf("blob reader: %v", err)
			}
			defer reader.Close()
			got := make([]byte, blob.Size)
			if _, err := reader.Read(got); err != nil {
				t.Fatalf("read blob: %v", err)
			}
			if string(got) != string(tc.content) {
				t.Errorf("blob contents mutated by redaction:\ngot:  %s\nwant: %s", got, tc.content)
			}
		})
	}
}

// TestWriteCommitted_ReadSessionContent_RoundtripsExtraFiles verifies that
// agent-contributed extra files survive the checkpoint write/read roundtrip
// on the metadata branch. This is the commit→resume path that lets an agent's
// CheckpointRestorer recover its native data (e.g., Cursor's store.db).
func TestWriteCommitted_ReadSessionContent_RoundtripsExtraFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	testutil.InitRepo(t, tempDir)
	repo, err := git.PlainOpen(tempDir)
	if err != nil {
		t.Fatalf("git open: %v", err)
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
