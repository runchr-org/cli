package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type experimentalCommandInfo struct {
	Name       string
	Invocation string
	Summary    string
}

var experimentalCommands = []experimentalCommandInfo{
	{
		Name:       "review",
		Invocation: "entire review",
		Summary:    "Run configured review skills against the current branch",
	},
	{
		Name:       "tour",
		Invocation: "entire tour",
		Summary:    "Tour the Entire CLI",
	},
}

func newLabsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "labs",
		Short: "Explore experimental Entire workflows",
		Long:  labsOverview(),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			topic := args[0]
			err := fmt.Errorf("unknown labs topic %q", topic)
			fmt.Fprintf(cmd.ErrOrStderr(), "%v\n\n%s\n", err, labsTopicHint(topic))
			return NewSilentError(err)
		},
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprint(cmd.OutOrStdout(), labsOverview())
		},
	}
	return cmd
}

func labsOverview() string {
	if len(experimentalCommands) == 0 {
		return `Labs

No experimental commands are available in this build.
`
	}

	return `Labs

These are newer Entire workflows we are actively refining. They are available
to try now, but details may change based on feedback.

Available experimental commands:
` + renderExperimentalCommands(experimentalCommands) + `
Try:
  entire tour --help
  entire review --help
`
}

// labsTopicHint returns the redirect string shown when the user types
// `entire labs <topic>` and topic is not a real labs subcommand. When the
// topic matches a known experimental command (e.g. `entire labs review`
// when review actually lives at the top level), point at its canonical
// invocation instead of leaving the user to guess.
func labsTopicHint(topic string) string {
	for _, info := range experimentalCommands {
		if info.Name == topic {
			return fmt.Sprintf("%s lives at `%s`. Run `%s --help` for command-specific help.", info.Name, info.Invocation, info.Invocation)
		}
	}
	return "Run `entire labs` to see available experimental commands."
}

func renderExperimentalCommands(commands []experimentalCommandInfo) string {
	width := 16
	for _, info := range commands {
		if l := len(info.Invocation); l > width {
			width = l
		}
	}
	var out strings.Builder
	for _, info := range commands {
		out.WriteString("  ")
		out.WriteString(padRight(info.Invocation, width))
		out.WriteByte(' ')
		out.WriteString(info.Summary)
		out.WriteByte('\n')
	}
	return out.String()
}

func padRight(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(value))
}
