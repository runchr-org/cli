package tour

import (
	"context"
	"errors"
	"fmt"

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
	State       State
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
		return nil, fmt.Errorf("generate latest dispatch with %s: %w", choice.DisplayName, err)
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
	state, err := ResolveState(ctx, opts.LoadSettings, opts.ListInstalledAgents)
	if err != nil {
		return nil, err
	}
	if state.Stage == StageNotGitRepo {
		return nil, ErrNotGitRepo
	}

	if opts.Regenerate {
		return regenerateFromAgent(ctx, root, opts, state)
	}

	switch state.Stage {
	case StageSetup:
		return &Result{Markdown: setupPromptText, State: state}, nil
	case StageAgentInstall:
		return &Result{Markdown: agentInstallPromptText, State: state}, nil
	}

	markdown := embeddedTour
	if state.Stage == StageFirstCapture {
		markdown += firstCaptureTail
	}
	return &Result{Markdown: markdown, State: state}, nil
}

// regenerateFromAgent runs the original agent-driven generation path.
// Maintainers invoke it via `entire tour --regenerate` before each
// release, then commit the captured markdown to embedded/tour.md.
// Skipped on every normal user invocation so the runtime cost stays
// at "read embedded file + glamour render".
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
		return nil, fmt.Errorf("regenerate tour with %s: %w", choice.DisplayName, err)
	}
	return &Result{
		Markdown:    rendered,
		DisplayName: choice.DisplayName,
		State:       state,
	}, nil
}
