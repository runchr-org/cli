package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"charm.land/glamour/v2/ansi"

	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/learn"
	"github.com/entireio/cli/cmd/entire/cli/mdrender"
	"github.com/spf13/cobra"
)

// runLearnGenerate is overridable for tests so they can stub out the agent
// call without touching cobra plumbing.
var runLearnGenerate = learn.Generate

const learnNotGitRepoMessage = "Entire works inside a git repository. Run 'git init' or cd into one and try again."

const learnNoTextGeneratorMessage = `No TextGenerator-capable agent on PATH.

'entire labs learn' renders the tour by piping the discovered command surface
through your locally-installed agent. Install one of: claude, codex, gemini,
cursor, copilot, or an external entire-agent-* plugin that declares
text_generator support.`

// newLearnCmd builds the `entire learn` cobra command. Hidden from
// `entire help` while the feature matures — discoverable via
// `entire labs` and runs normally for users who already know the name.
// Mirrors the registration shape of `entire review`.
func newLearnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "learn",
		// Hidden from `entire help` while the feature is still maturing —
		// users who know about it can still run `entire learn` /
		// `entire learn --help` and the command works normally.
		Hidden: true,
		Short:  "Tour the Entire CLI tailored to your repo state",
		Long: `Generate a state-aware tour of the Entire CLI.

Detects whether Entire is enabled, which agents are installed, and whether
this repo has captured history. Then asks a locally-installed TextGenerator
agent (claude, codex, gemini, cursor, copilot, or an external entire-agent-*
plugin that declares text_generator) to render an actionable tour against
the live command surface.

Labs entry: learn is experimental. We are actively refining it based on
user feedback.

Requires a TextGenerator-capable agent on your PATH. Output is rendered
through the shared markdown palette in interactive terminals; pipelines
get raw markdown so they remain grep-friendly.

Examples:
  entire learn`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeLearn(cmd.Context(), cmd.OutOrStdout(), cmd.Root())
		},
	}
	return cmd
}

func executeLearn(ctx context.Context, w io.Writer, root *cobra.Command) error {
	settings, err := LoadEntireSettings(ctx)
	configuredProvider := ""
	if err == nil && settings.SummaryGeneration != nil {
		configuredProvider = settings.SummaryGeneration.Provider
	}

	opts := learn.Options{
		LoadSettings:        learnSettingsLoader,
		ListInstalledAgents: GetAgentsWithHooksInstalled,
		ConfiguredProvider:  configuredProvider,
		Labs:                labsRegistryForLearn(),
	}

	generate := func(ctx context.Context) (*learn.Result, error) {
		return runLearnGenerate(ctx, root, opts)
	}

	var (
		result    *learn.Result
		generErr  error
		usedTUI   = interactive.IsTerminalWriter(w) && !IsAccessibleMode()
		cancelled bool
	)
	if usedTUI {
		result, generErr = runLearnTUI(ctx, w, generate)
		if errors.Is(generErr, errLearnCancelled) {
			cancelled = true
			generErr = nil
		}
	} else {
		result, generErr = generate(ctx)
	}

	if cancelled {
		return nil
	}
	if generErr != nil {
		return translateLearnError(generErr)
	}

	rendered, err := mdrender.RenderForWriterWithOverride(w, result.Markdown, learnHeaderOverride)
	if err != nil {
		// mdrender failed — fall back to raw markdown rather than
		// surfacing a renderer panic to the user.
		rendered = result.Markdown
	}
	fmt.Fprintln(w, rendered)
	if usedTUI {
		fmt.Fprintf(w, "(rendered by %s)\n", result.DisplayName)
	}
	return nil
}

// translateLearnError converts learn.Generate errors into user-facing
// messages. ErrNotGitRepo and ErrNoTextGenerator print and exit 0;
// everything else propagates.
func translateLearnError(err error) error {
	if errors.Is(err, learn.ErrNotGitRepo) {
		return NewSilentError(errors.New(learnNotGitRepoMessage))
	}
	if errors.Is(err, learn.ErrNoTextGenerator) {
		return errors.New(learnNoTextGeneratorMessage)
	}
	return err
}

// learnSettingsLoader adapts the cli package's settings helpers to the
// (enabled, isSetUp, err) shape the learn package expects. Keeping this
// adapter at the cli boundary leaves the learn package free of cli imports.
func learnSettingsLoader(ctx context.Context) (bool, bool, error) {
	s, err := LoadEntireSettings(ctx)
	if err != nil {
		return false, false, err
	}
	return s.Enabled, true, nil
}

// labsRegistryForLearn projects the cli's experimentalCommands list onto
// the learn-package shape. Done at the cli boundary so the learn package
// doesn't need to import labs internals (which would create a cycle —
// labs.go itself wires in newLearnCmd).
func labsRegistryForLearn() []learn.LabsCommand {
	out := make([]learn.LabsCommand, 0, len(experimentalCommands))
	for _, info := range experimentalCommands {
		out = append(out, learn.LabsCommand{
			Name:       info.Name,
			Invocation: info.Invocation,
			Summary:    info.Summary,
		})
	}
	return out
}

// learnHeaderOverride paints H2 violet so section headers stand apart
// from the orange inline-code, list-item, and accent surfaces that
// already dominate the rendered tour. The system prompt instructs the
// agent to open every section with '## <title>', so this override is
// what gives the section breaks their color — without it, H2 is the
// shared cyan from mdrender's default palette.
func learnHeaderOverride(styles *ansi.StyleConfig) {
	styles.H2.Color = mdrender.StringPtr("#a78bfa")
}
