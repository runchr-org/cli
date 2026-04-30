package cli

import (
	"errors"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/spf13/cobra"
)

func newSkillGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Generate reusable skills from Entire sessions",
		Long: `Generate reusable skills from Entire session history.

Commands:
  generate  Generate a skill draft from a session

Examples:
  entire skill generate
  entire skill generate --session <session-id>
  entire skill generate --session <session-id> --output ./my-skill`,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := paths.WorktreeRoot(cmd.Context()); err != nil {
				return errors.New("not a git repository")
			}
			return nil
		},
	}

	cmd.AddCommand(newSkillGenerateCmd())
	return cmd
}
