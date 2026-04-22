package cursor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	// sqlite registers the "sqlite" driver for database/sql (pure Go, no CGo).
	_ "modernc.org/sqlite"
)

// ChatArchive is the portable archive format for Cursor agent chat sessions.
// Shared by both cursor-export and cursor-import commands, and used by
// ContributeCheckpointFiles to embed chat data in checkpoints.
type ChatArchive struct {
	Format         string            `json:"format"`
	Version        int               `json:"version"`
	AgentID        string            `json:"agentId"`
	DBPath         string            `json:"db_path"`
	TranscriptPath string            `json:"transcript_path,omitempty"`
	Store          StoreData         `json:"store"`
	Transcript     []json.RawMessage `json:"transcript,omitempty"`
}

// StoreData contains the meta and blob tables from Cursor's store.db.
type StoreData struct {
	Meta  map[string]string `json:"meta"`
	Blobs map[string]string `json:"blobs"`
}

// ExportChatArchive exports a Cursor chat session as a JSON archive.
// Returns the marshaled archive bytes. agentID is the Cursor conversation UUID.
func ExportChatArchive(ctx context.Context, agentID string) ([]byte, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	cursorDir := filepath.Join(homeDir, ".cursor")
	chatsDir := filepath.Join(cursorDir, "chats")
	projectsDir := filepath.Join(cursorDir, "projects")

	// Find store.db
	dbPath, err := FindFile(filepath.Join(chatsDir, "*", agentID, "store.db"))
	if err != nil {
		return nil, fmt.Errorf("finding store.db for agent %s: %w", agentID, err)
	}
	if dbPath == "" {
		return nil, fmt.Errorf("no store.db found for agent %q (searched %s/*/%s/store.db)", agentID, chatsDir, agentID)
	}

	// Export database
	storeData, err := ExportDB(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("exporting database: %w", err)
	}

	// Find transcript
	transcriptPath, err := FindFile(filepath.Join(projectsDir, "*", "agent-transcripts", agentID+".jsonl"))
	if err != nil {
		return nil, fmt.Errorf("searching for transcript: %w", err)
	}

	archive := ChatArchive{
		Format:  "cursor-chat-export",
		Version: 1,
		AgentID: agentID,
		DBPath:  dbPath,
		Store:   storeData,
	}

	if transcriptPath != "" {
		archive.TranscriptPath = transcriptPath
		transcript, err := ReadTranscriptFile(transcriptPath)
		if err != nil {
			return nil, fmt.Errorf("reading transcript: %w", err)
		}
		archive.Transcript = transcript
	}

	data, err := json.Marshal(archive)
	if err != nil {
		return nil, fmt.Errorf("marshaling archive: %w", err)
	}
	return data, nil
}

// FindFile finds a file matching a glob pattern. Returns empty string if not found.
func FindFile(pattern string) (string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob %s: %w", pattern, err)
	}
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0], nil
}

// ExportDB reads all data from a Cursor store.db file.
// Opens the DB read-only so a live Cursor process holding the file is not
// disturbed (no WAL checkpoint, no lock contention).
func ExportDB(ctx context.Context, dbPath string) (StoreData, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return StoreData{}, fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	meta := make(map[string]string)
	metaRows, err := db.QueryContext(ctx, "SELECT key, value FROM meta")
	if err != nil {
		return StoreData{}, fmt.Errorf("querying meta: %w", err)
	}
	defer metaRows.Close()
	for metaRows.Next() {
		var key, value string
		if err := metaRows.Scan(&key, &value); err != nil {
			return StoreData{}, fmt.Errorf("scanning meta row: %w", err)
		}
		meta[key] = value
	}
	if err := metaRows.Err(); err != nil {
		return StoreData{}, fmt.Errorf("iterating meta rows: %w", err)
	}

	blobs := make(map[string]string)
	blobRows, err := db.QueryContext(ctx, "SELECT id, data FROM blobs")
	if err != nil {
		return StoreData{}, fmt.Errorf("querying blobs: %w", err)
	}
	defer blobRows.Close()
	for blobRows.Next() {
		var id string
		var data []byte
		if err := blobRows.Scan(&id, &data); err != nil {
			return StoreData{}, fmt.Errorf("scanning blob row: %w", err)
		}
		blobs[id] = base64.StdEncoding.EncodeToString(data)
	}
	if err := blobRows.Err(); err != nil {
		return StoreData{}, fmt.Errorf("iterating blob rows: %w", err)
	}

	return StoreData{Meta: meta, Blobs: blobs}, nil
}

// ReadTranscriptFile reads a JSONL transcript file and returns the parsed entries.
func ReadTranscriptFile(path string) ([]json.RawMessage, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from glob results, not user input
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
