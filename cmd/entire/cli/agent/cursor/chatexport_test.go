package cursor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const (
	testAgentID   = "11111111-2222-3333-4444-555555555555"
	testWorkspace = "abc123hash"
	testSlug      = "abc123hash"
)

type seed struct {
	meta       map[string]string
	blobs      map[string][]byte
	transcript []string
}

// seedCursorTree materializes a fake ~/.cursor tree under home and returns the db path.
// Callers point HOME at this tree via t.Setenv.
func seedCursorTree(t *testing.T, home string, s seed) (dbPath string) {
	t.Helper()

	dbDir := filepath.Join(home, ".cursor", "chats", testWorkspace, testAgentID)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}
	dbPath = filepath.Join(dbDir, "store.db")
	writeFixtureDB(t, dbPath, s.meta, s.blobs)

	if s.transcript != nil {
		txDir := filepath.Join(home, ".cursor", "projects", testSlug, "agent-transcripts")
		if err := os.MkdirAll(txDir, 0o755); err != nil {
			t.Fatalf("mkdir transcripts: %v", err)
		}
		txPath := filepath.Join(txDir, testAgentID+".jsonl")
		f, err := os.Create(txPath)
		if err != nil {
			t.Fatalf("create transcript: %v", err)
		}
		defer f.Close()
		for _, line := range s.transcript {
			if _, err := f.WriteString(line + "\n"); err != nil {
				t.Fatalf("write transcript: %v", err)
			}
		}
	}
	return dbPath
}

func writeFixtureDB(t *testing.T, path string, meta map[string]string, blobs map[string][]byte) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)"); err != nil {
		t.Fatalf("create blobs: %v", err)
	}
	for k, v := range meta {
		if _, err := db.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES (?, ?)", k, v); err != nil {
			t.Fatalf("insert meta: %v", err)
		}
	}
	for id, data := range blobs {
		if _, err := db.ExecContext(ctx, "INSERT INTO blobs (id, data) VALUES (?, ?)", id, data); err != nil {
			t.Fatalf("insert blob: %v", err)
		}
	}
}

func TestExportChatArchive_Roundtrip(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") conflicts with parallel execution.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := seed{
		meta:  map[string]string{"version": "1", "model": "gpt-5"},
		blobs: map[string][]byte{"b1": []byte("hello"), "b2": {0x00, 0xff, 0x42}},
		transcript: []string{
			`{"role":"user","content":"hi"}`,
			`{"role":"assistant","content":"hey"}`,
		},
	}
	seedCursorTree(t, tmp, s)

	data, err := ExportChatArchive(context.Background(), testAgentID)
	if err != nil {
		t.Fatalf("ExportChatArchive: %v", err)
	}

	var got ChatArchive
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal archive: %v", err)
	}

	if got.Format != "cursor-chat-export" {
		t.Fatalf("format = %q, want cursor-chat-export", got.Format)
	}
	if got.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Version)
	}
	if got.AgentID != testAgentID {
		t.Fatalf("agentId mismatch: %q", got.AgentID)
	}
	if len(got.Store.Meta) != len(s.meta) {
		t.Fatalf("meta len = %d, want %d", len(got.Store.Meta), len(s.meta))
	}
	for k, want := range s.meta {
		if got.Store.Meta[k] != want {
			t.Errorf("meta[%q] = %q, want %q", k, got.Store.Meta[k], want)
		}
	}
	for id, want := range s.blobs {
		b64, ok := got.Store.Blobs[id]
		if !ok {
			t.Errorf("blob %q missing", id)
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Errorf("blob %q base64: %v", id, err)
			continue
		}
		if string(decoded) != string(want) {
			t.Errorf("blob %q = %x, want %x", id, decoded, want)
		}
	}
	if len(got.Transcript) != len(s.transcript) {
		t.Fatalf("transcript entries = %d, want %d", len(got.Transcript), len(s.transcript))
	}
}

func TestExportChatArchive_ReadOnly_WithWALSidecar(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") conflicts with parallel execution.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := seed{
		meta:  map[string]string{"k": "v"},
		blobs: map[string][]byte{"b": []byte("x")},
	}
	dbPath := seedCursorTree(t, tmp, s)

	// Simulate Cursor holding the DB open with WAL — create empty sidecar files.
	for _, ext := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(dbPath+ext, []byte{}, 0o600); err != nil {
			t.Fatalf("seed %s: %v", ext, err)
		}
	}

	before, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	if _, err := ExportChatArchive(context.Background(), testAgentID); err != nil {
		t.Fatalf("ExportChatArchive: %v", err)
	}

	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("export mutated the source DB: size %d->%d, mtime %v->%v",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
}

func TestExportChatArchive_ReadOnlyOnReadOnlyFile(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") conflicts with parallel execution.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := seed{
		meta:  map[string]string{"k": "v"},
		blobs: map[string][]byte{"b": []byte("x")},
	}
	dbPath := seedCursorTree(t, tmp, s)

	// Make the DB file read-only (0o400). A read-write open performs an
	// implicit WAL checkpoint/write which would fail here.
	if err := os.Chmod(dbPath, 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Also make parent directory read-only so WAL sidecar creation would fail.
	parent := filepath.Dir(dbPath)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(parent, 0o755); err != nil {
			t.Logf("cleanup chmod: %v", err)
		}
	})

	if _, err := ExportChatArchive(context.Background(), testAgentID); err != nil {
		t.Fatalf("export on read-only DB: %v", err)
	}
}

func TestExportChatArchive_NoTranscript(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") conflicts with parallel execution.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := seed{
		meta:  map[string]string{"k": "v"},
		blobs: map[string][]byte{"b": []byte("x")},
		// no transcript
	}
	seedCursorTree(t, tmp, s)

	data, err := ExportChatArchive(context.Background(), testAgentID)
	if err != nil {
		t.Fatalf("ExportChatArchive: %v", err)
	}
	var got ChatArchive
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Transcript) != 0 {
		t.Errorf("expected no transcript entries, got %d", len(got.Transcript))
	}
	if got.TranscriptPath != "" {
		t.Errorf("expected empty transcript_path, got %q", got.TranscriptPath)
	}
}

func TestExportChatArchive_MissingDB(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") conflicts with parallel execution.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// no seeding — no ~/.cursor/chats exists

	_, err := ExportChatArchive(context.Background(), testAgentID)
	if err == nil {
		t.Fatal("expected error when no store.db exists, got nil")
	}
}
