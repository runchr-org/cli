package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
	_ "modernc.org/sqlite"
)

const exportTestAgentID = "eeeeeeee-1111-2222-3333-ffffffffffff"

func TestCursorExportCmd_WritesArchiveToDisk(t *testing.T) {
	// Cannot t.Parallel: sets HOME.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Seed minimal Cursor tree.
	dbDir := filepath.Join(tmp, ".cursor", "chats", "hash", exportTestAgentID)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "store.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT); CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB); INSERT INTO meta VALUES ('k','v');"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	db.Close()

	outputPath := filepath.Join(tmp, "snap.cursor-chat.json")

	cmd := newCursorExportCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{exportTestAgentID, outputPath})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("run export: %v\n%s", err, buf.String())
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var archive cursor.ChatArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		t.Fatalf("parse archive: %v", err)
	}
	if archive.Format != "cursor-chat-export" {
		t.Errorf("format = %q", archive.Format)
	}
	if archive.AgentID != exportTestAgentID {
		t.Errorf("agentId = %q", archive.AgentID)
	}
	if archive.Store.Meta["k"] != "v" {
		t.Errorf("meta[k] = %q, want v", archive.Store.Meta["k"])
	}
}
