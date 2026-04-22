package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
	_ "modernc.org/sqlite"
)

const (
	testImportAgentID   = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testImportWorkspace = "workspacehash0123"
	testImportModel     = "gpt-5"
)

func importFixtureArchive() cursor.ChatArchive {
	return cursor.ChatArchive{
		Format:  "cursor-chat-export",
		Version: 1,
		AgentID: testImportAgentID,
		Store: cursor.StoreData{
			Meta: map[string]string{"version": "1", "model": testImportModel},
			Blobs: map[string]string{
				"b1": base64.StdEncoding.EncodeToString([]byte("hello")),
				"b2": base64.StdEncoding.EncodeToString([]byte{0x00, 0xff, 0x42}),
			},
		},
		Transcript: []json.RawMessage{
			json.RawMessage(`{"role":"user","content":"hi"}`),
			json.RawMessage(`{"role":"assistant","content":"hey"}`),
		},
	}
}

// runImport writes the archive to a temp file and invokes the cursor-import command.
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

func readImportedDB(t *testing.T, path string) (map[string]string, map[string][]byte) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	meta := map[string]string{}
	rows, err := db.QueryContext(context.Background(), "SELECT key, value FROM meta")
	if err != nil {
		t.Fatalf("query meta: %v", err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan meta: %v", err)
		}
		meta[k] = v
	}
	rows.Close()

	blobs := map[string][]byte{}
	rows, err = db.QueryContext(context.Background(), "SELECT id, data FROM blobs")
	if err != nil {
		t.Fatalf("query blobs: %v", err)
	}
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			t.Fatalf("scan blob: %v", err)
		}
		blobs[id] = data
	}
	rows.Close()
	return meta, blobs
}

func TestCursorImport_Roundtrip(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd for the command.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Chdir(tmp)

	archive := importFixtureArchive()
	err := runImport(t, archive, "--workspace-hash", testImportWorkspace)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	dbPath := filepath.Join(tmp, ".cursor", "chats", testImportWorkspace, testImportAgentID, "store.db")
	gotMeta, gotBlobs := readImportedDB(t, dbPath)
	if gotMeta["model"] != testImportModel {
		t.Errorf("meta model = %q, want gpt-5", gotMeta["model"])
	}
	if string(gotBlobs["b1"]) != "hello" {
		t.Errorf("blob b1 = %q, want hello", gotBlobs["b1"])
	}
	if !bytes.Equal(gotBlobs["b2"], []byte{0x00, 0xff, 0x42}) {
		t.Errorf("blob b2 bytes mismatch: %x", gotBlobs["b2"])
	}
}

func TestCursorImport_RefusesOverwriteWithoutForce(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Chdir(tmp)

	archive := importFixtureArchive()

	// First import succeeds.
	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace); err != nil {
		t.Fatalf("first import: %v", err)
	}

	dbPath := filepath.Join(tmp, ".cursor", "chats", testImportWorkspace, testImportAgentID, "store.db")
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	// Mutate the archive so a successful overwrite would be observable.
	archive.Store.Meta["model"] = "different"

	// Second import without --force must fail and leave the existing DB untouched.
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

	archive.Store.Meta["model"] = "swapped"
	if err := runImport(t, archive, "--workspace-hash", testImportWorkspace, "--force"); err != nil {
		t.Fatalf("forced import: %v", err)
	}

	dbPath := filepath.Join(tmp, ".cursor", "chats", testImportWorkspace, testImportAgentID, "store.db")
	gotMeta, _ := readImportedDB(t, dbPath)
	if gotMeta["model"] != "swapped" {
		t.Errorf("after --force, meta model = %q, want swapped", gotMeta["model"])
	}
}

func TestCursorImport_CrossMachineNoFlags(t *testing.T) {
	// Cannot t.Parallel: sets HOME and cwd.
	// Simulates: export on machine A at /path-A, import on machine B at /path-B,
	// different absolute paths. No --workspace-hash flag.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Different repo root than the original export site.
	repoB := filepath.Join(tmp, "different", "path", "repo-b")
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatalf("mkdir repo-b: %v", err)
	}
	t.Chdir(repoB)

	archive := importFixtureArchive()
	if err := runImport(t, archive); err != nil {
		t.Fatalf("import without flags: %v", err)
	}

	// Computed destination should be hash of repo-B.
	expectedHash := cursorWorkspaceHash(repoB)
	dbPath := filepath.Join(tmp, ".cursor", "chats", expectedHash, testImportAgentID, "store.db")
	meta, _ := readImportedDB(t, dbPath)
	if meta["model"] != testImportModel {
		t.Errorf("meta model = %q, want gpt-5", meta["model"])
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
		t.Fatal("expected error on wrong Format, got nil")
	}
}
