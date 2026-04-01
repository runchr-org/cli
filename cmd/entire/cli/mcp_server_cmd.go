package cli

import (
	"fmt"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/entireio/cli/cmd/entire/cli/mcpserver"
)

func newMCPServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "mcp-server",
		Short:  "Start the MCP server for memory loop tools (used by Claude Code)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			s := mcpserver.NewServer()
			if err := server.ServeStdio(s); err != nil {
				return fmt.Errorf("MCP server error: %w", err)
			}
			return nil
		},
	}
	return cmd
}
