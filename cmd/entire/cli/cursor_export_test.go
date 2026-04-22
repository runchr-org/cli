package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
)

const exportTestAgentID = "eeeeeeee-1111-2222-3333-ffffffffffff"

func TestCursorExportCmd_WritesArchiveToDisk(t *testing.T) {
	// Cannot t.Parallel: sets HOME.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dbDir := filepath.Join(tmp, ".cursor", "chats", "hash", exportTestAgentID)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	originalDB := []byte("opaque store.db bytes")
	if err := os.WriteFile(filepath.Join(dbDir, "store.db"), originalDB, 0o600); err != nil {
		t.Fatalf("seed db: %v", err)
	}

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
	if archive.Version != 2 {
		t.Errorf("version = %d", archive.Version)
	}
	if archive.AgentID != exportTestAgentID {
		t.Errorf("agentId = %q", archive.AgentID)
	}
	gotDB, err := base64.StdEncoding.DecodeString(archive.DBBytes)
	if err != nil {
		t.Fatalf("decode db: %v", err)
	}
	if !bytes.Equal(gotDB, originalDB) {
		t.Errorf("db roundtrip: got %q, want %q", gotDB, originalDB)
	}
}
