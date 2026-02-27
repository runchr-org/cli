package cli

import (
	"context"
	"crypto/md5" //nolint:gosec // MD5 used to match Cursor's directory naming, not for security
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newCursorImportCmd() *cobra.Command {
	var (
		workspaceHash string
		projectSlug   string
		force         bool
	)

	cmd := &cobra.Command{
		Use:   "cursor-import <archive-file>",
		Short: "Import a Cursor agent chat session from a portable archive",
		Long: `Import a Cursor agent chat session from a .cursor-chat.json archive file.

Recreates the store.db and transcript JSONL in the appropriate
Cursor directories (~/.cursor/chats/ and ~/.cursor/projects/).

By default, the workspace hash is computed as MD5 of the current directory
(matching Cursor's convention). Override with --workspace-hash if needed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCursorImport(cmd, args[0], workspaceHash, projectSlug, force)
		},
	}

	cmd.Flags().StringVar(&workspaceHash, "workspace-hash", "", "override the workspace hash (subfolder under ~/.cursor/chats/)")
	cmd.Flags().StringVar(&projectSlug, "project-slug", "", "override the project slug (subfolder under ~/.cursor/projects/)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files without prompting")

	return cmd
}

func runCursorImport(cmd *cobra.Command, archivePath, workspaceHash, projectSlug string, force bool) error {
	w := cmd.OutOrStdout()
	ctx := cmd.Context()

	data, err := os.ReadFile(archivePath) //nolint:gosec // archivePath is a CLI argument, not user-controlled web input
	if err != nil {
		return fmt.Errorf("reading archive: %w", err)
	}

	var archive cursorChatArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		return fmt.Errorf("parsing archive: %w", err)
	}

	if archive.Format != "cursor-chat-export" {
		return fmt.Errorf("not a valid cursor-chat-export file (format: %q)", archive.Format)
	}

	agentID := archive.AgentID
	fmt.Fprintf(w, "Importing agent: %s\n", agentID)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	cursorDir := filepath.Join(homeDir, ".cursor")
	chatsDir := filepath.Join(cursorDir, "chats")
	projectsDir := filepath.Join(cursorDir, "projects")

	// Determine workspace hash: MD5 of current directory (Cursor's convention)
	if workspaceHash == "" {
		cwd, err := os.Getwd() //nolint:forbidigo // need actual cwd for Cursor's MD5(project_path) hash, not git root
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		workspaceHash = cursorWorkspaceHash(cwd)
		fmt.Fprintf(w, "Workspace hash: %s (from %s)\n", workspaceHash, cwd)
	}

	// Import store.db
	dbTarget := filepath.Join(chatsDir, workspaceHash, agentID, "store.db")
	fmt.Fprintf(w, "Target database: %s\n", dbTarget)

	if fileExists(dbTarget) && !force {
		return fmt.Errorf("%s already exists; use --force to overwrite", dbTarget)
	}

	if err := importCursorDB(ctx, archive.Store, dbTarget, force); err != nil {
		return fmt.Errorf("importing database: %w", err)
	}
	fmt.Fprintf(w, "Imported %d meta rows, %d blobs\n", len(archive.Store.Meta), len(archive.Store.Blobs))

	// Import transcript if present
	if len(archive.Transcript) > 0 {
		slug := projectSlug
		if slug == "" {
			cwd, err := os.Getwd() //nolint:forbidigo // need actual cwd for Cursor's MD5(project_path) hash, not git root
			if err != nil {
				return fmt.Errorf("getting current directory: %w", err)
			}
			slug = cursorWorkspaceHash(cwd)
		}

		transcriptTarget := filepath.Join(projectsDir, slug, "agent-transcripts", agentID+".jsonl")
		fmt.Fprintf(w, "Target transcript: %s\n", transcriptTarget)

		if fileExists(transcriptTarget) && !force {
			return fmt.Errorf("%s already exists; use --force to overwrite", transcriptTarget)
		}

		if err := importCursorTranscript(archive.Transcript, transcriptTarget); err != nil {
			return fmt.Errorf("importing transcript: %w", err)
		}
		fmt.Fprintf(w, "Imported %d transcript entries\n", len(archive.Transcript))
	} else {
		fmt.Fprintln(w, "No transcript in archive, skipping.")
	}

	fmt.Fprintf(w, "\nImport complete for agent %s\n", agentID)
	return nil
}

// cursorWorkspaceHash computes the workspace hash Cursor uses for directory naming.
// Cursor stores per-project data under MD5(absolute_project_path).
func cursorWorkspaceHash(projectPath string) string {
	sum := md5.Sum([]byte(projectPath)) //nolint:gosec // matching Cursor's convention, not used for security
	return hex.EncodeToString(sum[:])
}

func importCursorDB(ctx context.Context, store cursorStoreData, targetPath string, force bool) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if force {
		// Remove existing DB and WAL/SHM files (errors ignored; files may not exist)
		_ = os.Remove(targetPath)
		_ = os.Remove(targetPath + "-wal")
		_ = os.Remove(targetPath + "-shm")
	}

	db, err := sql.Open("sqlite", targetPath)
	if err != nil {
		return fmt.Errorf("creating database: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=wal"); err != nil {
		return fmt.Errorf("setting journal mode: %w", err)
	}

	if _, err := db.ExecContext(ctx, "CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		return fmt.Errorf("creating meta table: %w", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)"); err != nil {
		return fmt.Errorf("creating blobs table: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	for key, value := range store.Meta {
		if _, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES (?, ?)", key, value); err != nil {
			return fmt.Errorf("inserting meta key %q: %w", key, err)
		}
	}

	for id, b64Data := range store.Blobs {
		data, err := base64.StdEncoding.DecodeString(b64Data)
		if err != nil {
			return fmt.Errorf("decoding blob %q: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO blobs (id, data) VALUES (?, ?)", id, data); err != nil {
			return fmt.Errorf("inserting blob %q: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

func importCursorTranscript(entries []json.RawMessage, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	f, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // targetPath is constructed from known paths
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer f.Close()

	for _, entry := range entries {
		if _, err := f.Write(entry); err != nil {
			return fmt.Errorf("writing transcript entry: %w", err)
		}
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("writing newline: %w", err)
		}
	}

	return nil
}
