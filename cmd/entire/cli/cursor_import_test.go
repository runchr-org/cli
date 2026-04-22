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

const (
	testImportAgentID   = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testImportWorkspace = "workspacehash0123"
)

func importFixtureArchive() cursor.ChatArchive {
	return cursor.ChatArchive{
		Format:     "cursor-chat-export",
		Version:    2,
		AgentID:    testImportAgentID,
		DBBytes:    base64.StdEncoding.EncodeToString([]byte("db-bytes-v1")),
		DBWALBytes: base64.StdEncoding.EncodeToString([]byte("wal-bytes")),
		Transcript: []json.RawMessage{
			json.RawMessage(`{"role":"user","content":"hi"}`),
			json.RawMessage(`{"role":"assistant","content":"hey"}`),
		},
	}
}

func runImport(t *testing.T, archive cursor.ChatArchive, args ...string) error {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "snap.cursor-chat.json")
	blob, err := json.Marshal(archive)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(archivePath, blob, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	cmd := newCursorImportCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{archivePath}, args...))
	return cmd.ExecuteContext(context.Background())
}

func TestCursorImport_RoundtripBytes(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Chdir(tmp)

	archive := importFixtureArchive()
	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace); err != nil {
		t.Fatalf("import: %v", err)
	}

	dbPath := filepath.Join(tmp, ".cursor", "chats", testImportWorkspace, testImportAgentID, "store.db")
	got, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if string(got) != "db-bytes-v1" {
		t.Errorf("db bytes = %q, want db-bytes-v1", got)
	}

	wal, err := os.ReadFile(dbPath + "-wal")
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	if string(wal) != "wal-bytes" {
		t.Errorf("wal bytes = %q, want wal-bytes", wal)
	}
}

func TestCursorImport_RefusesOverwriteWithoutForce(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Chdir(tmp)

	archive := importFixtureArchive()
	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace); err != nil {
		t.Fatalf("first import: %v", err)
	}

	dbPath := filepath.Join(tmp, ".cursor", "chats", testImportWorkspace, testImportAgentID, "store.db")
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	archive.DBBytes = base64.StdEncoding.EncodeToString([]byte("different-bytes"))
	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace); err == nil {
		t.Fatal("expected error on overwrite without --force")
	}

	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("DB file was mutated when --force not passed")
	}
}

func TestCursorImport_ForceOverwriteReplacesContents(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Chdir(tmp)

	archive := importFixtureArchive()
	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace); err != nil {
		t.Fatalf("first import: %v", err)
	}

	archive.DBBytes = base64.StdEncoding.EncodeToString([]byte("swapped"))
	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace, "--force"); err != nil {
		t.Fatalf("forced import: %v", err)
	}

	dbPath := filepath.Join(tmp, ".cursor", "chats", testImportWorkspace, testImportAgentID, "store.db")
	got, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if string(got) != "swapped" {
		t.Errorf("after --force, db = %q, want swapped", got)
	}
}

func TestCursorImport_CrossMachineNoFlags(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	repoB := filepath.Join(tmp, "different", "path", "repo-b")
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatalf("mkdir repo-b: %v", err)
	}
	t.Chdir(repoB)

	archive := importFixtureArchive()
	if err := runImport(t, archive); err != nil {
		t.Fatalf("import without flags: %v", err)
	}

	expectedHash := cursorWorkspaceHash(repoB)
	dbPath := filepath.Join(tmp, ".cursor", "chats", expectedHash, testImportAgentID, "store.db")
	got, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if string(got) != "db-bytes-v1" {
		t.Errorf("db = %q, want db-bytes-v1", got)
	}
}

func TestCursorImport_RejectsUnknownFormat(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Chdir(tmp)

	archive := importFixtureArchive()
	archive.Format = "some-other-format"

	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace); err == nil {
		t.Fatal("expected error on wrong Format")
	}
}

func TestCursorImport_RejectsOldVersion(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Chdir(tmp)

	archive := importFixtureArchive()
	archive.Version = 1

	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace); err == nil {
		t.Fatal("expected error on unsupported version")
	}
}
