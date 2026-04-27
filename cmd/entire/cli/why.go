package cli

import (
	"context"
	"errors"
	"io"

	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/spf13/cobra"
)

type whyOptions struct {
	Path        string
	LineRange   string
	Interactive bool
	NoPager     bool
}

func newWhyCmd() *cobra.Command {
	var opts whyOptions

	cmd := &cobra.Command{
		Use:    "why [path]",
		Short:  "Explain why a file looks the way it does",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Path = args[0]
			}
			return runWhy(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}

	cmd.Flags().StringVarP(&opts.LineRange, "lines", "L", "", "Show a specific line or line range")
	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Force interactive TUI mode")
	cmd.Flags().BoolVar(&opts.NoPager, "no-pager", false, "Disable pager output")

	return cmd
}

func runWhy(_ context.Context, w io.Writer, _ io.Writer, opts whyOptions) error {
	canUseTUI := canRunWhyTUI(w)
	if opts.Path == "" {
		if !canUseTUI {
			return errors.New("path required when not running interactively")
		}
		return errors.New("interactive file browser is not implemented yet")
	}
	if opts.Interactive && !canUseTUI {
		return errors.New("interactive mode requires a real terminal")
	}
	return errors.New("entire why is not implemented yet")
}

func canRunWhyTUI(w io.Writer) bool {
	return !IsAccessibleMode() && interactive.IsTerminalWriter(w) && interactive.CanPromptInteractively()
}
