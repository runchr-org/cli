package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
	"github.com/spf13/cobra"
)

func newCursorSessionsCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "cursor-sessions",
		Short: "List Cursor agent sessions stored locally",
		Long: `List Cursor agent sessions discovered under ~/.cursor/chats.

Each row shows the agent ID, store.db size, and transcript line count.
Use --json for machine-readable output.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sessions, err := cursor.ListSessions()
			if err != nil {
				return fmt.Errorf("listing sessions: %w", err)
			}

			w := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(w)
				return enc.Encode(sessions)
			}

			if len(sessions) == 0 {
				fmt.Fprintln(w, "No Cursor agent sessions found.")
				return nil
			}

			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "AGENT-ID\tWORKSPACE\tDB-SIZE\tTRANSCRIPT-LINES")
			for _, s := range sessions {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\n", s.AgentID, s.WorkspaceHash, s.DBSize, s.TranscriptLines)
			}
			return tw.Flush()
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return cmd
}
