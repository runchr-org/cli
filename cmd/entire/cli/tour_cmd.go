package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"charm.land/glamour/v2/ansi"

	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/tour"
	"github.com/entireio/cli/cmd/entire/cli/mdrender"
	"github.com/spf13/cobra"
)

// runTourGenerate is overridable for tests so they can stub out the agent
// call without touching cobra plumbing.
var (
	runTourGenerate       = tour.Generate
	runTourGenerateLatest = tour.GenerateLatest
)

const tourNotGitRepoMessage = "Entire works inside a git repository. Run 'git init' or cd into one and try again."

const tourNoTextGeneratorMessage = `No TextGenerator-capable agent on PATH.

'entire labs tour' renders the tour by piping the discovered command surface
through your locally-installed agent. Install one of: claude, codex, gemini,
cursor, copilot, or an external entire-agent-* plugin that declares
text_generator support.`

// newTourCmd builds the `entire tour` cobra command. Hidden from
// `entire help` while the feature matures — discoverable via
// `entire labs` and runs normally for users who already know the name.
// Mirrors the registration shape of `entire review`.
func newTourCmd() *cobra.Command {
	var latestFlag bool

	cmd := &cobra.Command{
		Use: "tour",
		// Hidden from `entire help` while the feature is still maturing —
		// users who know about it can still run `entire tour` /
		// `entire tour --help` and the command works normally.
		Hidden: true,
		Short:  "Tour the Entire CLI",
		Long: `Generate a state-aware tour of the Entire CLI.

Detects whether Entire is enabled, which agents are installed, and whether
this repo has captured history. Then asks a locally-installed TextGenerator
agent (claude, codex, gemini, cursor, copilot, or an external entire-agent-*
plugin that declares text_generator) to render an actionable tour against
the live command surface.

Pass --latest to skip the tour and instead summarize the latest post
from the entire.io blog feed — a quick "what's new in Entire" digest.

Labs entry: tour is experimental. We are actively refining it based on
user feedback.

Requires a TextGenerator-capable agent on your PATH. Output is rendered
through the shared markdown palette in interactive terminals; pipelines
get raw markdown so they remain grep-friendly.

Examples:
  entire tour
  entire tour --latest`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeTour(cmd.Context(), cmd.OutOrStdout(), cmd.Root(), latestFlag)
		},
	}
	cmd.Flags().BoolVar(&latestFlag, "latest", false, "Summarize the latest entire.io blog post instead of touring the CLI")
	return cmd
}

func executeTour(ctx context.Context, w io.Writer, root *cobra.Command, latestFlag bool) error {
	settings, err := LoadEntireSettings(ctx)
	configuredProvider := ""
	if err == nil && settings.SummaryGeneration != nil {
		configuredProvider = settings.SummaryGeneration.Provider
	}

	opts := tour.Options{
		LoadSettings:        tourSettingsLoader,
		ListInstalledAgents: GetAgentsWithHooksInstalled,
		ConfiguredProvider:  configuredProvider,
		Labs:                labsRegistryForTour(),
	}

	generate := func(ctx context.Context) (*tour.Result, error) {
		if latestFlag {
			return runTourGenerateLatest(ctx, opts)
		}
		return runTourGenerate(ctx, root, opts)
	}

	usedTUI := interactive.IsTerminalWriter(w) && !IsAccessibleMode()
	title, subtitle := "Generating tour", "This can take a moment."
	if latestFlag {
		title = "Fetching the latest dispatch"
	}

	var (
		result   *tour.Result
		generErr error
	)
	if usedTUI {
		result, generErr = runTourTUI(ctx, w, title, subtitle, generate)
		if errors.Is(generErr, errTourCancelled) {
			return nil
		}
	} else {
		result, generErr = generate(ctx)
	}
	if generErr != nil {
		return translateTourError(generErr)
	}

	rendered, err := mdrender.RenderForWriterWithOverride(w, result.Markdown, tourHeaderOverride)
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

// translateTourError converts tour.Generate errors into user-facing
// messages. ErrNotGitRepo and ErrNoTextGenerator print and exit 0;
// everything else propagates.
func translateTourError(err error) error {
	if errors.Is(err, tour.ErrNotGitRepo) {
		return NewSilentError(errors.New(tourNotGitRepoMessage))
	}
	if errors.Is(err, tour.ErrNoTextGenerator) {
		return errors.New(tourNoTextGeneratorMessage)
	}
	return err
}

// tourSettingsLoader adapts the cli package's settings helpers to the
// (enabled, isSetUp, err) shape the tour package expects. Keeping this
// adapter at the cli boundary leaves the tour package free of cli imports.
func tourSettingsLoader(ctx context.Context) (bool, bool, error) {
	s, err := LoadEntireSettings(ctx)
	if err != nil {
		return false, false, err
	}
	return s.Enabled, true, nil
}

// labsRegistryForTour projects the cli's experimentalCommands list onto
// the tour-package shape. Done at the cli boundary so the tour package
// doesn't need to import labs internals (which would create a cycle —
// labs.go itself wires in newTourCmd).
func labsRegistryForTour() []tour.LabsCommand {
	out := make([]tour.LabsCommand, 0, len(experimentalCommands))
	for _, info := range experimentalCommands {
		out = append(out, tour.LabsCommand{
			Name:       info.Name,
			Invocation: info.Invocation,
			Summary:    info.Summary,
		})
	}
	return out
}

// tourHeaderOverride paints H2 violet so section headers stand apart
// from the orange inline-code, list-item, and accent surfaces that
// already dominate the rendered tour. The system prompt instructs the
// agent to open every section with '## <title>', so this override is
// what gives the section breaks their color — without it, H2 is the
// shared cyan from mdrender's default palette.
func tourHeaderOverride(styles *ansi.StyleConfig) {
	styles.H2.Color = mdrender.StringPtr("#a78bfa")
}
