package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const (
	sessA = "10000000-0000-0000-0000-00000000000a"
	sessB = "20000000-0000-0000-0000-00000000000b"
)

// seedImportedSession creates a minimal valid Cursor session tree under home.
func seedImportedSession(t *testing.T, home, workspaceHash, slug, agentID string, transcriptLines int) {
	t.Helper()
	dbDir := filepath.Join(home, ".cursor", "chats", workspaceHash, agentID)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(dbDir, "store.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)"); err != nil {
		t.Fatalf("create blobs: %v", err)
	}
	db.Close()

	if transcriptLines > 0 {
		txDir := filepath.Join(home, ".cursor", "projects", slug, "agent-transcripts")
		if err := os.MkdirAll(txDir, 0o755); err != nil {
			t.Fatalf("mkdir tx: %v", err)
		}
		f, err := os.Create(filepath.Join(txDir, agentID+".jsonl"))
		if err != nil {
			t.Fatalf("create tx: %v", err)
		}
		for range transcriptLines {
			if _, err := f.WriteString(`{"role":"user","content":"x"}` + "\n"); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		f.Close()
	}
}

func runSessionsCmd(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newCursorSessionsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("cursor-sessions: %v", err)
	}
	return buf.String()
}

func TestCursorSessions_JSON_ListsAllDiscovered(t *testing.T) {
	// Cannot t.Parallel: sets HOME.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	seedImportedSession(t, tmp, "hash1", "slug1", sessA, 3)
	seedImportedSession(t, tmp, "hash2", "slug2", sessB, 0) // no transcript

	out := runSessionsCmd(t, "--json")

	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parse JSON output: %v\n%s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 sessions, got %d: %s", len(entries), out)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		id, ok := e["agent_id"].(string)
		if !ok {
			t.Fatalf("entry missing agent_id: %v", e)
		}
		seen[id] = true
	}
	if !seen[sessA] || !seen[sessB] {
		t.Errorf("missing sessions: %v", seen)
	}
}

func TestCursorSessions_JSON_IncludesTranscriptLineCount(t *testing.T) {
	// Cannot t.Parallel: sets HOME.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	seedImportedSession(t, tmp, "hash1", "slug1", sessA, 4)

	out := runSessionsCmd(t, "--json")
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 session, got %d", len(entries))
	}
	lines, ok := entries[0]["transcript_lines"].(float64)
	if !ok {
		t.Fatalf("transcript_lines missing or wrong type: %v", entries[0])
	}
	if int(lines) != 4 {
		t.Errorf("transcript_lines = %d, want 4", int(lines))
	}
}

func TestCursorSessions_Empty(t *testing.T) {
	// Cannot t.Parallel: sets HOME.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	out := runSessionsCmd(t, "--json")
	// Expect valid JSON: either [] or null.
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(entries) != 0 {
		t.Errorf("want empty, got %d entries", len(entries))
	}
}
