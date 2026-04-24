package cursor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // matches Cursor's directory naming convention, not security
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceHash returns the Cursor workspace hash for a project path.
// Cursor stores per-project chat DBs under MD5(absolute_project_path).
func WorkspaceHash(projectPath string) string {
	sum := md5.Sum([]byte(projectPath)) //nolint:gosec // matches Cursor's convention
	return hex.EncodeToString(sum[:])
}

// WorkspaceSlug returns the Cursor project slug for a project path.
// Cursor stores per-project transcript JSONLs under a path-flattened slug
// (leading slash stripped, remaining slashes replaced with dashes), not MD5.
// For example: /private/tmp/foo -> private-tmp-foo.
func WorkspaceSlug(projectPath string) string {
	return strings.ReplaceAll(strings.TrimPrefix(projectPath, "/"), "/", "-")
}

// RestoreCheckpointFiles rebuilds cursor's store.db for this session from the
// checkpoint's agent-contributed extras. Supports two archive shapes:
//
//   - v3 (current): cursor-chat.jsonl — JSONL dump of meta + blobs tables.
//     Restore replays the rows into a fresh SQLite file.
//   - v2 (legacy, still present on already-pushed checkpoints):
//     <sessionID>.cursor-chat.json — opaque base64 of store.db + WAL + SHM.
//
// The workspace hash is computed from the current working directory,
// matching Cursor's own MD5(project_path) convention — run rewind/resume
// from the repo that originally hosted the session.
func (c *CursorAgent) RestoreCheckpointFiles(ctx context.Context, sessionID string, files map[string][]byte) error {
	target, err := cursorTargetPaths(sessionID)
	if err != nil {
		return err
	}

	if data, ok := files[ChatArchiveFilename]; ok {
		return restoreV3(ctx, target, data)
	}
	if data, ok := files[sessionID+ChatArchiveFilenameV2]; ok {
		return restoreV2(target, data)
	}
	return nil
}

// cursorTarget holds the paths we write into for a given session.
type cursorTarget struct {
	agentID   string
	storeDB   string // ~/.cursor/chats/<hash>/<agent-id>/store.db
	nestedJSO string // ~/.cursor/projects/<slug>/agent-transcripts/<agent-id>/<agent-id>.jsonl
}

// cursorTargetPaths resolves cursor's on-disk layout for the current cwd.
// Cursor resolves symlinks when computing its workspace hash (e.g. /tmp → /private/tmp
// on macOS), so we match that before hashing/slugifying.
func cursorTargetPaths(sessionID string) (cursorTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return cursorTarget{}, fmt.Errorf("home directory: %w", err)
	}
	cwd, err := os.Getwd() //nolint:forbidigo // Cursor's MD5(project_path) uses cwd, not git root
	if err != nil {
		return cursorTarget{}, fmt.Errorf("current directory: %w", err)
	}
	if resolved, evalErr := filepath.EvalSymlinks(cwd); evalErr == nil {
		cwd = resolved
	}
	hash := WorkspaceHash(cwd)
	slug := WorkspaceSlug(cwd)
	return cursorTarget{
		agentID:   sessionID,
		storeDB:   filepath.Join(home, ".cursor", "chats", hash, sessionID, "store.db"),
		nestedJSO: filepath.Join(home, ".cursor", "projects", slug, "agent-transcripts", sessionID, sessionID+".jsonl"),
	}, nil
}

// restoreV3 rebuilds store.db from a JSONL dump.
func restoreV3(ctx context.Context, t cursorTarget, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(t.storeDB), 0o750); err != nil {
		return fmt.Errorf("chats dir: %w", err)
	}
	// Remove any prior DB + sidecars so sqlite creates a fresh file with no
	// WAL state bleeding through from a stale session.
	for _, p := range []string{t.storeDB, t.storeDB + "-wal", t.storeDB + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clearing %s: %w", filepath.Base(p), err)
		}
	}

	db, err := sql.Open("sqlite", t.storeDB)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE meta  (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE blobs (id  TEXT PRIMARY KEY, data BLOB);
	`); err != nil {
		return fmt.Errorf("create tables: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Commit() supersedes

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Blobs can be larger than bufio.Scanner's default 64 KB line cap.
	// SQLite row size is effectively unbounded for our inputs; 8 MB is
	// comfortably above any single cursor blob we expect.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row jsonlLine
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("parse line %d: %w", lineNum, err)
		}
		switch row.T {
		case "meta":
			if err := insertMetaRow(ctx, tx, row); err != nil {
				return fmt.Errorf("line %d: %w", lineNum, err)
			}
		case "blob":
			if err := insertBlobRow(ctx, tx, row); err != nil {
				return fmt.Errorf("line %d: %w", lineNum, err)
			}
		default:
			return fmt.Errorf("line %d: unknown row type %q", lineNum, row.T)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func insertMetaRow(ctx context.Context, tx *sql.Tx, row jsonlLine) error {
	// Cursor's meta.value is stored as hex-ASCII of the JSON bytes; preserve
	// that shape so cursor reads the same format it wrote.
	if len(row.V) == 0 {
		return errors.New("meta row missing v")
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO meta(key, value) VALUES(?, ?)",
		row.K, hex.EncodeToString(row.V),
	); err != nil {
		return fmt.Errorf("insert meta: %w", err)
	}
	return nil
}

func insertBlobRow(ctx context.Context, tx *sql.Tx, row jsonlLine) error {
	raw, err := base64.StdEncoding.DecodeString(row.Data)
	if err != nil {
		return fmt.Errorf("decode blob data: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO blobs(id, data) VALUES(?, ?)",
		row.ID, raw,
	); err != nil {
		return fmt.Errorf("insert blob: %w", err)
	}
	return nil
}

// restoreV2 keeps the legacy opaque-base64 restore path alive for already-
// pushed checkpoints that predate v3. When the metadata branch ages out
// past the last v2 checkpoint, this function and ChatArchive can be removed.
func restoreV2(t cursorTarget, data []byte) error {
	var archive ChatArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		return fmt.Errorf("decoding v2 cursor archive: %w", err)
	}
	if archive.Format != archiveFormat {
		return fmt.Errorf("unexpected v2 archive format %q", archive.Format)
	}
	if archive.Version != 2 {
		return fmt.Errorf("unsupported v2 archive version %d", archive.Version)
	}

	if err := os.MkdirAll(filepath.Dir(t.storeDB), 0o750); err != nil {
		return fmt.Errorf("creating chats directory: %w", err)
	}
	if err := writeBase64(archive.DBBytes, t.storeDB); err != nil {
		return fmt.Errorf("writing store.db: %w", err)
	}
	for _, pair := range []struct {
		b64, path string
	}{
		{archive.DBWALBytes, t.storeDB + "-wal"},
		{archive.DBSHMBytes, t.storeDB + "-shm"},
	} {
		if pair.b64 == "" {
			_ = os.Remove(pair.path)
			continue
		}
		if err := writeBase64(pair.b64, pair.path); err != nil {
			return fmt.Errorf("writing %s: %w", filepath.Base(pair.path), err)
		}
	}

	if len(archive.Transcript) > 0 {
		if err := os.MkdirAll(filepath.Dir(t.nestedJSO), 0o750); err != nil {
			return fmt.Errorf("creating transcripts directory: %w", err)
		}
		if err := writeTranscript(archive.Transcript, t.nestedJSO); err != nil {
			return fmt.Errorf("writing transcript: %w", err)
		}
	}
	return nil
}

func writeBase64(b64, targetPath string) error {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("base64: %w", err)
	}
	if err := os.WriteFile(targetPath, raw, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", targetPath, err)
	}
	return nil
}

func writeTranscript(entries []json.RawMessage, targetPath string) error {
	f, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path under ~/.cursor
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	for _, entry := range entries {
		if _, err := f.Write(entry); err != nil {
			return fmt.Errorf("write entry: %w", err)
		}
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("write newline: %w", err)
		}
	}
	return nil
}
