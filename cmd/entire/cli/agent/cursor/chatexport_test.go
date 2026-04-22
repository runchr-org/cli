package cursor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	testAgentID   = "11111111-2222-3333-4444-555555555555"
	testWorkspace = "abc123hash"
	testSlug      = "abc123hash"
)

type seed struct {
	db         []byte
	wal        []byte // optional
	shm        []byte // optional
	transcript []string
}

// seedCursorTree writes a fake ~/.cursor tree under home and returns the db path.
// Production code treats store.db as an opaque blob, so any bytes work.
func seedCursorTree(t *testing.T, home string, s seed) string {
	t.Helper()
	dbDir := filepath.Join(home, ".cursor", "chats", testWorkspace, testAgentID)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}
	dbPath := filepath.Join(dbDir, "store.db")
	if err := os.WriteFile(dbPath, s.db, 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if s.wal != nil {
		if err := os.WriteFile(dbPath+"-wal", s.wal, 0o600); err != nil {
			t.Fatalf("write wal: %v", err)
		}
	}
	if s.shm != nil {
		if err := os.WriteFile(dbPath+"-shm", s.shm, 0o600); err != nil {
			t.Fatalf("write shm: %v", err)
		}
	}

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

func decodeB64(t *testing.T, s string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	return data
}

func TestExportChatArchive_Roundtrip(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") conflicts with parallel execution.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := seed{
		db:  []byte("SQLite format 3\x00\x01\x02\x03not really a db"),
		wal: []byte("wal sidecar bytes"),
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
		t.Fatalf("format = %q", got.Format)
	}
	if got.Version != 2 {
		t.Fatalf("version = %d, want 2", got.Version)
	}
	if got.AgentID != testAgentID {
		t.Fatalf("agentId = %q", got.AgentID)
	}
	if gotDB := decodeB64(t, got.DBBytes); string(gotDB) != string(s.db) {
		t.Errorf("db roundtrip mismatch: got %q, want %q", gotDB, s.db)
	}
	if gotWAL := decodeB64(t, got.DBWALBytes); string(gotWAL) != string(s.wal) {
		t.Errorf("wal roundtrip mismatch")
	}
	if got.DBSHMBytes != "" {
		t.Errorf("expected no SHM in archive, got %q", got.DBSHMBytes)
	}
	if len(got.Transcript) != len(s.transcript) {
		t.Fatalf("transcript entries = %d, want %d", len(got.Transcript), len(s.transcript))
	}
}

func TestExportChatArchive_ReadOnly_DoesNotMutateSource(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") conflicts with parallel execution.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := seed{
		db:  []byte("db"),
		wal: []byte("wal"),
		shm: []byte("shm"),
	}
	dbPath := seedCursorTree(t, tmp, s)

	before := statAll(t, dbPath)

	if _, err := ExportChatArchive(context.Background(), testAgentID); err != nil {
		t.Fatalf("ExportChatArchive: %v", err)
	}

	after := statAll(t, dbPath)

	for k, b := range before {
		a := after[k]
		if !b.ModTime().Equal(a.ModTime()) {
			t.Errorf("%s modtime changed", k)
		}
		if b.Size() != a.Size() {
			t.Errorf("%s size changed: %d -> %d", k, b.Size(), a.Size())
		}
	}
}

// statAll stats store.db plus its -wal and -shm sidecars.
// Any file that does not exist is omitted from the returned map.
func statAll(t *testing.T, dbPath string) map[string]os.FileInfo {
	t.Helper()
	out := map[string]os.FileInfo{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(dbPath + suffix)
		if err != nil {
			continue
		}
		out["store.db"+suffix] = info
	}
	return out
}

func TestExportChatArchive_ReadOnlyFile(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") conflicts with parallel execution.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := seed{db: []byte("db")}
	dbPath := seedCursorTree(t, tmp, s)

	if err := os.Chmod(dbPath, 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
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

	seedCursorTree(t, tmp, seed{db: []byte("db")})

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

	_, err := ExportChatArchive(context.Background(), testAgentID)
	if err == nil {
		t.Fatal("expected error when no store.db exists")
	}
}
