package cli

import (
	"crypto/md5" //nolint:gosec // MD5 matches Cursor's directory naming, not security
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
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

	data, err := os.ReadFile(archivePath) //nolint:gosec // archivePath is a CLI argument
	if err != nil {
		return fmt.Errorf("reading archive: %w", err)
	}

	var archive cursor.ChatArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		return fmt.Errorf("parsing archive: %w", err)
	}
	if archive.Format != "cursor-chat-export" {
		return fmt.Errorf("not a valid cursor-chat-export file (format: %q)", archive.Format)
	}
	if archive.Version != 2 {
		return fmt.Errorf("unsupported archive version %d (want 2)", archive.Version)
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

	if workspaceHash == "" {
		cwd, err := os.Getwd() //nolint:forbidigo // Cursor hashes MD5(project_path), not git root
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		workspaceHash = cursorWorkspaceHash(cwd)
		fmt.Fprintf(w, "Workspace hash: %s (from %s)\n", workspaceHash, cwd)
	}

	dbTarget := filepath.Join(chatsDir, workspaceHash, agentID, "store.db")
	fmt.Fprintf(w, "Target database: %s\n", dbTarget)
	if fileExists(dbTarget) && !force {
		return fmt.Errorf("%s already exists; use --force to overwrite", dbTarget)
	}
	if err := writeArchiveDBFiles(archive, dbTarget); err != nil {
		return fmt.Errorf("importing database: %w", err)
	}
	fmt.Fprintf(w, "Imported store.db (+WAL=%t, +SHM=%t)\n",
		archive.DBWALBytes != "", archive.DBSHMBytes != "")

	if len(archive.Transcript) > 0 {
		slug := projectSlug
		if slug == "" {
			cwd, err := os.Getwd() //nolint:forbidigo // Cursor's project slug convention
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
	sum := md5.Sum([]byte(projectPath)) //nolint:gosec // matching Cursor's convention
	return hex.EncodeToString(sum[:])
}

// writeArchiveDBFiles decodes the base64-encoded DB blobs from the archive and
// writes them alongside each other (main + optional WAL + optional SHM).
func writeArchiveDBFiles(archive cursor.ChatArchive, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	if err := writeBase64ToFile(archive.DBBytes, targetPath); err != nil {
		return fmt.Errorf("writing store.db: %w", err)
	}
	for _, pair := range []struct {
		b64, path string
	}{
		{archive.DBWALBytes, targetPath + "-wal"},
		{archive.DBSHMBytes, targetPath + "-shm"},
	} {
		if pair.b64 == "" {
			// Remove any leftover sidecar from a previous, differently-stated import.
			_ = os.Remove(pair.path)
			continue
		}
		if err := writeBase64ToFile(pair.b64, pair.path); err != nil {
			return fmt.Errorf("writing %s: %w", filepath.Base(pair.path), err)
		}
	}
	return nil
}

func writeBase64ToFile(b64, targetPath string) error {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("decoding base64: %w", err)
	}
	if err := os.WriteFile(targetPath, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", targetPath, err)
	}
	return nil
}

func importCursorTranscript(entries []json.RawMessage, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	f, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // targetPath from known dirs
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
