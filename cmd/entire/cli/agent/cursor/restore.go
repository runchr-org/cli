package cursor

import (
	"context"
	"crypto/md5" //nolint:gosec // matches Cursor's directory naming convention, not security
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WorkspaceHash returns the Cursor workspace hash for a project path.
// Cursor stores per-project data under MD5(absolute_project_path).
func WorkspaceHash(projectPath string) string {
	sum := md5.Sum([]byte(projectPath)) //nolint:gosec // matches Cursor's convention
	return hex.EncodeToString(sum[:])
}

// RestoreCheckpointFiles reads the Cursor chat archive out of the checkpoint
// files map and writes store.db (+ optional WAL/SHM sidecars) + the transcript
// JSONL back to the agent's native storage under ~/.cursor. The workspace hash
// is computed from the current working directory, matching Cursor's own
// convention — the user should run rewind/resume from the repo that originally
// hosted the session.
func (c *CursorAgent) RestoreCheckpointFiles(_ context.Context, sessionID string, files map[string][]byte) error {
	data, ok := files[sessionID+".cursor-chat.json"]
	if !ok {
		return nil
	}

	var archive ChatArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		return fmt.Errorf("decoding cursor chat archive: %w", err)
	}
	if archive.Format != archiveFormat {
		return fmt.Errorf("unexpected archive format %q", archive.Format)
	}
	if archive.Version != archiveVersion {
		return fmt.Errorf("unsupported archive version %d (want %d)", archive.Version, archiveVersion)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home directory: %w", err)
	}
	cwd, err := os.Getwd() //nolint:forbidigo // Cursor's MD5(project_path) uses cwd, not git root
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	hash := WorkspaceHash(cwd)

	dbTarget := filepath.Join(home, ".cursor", "chats", hash, archive.AgentID, "store.db")
	if err := os.MkdirAll(filepath.Dir(dbTarget), 0o750); err != nil {
		return fmt.Errorf("creating chats directory: %w", err)
	}
	if err := writeBase64(archive.DBBytes, dbTarget); err != nil {
		return fmt.Errorf("writing store.db: %w", err)
	}
	for _, pair := range []struct {
		b64, path string
	}{
		{archive.DBWALBytes, dbTarget + "-wal"},
		{archive.DBSHMBytes, dbTarget + "-shm"},
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
		txTarget := filepath.Join(home, ".cursor", "projects", hash, "agent-transcripts", archive.AgentID+".jsonl")
		if err := os.MkdirAll(filepath.Dir(txTarget), 0o750); err != nil {
			return fmt.Errorf("creating transcripts directory: %w", err)
		}
		if err := writeJSONL(archive.Transcript, txTarget); err != nil {
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

func writeJSONL(entries []json.RawMessage, targetPath string) error {
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
