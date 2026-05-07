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
	var (
		latestFlag     bool
		regenerateFlag bool
	)

	cmd := &cobra.Command{
		Use: "tour",
		// Hidden from `entire help` while the feature is still maturing —
		// users who know about it can still run `entire tour` /
		// `entire tour --help` and the command works normally.
		Hidden: true,
		Short:  "Tour the Entire CLI",
		Long: `Render a state-aware tour of the Entire CLI.

The default tour reads from a pre-rendered markdown file shipped with
the binary, so it returns instantly with no agent or network call. The
content reflects the CLI surface as of the last release; maintainers
re-run with --regenerate before each release to refresh it.

Pass --latest to skip the tour and instead summarize the latest post
from the entire.io blog feed — a quick "what's new in Entire" digest.
That path requires a TextGenerator-capable agent on your PATH and a
working network connection; output appears once the agent responds.

Labs entry: tour is experimental. We are actively refining it based on
user feedback.

Examples:
  entire tour
  entire tour --latest`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeTour(cmd.Context(), cmd.OutOrStdout(), cmd.Root(), latestFlag, regenerateFlag)
		},
	}
	cmd.Flags().BoolVar(&latestFlag, "latest", false, "Summarize the latest entire.io blog post instead of touring the CLI")
	cmd.Flags().BoolVar(&regenerateFlag, "regenerate", false, "Force the agent-driven path and write the result to stdout (for refreshing the embedded tour before a release)")
	if err := cmd.Flags().MarkHidden("regenerate"); err != nil {
		panic(fmt.Sprintf("hide regenerate flag: %v", err))
	}
	return cmd
}

func executeTour(ctx context.Context, w io.Writer, root *cobra.Command, latestFlag, regenerateFlag bool) error {
	settings, settingsErr := LoadEntireSettings(ctx)
	configuredProvider := ""
	if settingsErr == nil && settings.SummaryGeneration != nil {
		configuredProvider = settings.SummaryGeneration.Provider
	}

	opts := tour.Options{
		LoadSettings:        cachedTourSettingsLoader(settings, settingsErr),
		ListInstalledAgents: GetAgentsWithHooksInstalled,
		ConfiguredProvider:  configuredProvider,
		Labs:                labsRegistryForTour(),
		Regenerate:          regenerateFlag,
	}

	usedTUI := interactive.IsTerminalWriter(w) && !IsAccessibleMode()
	needsAgent := latestFlag || regenerateFlag

	generate := func(ctx context.Context) (*tour.Result, error) {
		if latestFlag {
			return runTourGenerateLatest(ctx, opts)
		}
		return runTourGenerate(ctx, root, opts)
	}

	var (
		result   *tour.Result
		generErr error
	)
	if usedTUI && needsAgent {
		title, subtitle := "Regenerating tour", "This can take a moment."
		if latestFlag {
			title = "Fetching the latest dispatch"
		}
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

	// --regenerate dumps the raw agent output verbatim so it can be
	// piped into embedded/tour.md. Skip glamour and the attribution
	// footer so the captured file stays clean markdown.
	if regenerateFlag {
		fmt.Fprintln(w, result.Markdown)
		return nil
	}

	rendered, err := mdrender.RenderForWriterWithOverride(w, result.Markdown, tourHeaderOverride)
	if err != nil {
		// mdrender failed — fall back to raw markdown rather than
		// surfacing a renderer panic to the user.
		rendered = result.Markdown
	}
	fmt.Fprintln(w, rendered)
	if usedTUI && result.DisplayName != "" {
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

// cachedTourSettingsLoader returns a tour.SettingsLoader that closes
// over a single LoadEntireSettings result so ResolveState doesn't
// re-read settings.json a second time per invocation. The previous
// shape of this function unconditionally re-loaded settings, which is
// cheap but not free for a command we want to keep at ~50ms.
func cachedTourSettingsLoader(settings *EntireSettings, loadErr error) tour.SettingsLoader {
	return func(_ context.Context) (bool, bool, error) {
		if loadErr != nil {
			return false, false, loadErr
		}
		return settings.Enabled, true, nil
	}
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
