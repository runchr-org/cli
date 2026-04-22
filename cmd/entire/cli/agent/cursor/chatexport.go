package cursor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ChatArchive is the portable archive format for Cursor agent chat sessions.
// Shared by cursor-export / cursor-import and by the CheckpointContributor
// that embeds chat data into committed checkpoints.
//
// Version 2 stores store.db (and its WAL/SHM sidecars) as opaque base64 blobs
// rather than exploding them into SQL rows. This keeps the archive bit-exact
// and removes any SQLite runtime dependency from the shipped CLI.
type ChatArchive struct {
	Format         string            `json:"format"`
	Version        int               `json:"version"`
	AgentID        string            `json:"agentId"`
	DBPath         string            `json:"db_path"`
	DBBytes        string            `json:"db_bytes"`
	DBWALBytes     string            `json:"db_wal_bytes,omitempty"`
	DBSHMBytes     string            `json:"db_shm_bytes,omitempty"`
	TranscriptPath string            `json:"transcript_path,omitempty"`
	Transcript     []json.RawMessage `json:"transcript,omitempty"`
}

const (
	archiveFormat  = "cursor-chat-export"
	archiveVersion = 2
)

// ExportChatArchive exports a Cursor chat session as a JSON archive.
// agentID is the Cursor conversation UUID. The ctx is reserved for future I/O
// cancellation; current operations are synchronous file reads.
func ExportChatArchive(_ context.Context, agentID string) ([]byte, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	cursorDir := filepath.Join(homeDir, ".cursor")
	chatsDir := filepath.Join(cursorDir, "chats")
	projectsDir := filepath.Join(cursorDir, "projects")

	dbPath, err := findOne(filepath.Join(chatsDir, "*", agentID, "store.db"))
	if err != nil {
		return nil, fmt.Errorf("finding store.db for agent %s: %w", agentID, err)
	}
	if dbPath == "" {
		return nil, fmt.Errorf("no store.db found for agent %q (searched %s/*/%s/store.db)", agentID, chatsDir, agentID)
	}

	archive := ChatArchive{
		Format:  archiveFormat,
		Version: archiveVersion,
		AgentID: agentID,
		DBPath:  dbPath,
	}

	if archive.DBBytes, err = readBase64(dbPath); err != nil {
		return nil, fmt.Errorf("reading store.db: %w", err)
	}
	if wal, err := readBase64(dbPath + "-wal"); err == nil {
		archive.DBWALBytes = wal
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading store.db-wal: %w", err)
	}
	if shm, err := readBase64(dbPath + "-shm"); err == nil {
		archive.DBSHMBytes = shm
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading store.db-shm: %w", err)
	}

	transcriptPath, err := findOne(filepath.Join(projectsDir, "*", "agent-transcripts", agentID+".jsonl"))
	if err != nil {
		return nil, fmt.Errorf("searching for transcript: %w", err)
	}
	if transcriptPath != "" {
		archive.TranscriptPath = transcriptPath
		entries, err := ReadTranscriptFile(transcriptPath)
		if err != nil {
			return nil, fmt.Errorf("reading transcript: %w", err)
		}
		archive.Transcript = entries
	}

	data, err := json.Marshal(archive)
	if err != nil {
		return nil, fmt.Errorf("marshaling archive: %w", err)
	}
	return data, nil
}

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

func readBase64(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from known cursor dirs
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// ReadTranscriptFile reads a JSONL transcript file and returns the parsed entries.
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
