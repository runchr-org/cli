package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
	"github.com/spf13/cobra"
)

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

	data, err := cursor.ExportChatArchive(ctx, agentID)
	if err != nil {
		return fmt.Errorf("exporting cursor chat: %w", err)
	}

	// Decode archive for progress reporting
	var archive cursor.ChatArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		return fmt.Errorf("decoding archive for display: %w", err)
	}

	fmt.Fprintf(w, "Found database: %s\n", archive.DBPath)
	fmt.Fprintf(w, "Exported store.db (WAL=%t, SHM=%t)\n",
		archive.DBWALBytes != "", archive.DBSHMBytes != "")
	if archive.TranscriptPath != "" {
		fmt.Fprintf(w, "Found transcript: %s\n", archive.TranscriptPath)
		fmt.Fprintf(w, "Exported %d transcript entries\n", len(archive.Transcript))
	} else {
		fmt.Fprintln(w, "No transcript JSONL found (exporting without it)")
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
