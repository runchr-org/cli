package cli

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

	"github.com/spf13/cobra"
)

// cursorChatArchive is the portable archive format for Cursor agent chat sessions.
// Shared by both cursor-export and cursor-import commands.
type cursorChatArchive struct {
	Format         string            `json:"format"`
	Version        int               `json:"version"`
	AgentID        string            `json:"agentId"`
	DBPath         string            `json:"db_path"`
	TranscriptPath string            `json:"transcript_path,omitempty"`
	Store          cursorStoreData   `json:"store"`
	Transcript     []json.RawMessage `json:"transcript,omitempty"`
}

type cursorStoreData struct {
	Meta  map[string]string `json:"meta"`
	Blobs map[string]string `json:"blobs"`
}

func newCursorExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cursor-export <agent-id> [output-path]",
		Short: "Export a Cursor agent chat session to a portable archive",
		Long: `Export a Cursor agent chat session (store.db + transcript) to a single
.cursor-chat.json file that can be imported on another machine.

Searches ~/.cursor/chats/ for the store.db and ~/.cursor/projects/ for
the agent transcript JSONL.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID := args[0]
			outputPath := agentID + ".cursor-chat.json"
			if len(args) > 1 {
				outputPath = args[1]
			}
			return runCursorExport(cmd, agentID, outputPath)
		},
	}
}

func runCursorExport(cmd *cobra.Command, agentID, outputPath string) error {
	w := cmd.OutOrStdout()
	ctx := cmd.Context()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	cursorDir := filepath.Join(homeDir, ".cursor")
	chatsDir := filepath.Join(cursorDir, "chats")
	projectsDir := filepath.Join(cursorDir, "projects")

	// Find store.db
	dbPath, err := findCursorFile(filepath.Join(chatsDir, "*", agentID, "store.db"))
	if err != nil {
		return fmt.Errorf("finding store.db for agent %s: %w", agentID, err)
	}
	if dbPath == "" {
		return fmt.Errorf("no store.db found for agent %q (searched %s/*/%s/store.db)", agentID, chatsDir, agentID)
	}
	fmt.Fprintf(w, "Found database: %s\n", dbPath)

	// Export database
	storeData, err := exportCursorDB(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("exporting database: %w", err)
	}
	fmt.Fprintf(w, "Exported %d meta rows, %d blobs\n", len(storeData.Meta), len(storeData.Blobs))

	// Find transcript
	transcriptPath, err := findCursorFile(filepath.Join(projectsDir, "*", "agent-transcripts", agentID+".jsonl"))
	if err != nil {
		return fmt.Errorf("searching for transcript: %w", err)
	}

	archive := cursorChatArchive{
		Format:  "cursor-chat-export",
		Version: 1,
		AgentID: agentID,
		DBPath:  dbPath,
		Store:   storeData,
	}

	if transcriptPath != "" {
		fmt.Fprintf(w, "Found transcript: %s\n", transcriptPath)
		archive.TranscriptPath = transcriptPath

		transcript, err := readCursorTranscript(transcriptPath)
		if err != nil {
			return fmt.Errorf("reading transcript: %w", err)
		}
		archive.Transcript = transcript
		fmt.Fprintf(w, "Exported %d transcript entries\n", len(transcript))
	} else {
		fmt.Fprintln(w, "No transcript JSONL found (exporting without it)")
	}

	data, err := json.Marshal(archive)
	if err != nil {
		return fmt.Errorf("marshaling archive: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0o600); err != nil {
		return fmt.Errorf("writing archive: %w", err)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("stat output file: %w", err)
	}
	sizeMB := float64(info.Size()) / (1024 * 1024)
	fmt.Fprintf(w, "\nExported to: %s (%.2f MB)\n", outputPath, sizeMB)
	return nil
}

func findCursorFile(pattern string) (string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob %s: %w", pattern, err)
	}
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0], nil
}

func exportCursorDB(ctx context.Context, dbPath string) (cursorStoreData, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return cursorStoreData{}, fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Flush WAL to main database file
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		return cursorStoreData{}, fmt.Errorf("WAL checkpoint: %w", err)
	}

	meta := make(map[string]string)
	metaRows, err := db.QueryContext(ctx, "SELECT key, value FROM meta")
	if err != nil {
		return cursorStoreData{}, fmt.Errorf("querying meta: %w", err)
	}
	defer metaRows.Close()
	for metaRows.Next() {
		var key, value string
		if err := metaRows.Scan(&key, &value); err != nil {
			return cursorStoreData{}, fmt.Errorf("scanning meta row: %w", err)
		}
		meta[key] = value
	}
	if err := metaRows.Err(); err != nil {
		return cursorStoreData{}, fmt.Errorf("iterating meta rows: %w", err)
	}

	blobs := make(map[string]string)
	blobRows, err := db.QueryContext(ctx, "SELECT id, data FROM blobs")
	if err != nil {
		return cursorStoreData{}, fmt.Errorf("querying blobs: %w", err)
	}
	defer blobRows.Close()
	for blobRows.Next() {
		var id string
		var data []byte
		if err := blobRows.Scan(&id, &data); err != nil {
			return cursorStoreData{}, fmt.Errorf("scanning blob row: %w", err)
		}
		blobs[id] = base64.StdEncoding.EncodeToString(data)
	}
	if err := blobRows.Err(); err != nil {
		return cursorStoreData{}, fmt.Errorf("iterating blob rows: %w", err)
	}

	return cursorStoreData{Meta: meta, Blobs: blobs}, nil
}

func readCursorTranscript(path string) ([]json.RawMessage, error) {
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
