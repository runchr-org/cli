package cursor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/entireio/cli/redact"

	_ "modernc.org/sqlite" // pure-Go SQLite driver registered as "sqlite"
)

// Archive v3 ships cursor's `store.db` as a JSONL dump of its two tables
// (meta + blobs). Each line is a JSON object carrying one row. Restore
// replays the rows into a fresh SQLite file, so cursor opens the same
// content-addressable DAG it wrote. Plain-text content gives git's pack
// compression something to chew on and keeps per-checkpoint size down
// to a few kB versus ~340 kB for the v2 opaque-base64 format.
const (
	archiveFormat = "cursor-chat-export"

	// ChatArchiveFilename is the filename used inside a session's metadata
	// directory to carry cursor's JSONL dump. A single archive belongs to
	// exactly one session, so we don't repeat the session id in the name.
	ChatArchiveFilename = "cursor-chat.jsonl"

	// ChatArchiveFilenameV2 is the legacy opaque-base64 format still readable
	// on restore (the metadata branch may contain pre-v3 checkpoints until
	// they age out).
	ChatArchiveFilenameV2 = ".cursor-chat.json"
)

// ChatArchive is the v2 archive shape kept around for backward-compatible
// reads of already-pushed checkpoints. New checkpoints use JSONL (v3).
//
// DBPath and TranscriptPath were absolute filesystem paths captured at v2
// export time. v3 export does not populate them (the format is JSONL, not
// this struct). Old archives on the metadata branch may still carry those
// paths until they age out — restore code reads them but never writes them.
type ChatArchive struct {
	Format         string            `json:"format"`
	Version        int               `json:"version"`
	AgentID        string            `json:"agentId"`
	DBPath         string            `json:"db_path,omitempty"`         // legacy v2; never set by v3.
	DBBytes        string            `json:"db_bytes,omitempty"`        // legacy v2; never set by v3.
	DBWALBytes     string            `json:"db_wal_bytes,omitempty"`    // legacy v2; never set by v3.
	DBSHMBytes     string            `json:"db_shm_bytes,omitempty"`    // legacy v2; never set by v3.
	TranscriptPath string            `json:"transcript_path,omitempty"` // legacy v2; never set by v3.
	Transcript     []json.RawMessage `json:"transcript,omitempty"`      // legacy v2; never set by v3.
}

// jsonlLine is one row in the v3 archive. `t` discriminates meta vs blob;
// other fields apply conditionally. Stored sorted by (t, id) so git pack
// deltas across checkpoints capture only the newly added blob rows.
type jsonlLine struct {
	T    string          `json:"t"`
	K    string          `json:"k,omitempty"`
	V    json.RawMessage `json:"v,omitempty"`
	ID   string          `json:"id,omitempty"`
	Data string          `json:"data,omitempty"`
}

// ExportChatArchive reads the live cursor store.db for agentID, absorbs any
// pending WAL frames, and returns a JSONL dump of its meta + blobs tables.
// Returns (nil, os.ErrNotExist) if cursor has no DB yet for this agent.
func ExportChatArchive(ctx context.Context, agentID string) ([]byte, error) {
	dbPath, err := findStoreDB(agentID)
	if err != nil {
		return nil, err
	}

	workDir, err := os.MkdirTemp("", "entire-cursor-export-*")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	stagedPath := filepath.Join(workDir, "store.db")
	if err := copyDBWithSidecars(dbPath, stagedPath); err != nil {
		return nil, fmt.Errorf("staging store.db: %w", err)
	}

	return dumpJSONL(ctx, stagedPath)
}

// findStoreDB searches ~/.cursor/chats/*/<agentID>/store.db. Returns
// os.ErrNotExist (wrapped) if cursor hasn't created a DB yet.
func findStoreDB(agentID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	chatsDir := filepath.Join(home, ".cursor", "chats")
	dbPath, err := findOne(filepath.Join(chatsDir, "*", agentID, "store.db"))
	if err != nil {
		return "", fmt.Errorf("glob store.db: %w", err)
	}
	if dbPath == "" {
		return "", fmt.Errorf("no store.db for agent %q: %w", agentID, os.ErrNotExist)
	}
	return dbPath, nil
}

// copyDBWithSidecars copies store.db plus its -wal and -shm sidecars into
// dst (dst is the target store.db path). Sidecars get the same stem. The
// copy lets us checkpoint WAL without touching cursor's live files, which
// cursor may still have open.
func copyDBWithSidecars(src, dst string) error {
	if err := copyFile(src, dst); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := copyFile(src+suffix, dst+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("copying %s: %w", suffix, err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	// Real byte copy, not a hardlink — sqlite's wal_checkpoint(TRUNCATE) and any
	// other writes during dump must not touch cursor's live store.db/-wal/-shm.
	in, err := os.Open(src) //nolint:gosec // cursor-managed path
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // dst is our own temp path
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying bytes: %w", err)
	}
	return nil
}

// dumpJSONL opens dbPath (a copy), runs wal_checkpoint, and emits JSONL.
func dumpJSONL(ctx context.Context, dbPath string) ([]byte, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	defer db.Close()

	// Consolidate any pending WAL frames into the main DB so our SELECTs see
	// the latest state. TRUNCATE resets the WAL file to size 0 on disk; we
	// discard the copy anyway, but the PRAGMA also makes the DB reads cheaper.
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		return nil, fmt.Errorf("wal_checkpoint: %w", err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)

	if err := writeMetaLines(ctx, db, enc); err != nil {
		return nil, err
	}
	if err := writeBlobLines(ctx, db, enc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeMetaLines(ctx context.Context, db *sql.DB, enc *json.Encoder) error {
	rows, err := db.QueryContext(ctx, "SELECT key, value FROM meta ORDER BY key")
	if err != nil {
		return fmt.Errorf("select meta: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("scan meta: %w", err)
		}
		// Cursor stores meta.value as hex-encoded ASCII JSON. Decode so the
		// archive is human-readable and redactor-friendly; restore re-encodes.
		decoded, err := hex.DecodeString(value)
		if err != nil {
			return fmt.Errorf("hex-decode meta[%s]: %w", key, err)
		}
		if !json.Valid(decoded) {
			return fmt.Errorf("meta[%s] value is not valid JSON after hex decode", key)
		}
		if err := enc.Encode(jsonlLine{T: "meta", K: key, V: json.RawMessage(decoded)}); err != nil {
			return fmt.Errorf("encode meta line: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate meta: %w", err)
	}
	return nil
}

func writeBlobLines(ctx context.Context, db *sql.DB, enc *json.Encoder) error {
	// Pull rows and sort in Go by id — gives deterministic output regardless
	// of sqlite row ordering and helps git pack dedupe across checkpoints.
	rows, err := db.QueryContext(ctx, "SELECT id, data FROM blobs")
	if err != nil {
		return fmt.Errorf("select blobs: %w", err)
	}
	defer rows.Close()

	type blobRow struct {
		id   string
		data []byte
	}
	var collected []blobRow
	for rows.Next() {
		var r blobRow
		if err := rows.Scan(&r.id, &r.data); err != nil {
			return fmt.Errorf("scan blob: %w", err)
		}
		collected = append(collected, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate blobs: %w", err)
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].id < collected[j].id })
	for _, r := range collected {
		redacted := redactBlobData(r.data)
		if err := enc.Encode(jsonlLine{
			T:    "blob",
			ID:   r.id,
			Data: base64.StdEncoding.EncodeToString(redacted),
		}); err != nil {
			return fmt.Errorf("encode blob line: %w", err)
		}
	}
	return nil
}

// redactBlobData strips secrets out of cursor chat-message blobs before they
// go into a checkpoint. Cursor's blobs come in three shapes:
//
//  1. {"role": "...", "content": "<text>"} — user/system messages.
//  2. {"role": "assistant", "content": [{"type":"text","text":"..."}, ...]} — typed parts.
//  3. Merkle tree nodes — JSON metadata referencing child blob IDs.
//
// Shapes (1) and (2) carry user prompts and assistant replies that may
// include API keys, credentials, etc. that the user pasted into the chat.
// We run those text fields through the same redactor the main transcript
// uses. Shape (3) is left untouched: blob-id refs are content-addressed
// SHA256 hashes that must roundtrip exactly to keep the DAG intact.
//
// If the blob isn't valid JSON (cursor occasionally wraps assistant frames
// in a binary envelope around an inner JSON message) we return the bytes
// unchanged. The opaque-archive redaction bypass in the checkpoint walker
// already keeps git from corrupting these on commit; we just don't get
// chat-text redaction for those frames, which mirrors the v2 behavior.
func redactBlobData(data []byte) []byte {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return data
	}
	if _, hasRole := doc["role"]; !hasRole {
		return data
	}
	switch content := doc["content"].(type) {
	case string:
		doc["content"] = redact.String(content)
	case []any:
		for i, item := range content {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, isStr := obj["text"].(string); isStr {
				obj["text"] = redact.String(text)
				content[i] = obj
			}
		}
	default:
		return data
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return data
	}
	return out
}

// findOne returns the first glob match, or "" if none matched.
func findOne(pattern string) (string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob %s: %w", pattern, err)
	}
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0], nil
}

// ReadTranscriptFile reads a JSONL transcript file and returns parsed entries.
// Kept exported for any legacy caller; the v3 archive no longer bundles a transcript.
func ReadTranscriptFile(path string) ([]json.RawMessage, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from known cursor dirs
	if err != nil {
		return nil, fmt.Errorf("reading transcript file: %w", err)
	}
	var entries []json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decoding transcript line: %w", err)
		}
		entries = append(entries, raw)
	}
	return entries, nil
}
