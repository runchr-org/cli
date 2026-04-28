package cursor

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	testAgentID   = "11111111-2222-3333-4444-555555555555"
	testWorkspace = "abc123hash"
)

// seedStoreDB creates a real cursor-format SQLite store.db under
// $HOME/.cursor/chats/<workspace>/<testAgentID>/ with the given meta + blobs.
// Returns the path to store.db so callers can chmod it for read-only tests.
func seedStoreDB(t *testing.T, home string, metaValue []byte, blobs map[string][]byte) string {
	t.Helper()
	dbDir := filepath.Join(home, ".cursor", "chats", testWorkspace, testAgentID)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}
	dbPath := filepath.Join(dbDir, "store.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE meta  (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE blobs (id  TEXT PRIMARY KEY, data BLOB);
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	if metaValue != nil {
		// Cursor stores meta.value as hex-ASCII of the JSON bytes.
		if _, err := db.ExecContext(context.Background(), "INSERT INTO meta(key, value) VALUES('0', ?)", hex.EncodeToString(metaValue)); err != nil {
			t.Fatalf("insert meta: %v", err)
		}
	}
	for id, data := range blobs {
		if _, err := db.ExecContext(context.Background(), "INSERT INTO blobs(id, data) VALUES(?, ?)", id, data); err != nil {
			t.Fatalf("insert blob %s: %v", id, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	return dbPath
}

// parseArchiveLines reads the JSONL export and splits it into meta + blob rows.
func parseArchiveLines(t *testing.T, data []byte) (metas []jsonlLine, blobs []jsonlLine) {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var l jsonlLine
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			t.Fatalf("unmarshal line %q: %v", scanner.Text(), err)
		}
		switch l.T {
		case "meta":
			metas = append(metas, l)
		case "blob":
			blobs = append(blobs, l)
		default:
			t.Fatalf("unexpected row type %q", l.T)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return metas, blobs
}

func TestExportChatArchive_DumpsMetaAndBlobs(t *testing.T) {
	// Cannot t.Parallel: t.Setenv("HOME") mutates a process global.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	metaJSON := []byte(`{"agentId":"11111111-2222-3333-4444-555555555555","latestRootBlobId":"aaa","name":"Test"}`)
	blobBytes := map[string][]byte{
		"aaa111": []byte(`{"role":"user","content":"hello"}`),
		"bbb222": []byte(`{"role":"assistant","content":"hi"}`),
	}
	seedStoreDB(t, tmp, metaJSON, blobBytes)

	data, err := ExportChatArchive(context.Background(), testAgentID)
	if err != nil {
		t.Fatalf("ExportChatArchive: %v", err)
	}

	metas, blobs := parseArchiveLines(t, data)
	if len(metas) != 1 {
		t.Fatalf("meta rows = %d, want 1", len(metas))
	}
	if metas[0].K != "0" {
		t.Errorf("meta.key = %q, want %q", metas[0].K, "0")
	}
	if !json.Valid(metas[0].V) {
		t.Errorf("meta.v is not valid JSON: %s", metas[0].V)
	}
	if !bytes.Equal([]byte(metas[0].V), metaJSON) {
		t.Errorf("meta.v = %s, want %s", metas[0].V, metaJSON)
	}
	if len(blobs) != len(blobBytes) {
		t.Fatalf("blob rows = %d, want %d", len(blobs), len(blobBytes))
	}
	for _, b := range blobs {
		want, ok := blobBytes[b.ID]
		if !ok {
			t.Errorf("unexpected blob id %q", b.ID)
			continue
		}
		got, err := base64.StdEncoding.DecodeString(b.Data)
		if err != nil {
			t.Errorf("decode blob %s: %v", b.ID, err)
			continue
		}
		// Cursor chat-message blobs go through secret redaction, which round-trips
		// the JSON via Marshal and may reorder keys. Compare structurally instead
		// of byte-equal.
		var gotDoc, wantDoc any
		if uerr := json.Unmarshal(got, &gotDoc); uerr != nil {
			t.Errorf("blob %s decoded bytes are not JSON: %v", b.ID, uerr)
			continue
		}
		if uerr := json.Unmarshal(want, &wantDoc); uerr != nil {
			t.Errorf("blob %s seed bytes are not JSON: %v", b.ID, uerr)
			continue
		}
		if !reflect.DeepEqual(gotDoc, wantDoc) {
			t.Errorf("blob %s roundtrip mismatch:\n got:  %s\n want: %s", b.ID, got, want)
		}
	}
}

func TestExportChatArchive_RedactsBlobContent(t *testing.T) {
	// Cursor chat blobs may carry user-pasted secrets in their content fields.
	// The export pipeline must run them through the same redactor the main
	// transcript uses; cursor.go's redactBlobData targets {role, content} JSON
	// shapes (string and array variants) and leaves merkle tree nodes alone.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// High-entropy token shaped like a typical OpenAI/Anthropic API key — the
	// AKIA-style AWS test key has too little entropy to trip the threshold.
	secret := "sk-proj-aBcDeFgHiJkLmNoPqRsTuVwXyZ1234567890aBcDeFgHiJkLmNoPq"
	stringContent := []byte(`{"role":"user","content":"key=` + secret + `"}`)
	arrayContent := []byte(`{"role":"assistant","content":[{"type":"text","text":"token: ` + secret + `"}]}`)
	// Tool-use shape: secret hides inside a nested input object. A top-level-only
	// redactor would miss this — recursive walk must catch it.
	toolUseContent := []byte(`{"role":"assistant","content":[{"type":"tool_use","name":"Write","input":{"path":"foo.txt","contents":"# api: ` + secret + `"}}]}`)
	// Cursor-wrapped binary frame: real cursor DBs put user prompts inside a
	// protobuf-like envelope. The bytes aren't JSON but the secret is in the
	// clear. Falls through to redact.Bytes byte-level scrubbing.
	wrappedFrame := append([]byte{0x0a, 0x8a, 0x01}, []byte("user input: "+secret+" trailing")...)
	treeNode := []byte(`{"latestRootBlobId":"958e728654e1e5e7b019e874d9045f53ba89b36af97e785f023ea8963e961c45"}`)

	seedStoreDB(t, tmp, []byte(`{"agentId":"x"}`), map[string][]byte{
		"chat-string":    stringContent,
		"chat-array":     arrayContent,
		"chat-tool-use":  toolUseContent,
		"wrapped-binary": wrappedFrame,
		"tree-node":      treeNode,
	})

	data, err := ExportChatArchive(context.Background(), testAgentID)
	if err != nil {
		t.Fatalf("ExportChatArchive: %v", err)
	}
	_, blobs := parseArchiveLines(t, data)

	for _, b := range blobs {
		raw, err := base64.StdEncoding.DecodeString(b.Data)
		if err != nil {
			t.Fatalf("decode %s: %v", b.ID, err)
		}
		if b.ID == "tree-node" {
			if !bytes.Equal(raw, treeNode) {
				t.Errorf("tree node bytes mutated by redaction:\n got:  %s\n want: %s", raw, treeNode)
			}
			continue
		}
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("blob %s leaked secret %q after redaction:\n%s", b.ID, secret, raw)
		}
		if !bytes.Contains(raw, []byte("REDACTED")) {
			t.Errorf("blob %s expected REDACTED placeholder after redaction:\n%s", b.ID, raw)
		}
	}
}

func TestExportChatArchive_BlobsSortedByID(t *testing.T) {
	// Deterministic ordering lets git pack dedup across checkpoints.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	blobBytes := map[string][]byte{
		"ccc": []byte("c"),
		"aaa": []byte("a"),
		"bbb": []byte("b"),
	}
	seedStoreDB(t, tmp, []byte(`{"agentId":"x"}`), blobBytes)

	data, err := ExportChatArchive(context.Background(), testAgentID)
	if err != nil {
		t.Fatalf("ExportChatArchive: %v", err)
	}
	_, blobs := parseArchiveLines(t, data)
	wantOrder := []string{"aaa", "bbb", "ccc"}
	for i, b := range blobs {
		if b.ID != wantOrder[i] {
			t.Errorf("blobs[%d].id = %q, want %q", i, b.ID, wantOrder[i])
		}
	}
}

func TestExportChatArchive_MissingDB_ReturnsErrNotExist(t *testing.T) {
	// When cursor has not created a DB yet, callers want to skip silently
	// rather than fail — errors.Is(err, os.ErrNotExist) is the contract.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	_, err := ExportChatArchive(context.Background(), testAgentID)
	if err == nil {
		t.Fatal("expected error when no store.db exists")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want errors.Is(os.ErrNotExist)", err)
	}
}

func TestExportChatArchive_ReadOnly_DoesNotMutateSource(t *testing.T) {
	// Export copies DB + sidecars to a temp dir before opening, so the
	// cursor-live files on disk must be byte-identical after export.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dbPath := seedStoreDB(t, tmp, []byte(`{"k":"v"}`), map[string][]byte{"id": []byte("data")})

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
