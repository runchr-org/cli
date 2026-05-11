package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"charm.land/glamour/v2/ansi"

	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/learn"
	"github.com/entireio/cli/cmd/entire/cli/mdrender"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/spf13/cobra"
)

// runLearnGenerate is overridable for tests so they can stub out the agent
// call without touching cobra plumbing.
var runLearnGenerate = learn.Generate

const learnNotGitRepoMessage = "Entire works inside a git repository. Run 'git init' or cd into one and try again."

const learnNoTextGeneratorMessage = `No TextGenerator-capable agent on PATH.

The default 'entire learn' uses a pre-rendered markdown file shipped
with the binary; '--regenerate' calls out to your locally-installed
agent. Install one of: claude, codex, gemini, cursor, copilot, or an
external entire-agent-* plugin that declares text_generator support
— or drop the flag to read the embedded tour.`

// newLearnCmd builds the `entire learn` cobra command. Hidden from
// `entire help` while the feature matures — discoverable via
// `entire labs` and runs normally for users who already know the name.
// Mirrors the registration shape of `entire review`.
func newLearnCmd() *cobra.Command {
	var regenerateFlag bool

	cmd := &cobra.Command{
		Use: "learn",
		// Hidden from `entire help` while the feature is still maturing —
		// users who know about it can still run `entire learn` /
		// `entire learn --help` and the command works normally.
		Hidden: true,
		Short:  "Learn the Entire CLI",
		Long: `Render a state-aware tour of the Entire CLI.

The default tour reads from a pre-rendered markdown file shipped with
the binary, so it returns instantly with no agent or network call. The
content reflects the CLI surface as of the last release; maintainers
re-run with --regenerate during the changelog PR to refresh it.

Labs entry: learn is experimental. We are actively refining it based
on user feedback.

Examples:
  entire learn`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeLearn(cmd.Context(), cmd.OutOrStdout(), cmd.Root(), regenerateFlag)
		},
	}
	cmd.Flags().BoolVar(&regenerateFlag, "regenerate", false, "Force the agent-driven path and write the result to stdout (for refreshing the embedded tour during the changelog PR)")
	if err := cmd.Flags().MarkHidden("regenerate"); err != nil {
		panic(fmt.Sprintf("hide regenerate flag: %v", err))
	}
	return cmd
}

func executeLearn(ctx context.Context, w io.Writer, root *cobra.Command, regenerateFlag bool) error {
	loadedSettings, settingsErr := LoadEntireSettings(ctx)
	// settings.Load returns a non-nil EntireSettings with default values
	// even when no settings.json exists, so isSetUp can't be inferred from
	// loadErr alone — we have to ask whether the files are actually on
	// disk. Bugbot pass-2 flagged the previous always-true return.
	isSetUp := settings.IsSetUpAny(ctx)
	configuredProvider, configuredModel := "", ""
	if settingsErr == nil && loadedSettings.SummaryGeneration != nil {
		configuredProvider = loadedSettings.SummaryGeneration.Provider
		configuredModel = loadedSettings.SummaryGeneration.Model
	}

	opts := learn.Options{
		LoadSettings:        cachedLearnSettingsLoader(loadedSettings, isSetUp, settingsErr),
		ListInstalledAgents: GetAgentsWithHooksInstalled,
		ConfiguredProvider:  configuredProvider,
		SummarizeModel:      configuredModel,
		Labs:                labsRegistryForLearn(),
		Regenerate:          regenerateFlag,
	}

	usedTUI := interactive.IsTerminalWriter(w) && !IsAccessibleMode()

	generate := func(ctx context.Context) (*learn.Result, error) {
		return runLearnGenerate(ctx, root, opts)
	}

	var (
		result   *learn.Result
		generErr error
	)
	if usedTUI && regenerateFlag {
		result, generErr = runLearnTUI(ctx, w, "Regenerating embedded learn markdown", "This can take a moment.", generate)
		if errors.Is(generErr, errLearnCancelled) {
			return nil
		}
	} else {
		result, generErr = generate(ctx)
	}
	if generErr != nil {
		return translateLearnError(w, generErr)
	}

	// --regenerate dumps the raw agent output verbatim so it can be
	// piped into embedded/learn.md. Skip glamour and the attribution
	// footer so the captured file stays clean markdown.
	if regenerateFlag {
		fmt.Fprintln(w, result.Markdown)
		return nil
	}

	rendered, err := mdrender.RenderForWriterWithOverride(w, result.Markdown, learnHeaderOverride)
	if err != nil {
		// mdrender failed — fall back to raw markdown rather than
		// surfacing a renderer panic to the user. Surface a one-line
		// breadcrumb to stderr so the failure isn't fully silent; the
		// user still gets readable (if uncolored) output above.
		fmt.Fprintf(os.Stderr, "learn: render fallback (%v)\n", err)
		rendered = result.Markdown
	}
	fmt.Fprintln(w, rendered)
	if usedTUI && result.DisplayName != "" {
		fmt.Fprintf(w, "(rendered by %s)\n", result.DisplayName)
	}
	return nil
}

// translateLearnError converts learn.Generate errors into user-facing
// output. ErrNotGitRepo and ErrNoTextGenerator are printed directly to
// w with their multi-line message and a short SilentError is returned
// so cobra/main don't reprint the error themselves. Anything else
// propagates to cobra's normal error path.
func translateLearnError(w io.Writer, err error) error {
	if errors.Is(err, learn.ErrNotGitRepo) {
		fmt.Fprintln(w, learnNotGitRepoMessage)
		return NewSilentError(errors.New("not a git repository"))
	}
	if errors.Is(err, learn.ErrNoTextGenerator) {
		fmt.Fprintln(w, learnNoTextGeneratorMessage)
		return NewSilentError(errors.New("no TextGenerator agent on PATH"))
	}
	return err
}

// cachedLearnSettingsLoader returns a learn.SettingsLoader that closes
// over a single LoadEntireSettings result + the resolved isSetUp
// flag, so ResolveState doesn't re-read settings.json (or stat the
// settings files) a second time per invocation.
//
// isSetUp must be passed in (rather than derived from loadErr) because
// settings.Load returns a non-nil EntireSettings with default values
// even when no settings.json exists. The caller resolves it via
// settings.IsSetUpAny.
func cachedLearnSettingsLoader(s *EntireSettings, isSetUp bool, loadErr error) learn.SettingsLoader {
	return func(_ context.Context) (bool, bool, error) {
		if loadErr != nil {
			return false, false, loadErr
		}
		return s.Enabled, isSetUp, nil
	}
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
