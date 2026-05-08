package tour

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/spf13/cobra"
)

// Options bundles the dependencies Generate needs from the cli package.
// Passing them in as values keeps this package importable without a cycle.
type Options struct {
	// LoadSettings returns (enabled, isSetUp, err) — the same pair the
	// cli package's LoadEntireSettings + IsSetUpAny produce.
	LoadSettings SettingsLoader

	// ListInstalledAgents returns the registered agents whose hooks are
	// installed in this repo. Cli supplies its GetAgentsWithHooksInstalled.
	ListInstalledAgents AgentInstallChecker

	// ConfiguredProvider is the optional pinned summary provider name from
	// settings. Empty means "auto-pick the first eligible agent".
	ConfiguredProvider string

	// SummarizeModel is the model hint to pass to the TextGenerator.
	// Empty means "use the provider CLI's default".
	SummarizeModel string

	// Labs is the cli's experimental-commands registry, surfaced under the
	// rendered Labs section. Cli builds this slice from its own
	// experimentalCommands list — passing it through keeps the tour
	// package free of cli imports while still giving the agent enough
	// information to talk about commands like 'entire review' that are
	// Hidden in the cobra tree.
	Labs []LabsCommand

	// Regenerate forces the agent-driven path even when the embedded
	// tour is available. Used by the `--regenerate` maintainer flag to
	// produce the markdown that gets committed back into
	// embedded/tour.md before each release.
	Regenerate bool
}

// Result is the markdown returned by an agent plus enough context for the
// caller to attribute the rendering ("rendered by Claude Code") in its UI.
type Result struct {
	Markdown    string
	DisplayName string
}

// ErrNotGitRepo is returned when Generate is called outside a git
// repository. Callers translate it to a friendly user message.
var ErrNotGitRepo = errors.New("entire tour: not a git repository")

// GenerateLatest fetches the latest entry from the entire.io blog feed
// and asks the configured TextGenerator to summarize it. Unlike Generate,
// this does not require a git repo or any session history — it's a
// pure "what's new in the CLI" call. Returns the raw markdown.
func GenerateLatest(ctx context.Context, opts Options) (*Result, error) {
	choice, err := ResolveTextGenerator(ctx, opts.ConfiguredProvider)
	if err != nil {
		return nil, err
	}
	post, err := FetchLatestBlogPost(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch blog feed: %w", err)
	}
	prompt, err := BuildLatestPrompt(post)
	if err != nil {
		return nil, err
	}
	rendered, err := choice.Generator.GenerateText(ctx, prompt, opts.SummarizeModel)
	if err != nil {
		return nil, fmt.Errorf("summarize latest blog post with %s: %w", choice.DisplayName, err)
	}
	return &Result{
		Markdown:    rendered,
		DisplayName: choice.DisplayName,
	}, nil
}

// Generate is the headless entry point: classify the repo, then return
// the right tour for the user's stage. By default the workflow / first-
// capture stages serve from the embedded pre-rendered markdown
// (instant, deterministic across users for a given CLI version), and
// the setup / agent-install stages render hand-written prose. Pass
// Options.Regenerate=true to force the agent-driven path — used by
// the maintainer-only `--regenerate` flag to produce the markdown
// that gets committed back into embedded/tour.md before a release.
func Generate(ctx context.Context, root *cobra.Command, opts Options) (*Result, error) {
	if opts.Regenerate {
		// --regenerate produces the canonical tour that gets committed
		// back into embedded/tour.md and shipped to all users. The
		// embedded markdown is only ever served to first-capture /
		// workflow stages (the setup and agent-install stages render
		// hand-written prose constants), so the regen output should
		// always be authored as if for StageWorkflow regardless of the
		// running repo's actual state. Skipping ResolveState here also
		// lets `--regenerate` succeed in CI checkouts that have no
		// .entire/settings.json — without this, ResolveState routes to
		// StageSetup and the agent produces a 4-line stub that the
		// release-pipeline validation rejects.
		return regenerateFromAgent(ctx, root, opts, State{
			Stage:      StageWorkflow,
			Enabled:    true,
			HasHistory: true,
		})
	}

	state, err := ResolveState(ctx, opts.LoadSettings, opts.ListInstalledAgents)
	if err != nil {
		return nil, err
	}
	if state.Stage == StageNotGitRepo {
		return nil, ErrNotGitRepo
	}

	switch state.Stage {
	case StageNotGitRepo:
		// Already returned ErrNotGitRepo above; this branch is
		// unreachable but listed for the exhaustive-switch lint.
		return nil, ErrNotGitRepo
	case StageSetup:
		return &Result{Markdown: setupPromptText}, nil
	case StageAgentInstall:
		return &Result{Markdown: agentInstallPromptText}, nil
	case StageFirstCapture:
		return &Result{Markdown: embeddedTour + firstCaptureTail}, nil
	case StageWorkflow:
		return &Result{Markdown: embeddedTour}, nil
	}
	return nil, fmt.Errorf("unhandled tour stage %q", state.Stage)
}

// regenerateFromAgent runs the agent-driven generation path.
// Maintainers invoke it via `entire tour --regenerate` before each
// release, then commit the captured markdown to embedded/tour.md.
// Skipped on every normal user invocation so the runtime cost stays
// at "read embedded file + glamour render".
//
// Output is run through stripControlSequences before return: the
// regen output gets piped to disk and embedded in the binary, so a
// compromised agent could otherwise smuggle terminal escapes /
// hyperlinks / title-rewrites into every future `entire tour` user.
func regenerateFromAgent(ctx context.Context, root *cobra.Command, opts Options, state State) (*Result, error) {
	choice, err := ResolveTextGenerator(ctx, opts.ConfiguredProvider)
	if err != nil {
		return nil, err
	}
	surface := Discover(root)
	prompt, err := BuildPrompt(PromptInput{
		State:   state,
		Surface: surface,
		Labs:    opts.Labs,
	})
	if err != nil {
		return nil, err
	}
	rendered, err := choice.Generator.GenerateText(ctx, prompt, opts.SummarizeModel)
	if err != nil {
		return nil, fmt.Errorf("generate tour with %s: %w", choice.DisplayName, err)
	}
	return &Result{
		Markdown:    stripControlSequences(rendered),
		DisplayName: choice.DisplayName,
	}, nil
}

// stripControlSequences removes ANSI escape sequences, OSC sequences,
// and C0/C1 control bytes other than common whitespace (TAB, LF, CR)
// from a markdown string. Used on agent output that gets persisted
// (committed back into embedded/tour.md) or written to a non-TTY
// destination — a compromised agent or feed could otherwise inject
// terminal-rewriting controls that survive into pasted logs and
// user-facing terminals.
//
// Glamour-styled output is unaffected because it isn't run through
// this function — glamour's own ANSI escapes are produced *after*
// this stripping happens, in the cli layer.
func stripControlSequences(s string) string {
	return controlSequencePattern.ReplaceAllString(s, "")
}

// controlSequencePattern matches:
//   - ESC followed by CSI/OSC/private-mode parameters and a final byte
//   - Bare C0 control bytes other than \t \n \r, plus DEL
//   - C1 control codepoints (U+0080-U+009F)
//
// Compiled once at init. Used by stripControlSequences above.
//
// The C1 range is written as - because Go regex requires
// valid UTF-8 input; raw \x80-\x9f are continuation bytes alone and
// trigger a compile-time panic.
var controlSequencePattern = regexp.MustCompile(
	"\x1b\\[[0-?]*[ -/]*[@-~]" + // CSI: ESC [ ... final
		"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC: ESC ] ... BEL or ESC \
		"|\x1b[@-Z\\\\-_]" + // other ESC sequences
		"|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]" + // C0 controls excl. \t \n \r, plus DEL
		"|[\u0080-\u009f]", // C1 controls
)
