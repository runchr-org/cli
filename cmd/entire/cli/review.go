package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	git "github.com/go-git/go-git/v6"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/external"
	"github.com/entireio/cli/cmd/entire/cli/agent/skilldiscovery"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/remote"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
	"github.com/spf13/cobra"
)

const pendingReviewMarkerFilename = "review-pending.json"

// agentChoice is one row in the spawn-time picker. Name is the agent
// registry key (used for marker/override); Label is the picker-visible
// string ("<name>   (N skills configured)" or "<name>   (prompt-only)").
type agentChoice struct {
	Name  string
	Label string
}

// runReviewDeps collects the runtime-injectable hooks runReview uses.
// Tests stub fields on this struct to drive branches that would otherwise
// require a real TTY. Production wiring leaves fields nil and defaults
// are resolved inside runReview.
type runReviewDeps struct {
	promptForAgentFn      func(ctx context.Context, eligible []agentChoice) (string, error)
	promptForAgentsFn     func(ctx context.Context, eligible []agentChoice) ([]string, error)
	runMultiAgentFn       func(ctx context.Context, tasks []MultiAgentTask, out io.Writer) (MultiRunResult, error)
	promptForRunContextFn func(ctx context.Context) (string, error)
}

// MultiAgentTask describes one agent's slot in a parallel review run.
// The dispatcher populates all three fields; the orchestrator passes the
// task straight through to HeadlessLauncher.LaunchHeadlessCmd.
type MultiAgentTask struct {
	Agent  agent.HeadlessLauncher // set by dispatcher when routing to multi-agent
	Name   string                 // agent registry key
	Prompt string                 // composed via composeReviewPrompt(cfg, runContext, scope)
	Skills []string               // configured skills for this agent; carried into the marker so adoption can record them per-agent
}

// MultiRunResult is the aggregated result of a multi-agent review. Runs
// contains one entry per task (in task order); Duration is wall-clock
// span (max of per-agent durations, not sum); Cancelled reports whether
// the orchestrator was signal-cancelled during the run.
type MultiRunResult struct {
	Runs      []AgentRunResult
	Duration  time.Duration
	Cancelled bool
}

// AgentRunStatus tracks one agent's lifecycle through a multi-agent
// review run. Transitions are Queued → Running → {Done, Failed,
// Cancelled}; the last three are terminal.
type AgentRunStatus int

const (
	AgentRunQueued AgentRunStatus = iota
	AgentRunRunning
	AgentRunDone
	AgentRunFailed
	AgentRunCancelled
)

// AgentRunResult reports the outcome of a single agent run inside a
// multi-agent review.
type AgentRunResult struct {
	Name           string
	Status         AgentRunStatus
	ExitCode       int
	Duration       time.Duration
	FinalOutput    []byte
	Err            error
	TokenUsage     *agent.TokenUsage
	TranscriptPath string
}

// PendingReviewMarker is written by `entire review` before spawning the agent.
// The next agent session's UserPromptSubmit hook reads it, tags the session
// kind/review-skills, then clears the marker (so a second review run doesn't
// inherit state from the first).
//
// WorktreePath scopes the marker to the worktree `entire review` was invoked
// from: multiple worktrees in one repo share .git/entire-sessions/, so without
// this field any session in any worktree could race to claim the marker. A
// blank WorktreePath (pre-fix markers) falls back to the legacy unscoped
// behavior — any session can adopt.
type PendingReviewMarker struct {
	AgentName string `json:"agent_name,omitempty"`
	// AgentNames is set by the multi-agent orchestrator. Any agent whose
	// name appears in this list is allowed to adopt the marker (tagging
	// its session as a review); the orchestrator — not the adopting hook
	// — owns clearing the marker once the parallel run finishes.
	AgentNames []string `json:"agent_names,omitempty"`
	Skills     []string `json:"skills"`
	// Prompt is the composed review prompt the agent will receive.
	// Stored on the marker (rather than recomputed on adoption) so session
	// metadata records exactly what the agent was asked to do.
	Prompt string `json:"prompt,omitempty"`
	// AgentEntries carries per-agent skills and prompts for multi-agent
	// runs. Each agent's hook looks up its own entry by agent name on
	// adoption so its session records the skills+prompt it actually ran,
	// not the union or an arbitrary representative. Single-agent markers
	// leave this empty and use the top-level Skills/Prompt fields.
	AgentEntries map[string]AgentMarkerEntry `json:"agent_entries,omitempty"`
	StartingSHA  string                      `json:"starting_sha"`
	StartedAt    time.Time                   `json:"started_at"`
	WorktreePath string                      `json:"worktree_path,omitempty"`
}

// AgentMarkerEntry is the per-agent payload inside a multi-agent
// PendingReviewMarker. Adoption copies these onto the agent's session
// state so checkpoint metadata reflects what each agent actually ran.
type AgentMarkerEntry struct {
	Skills []string `json:"skills,omitempty"`
	Prompt string   `json:"prompt,omitempty"`
}

func pendingMarkerPath(ctx context.Context) (string, error) {
	commonDir, err := session.GetGitCommonDir(ctx)
	if err != nil {
		return "", fmt.Errorf("locate git common dir: %w", err)
	}
	return filepath.Join(commonDir, session.SessionStateDirName, pendingReviewMarkerFilename), nil
}

// WritePendingReviewMarker persists the marker. Overwrites any existing marker.
func WritePendingReviewMarker(ctx context.Context, m PendingReviewMarker) error {
	path, err := pendingMarkerPath(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	return nil
}

// ReadPendingReviewMarker returns the marker if one exists.
// ok=false with err=nil indicates "no pending review."
func ReadPendingReviewMarker(ctx context.Context) (PendingReviewMarker, bool, error) {
	path, err := pendingMarkerPath(ctx)
	if err != nil {
		return PendingReviewMarker{}, false, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from git dir
	if errors.Is(err, os.ErrNotExist) {
		return PendingReviewMarker{}, false, nil
	}
	if err != nil {
		return PendingReviewMarker{}, false, fmt.Errorf("read marker: %w", err)
	}
	var m PendingReviewMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return PendingReviewMarker{}, false, fmt.Errorf("parse marker: %w", err)
	}
	return m, true, nil
}

// ClearPendingReviewMarker removes the marker. Missing file is not an error.
func ClearPendingReviewMarker(ctx context.Context) error {
	path, err := pendingMarkerPath(ctx)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove marker: %w", err)
	}
	return nil
}

// Curated built-ins and install hints live in package skilldiscovery.

// sameWorktreePath compares two worktree paths after filepath.Clean. Both
// paths come from paths.WorktreeRoot, so an exact-bytes match is expected in
// the common case; Clean is defense-in-depth against trailing slashes and
// duplicate separators.
func sameWorktreePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// adoptPendingReviewMarkerInto reads any pending review marker and applies it
// to the given session state. Returns (newState, modified, error). If the
// state already has Kind set (e.g., a subsequent turn of a review session),
// the marker is left in place and modified=false — adoption only happens on
// first tag. The marker is cleared on successful first adoption.
//
// Scoping rules — the marker is left untouched (not cleared) when a scope
// mismatch still leaves open the possibility that a future session could
// legitimately claim it:
//
//   - WorktreePath: a Claude session in worktree A must not claim a marker
//     meant for a session `entire review` spawned in worktree B. Both
//     worktrees share the same .git/entire-sessions/ directory.
//   - AgentName: a cursor session must not claim a marker that records a
//     claude-code review — the review config and skills are agent-specific,
//     and whichever session fires its UserPromptSubmit hook first would
//     otherwise silently steal the wrong agent's review metadata.
//
// StartingSHA is different: once HEAD moves past the marker's commit, no
// future session will meaningfully match the original review intent.
// A stale marker is cleared rather than left to mis-tag a later unrelated
// session.
//
// Pre-fix markers with empty fields fall back to unscoped adoption for
// each missing field (backwards compat).
func adoptPendingReviewMarkerInto(ctx context.Context, s session.State, agentName types.AgentName) (session.State, bool, error) {
	// Already tagged — don't re-apply on subsequent turns.
	if s.Kind != "" {
		return s, false, nil
	}
	m, ok, err := ReadPendingReviewMarker(ctx)
	if err != nil {
		return s, false, err
	}
	if !ok {
		return s, false, nil
	}
	if m.WorktreePath != "" && !sameWorktreePath(m.WorktreePath, s.WorktreePath) {
		// Marker belongs to a different worktree — leave it for the session
		// `entire review` actually spawned, which will reach its own hook
		// and claim the marker.
		return s, false, nil
	}
	// Multi-agent markers (written by multiReviewOrchestrator) carry a
	// list of eligible agent names. Any agent in the list may adopt, but
	// the orchestrator — not the adopting hook — owns clearing the marker
	// once the whole parallel run exits.
	multiAgent := len(m.AgentNames) > 0
	if multiAgent {
		inList := false
		for _, n := range m.AgentNames {
			if n == string(agentName) {
				inList = true
				break
			}
		}
		if !inList {
			// Agent not in the orchestrator's list — leave marker so the
			// other configured agents' hooks can still adopt.
			return s, false, nil
		}
	} else if m.AgentName != "" && m.AgentName != string(agentName) {
		// Marker was written for a different agent — leave it alone. The
		// correct agent's session will reach its own hook and claim the
		// marker.
		return s, false, nil
	}
	// SHA drift: the marker was written for a specific commit. If HEAD has
	// moved since, the user's intent (review THAT commit) no longer applies
	// to this session, and we'd otherwise silently tag an unrelated session
	// as a review. Discard the stale marker rather than adopting it or
	// leaving it in place to mis-tag a later session.
	//
	// Failure to resolve HEAD is non-fatal: adoption is best-effort, and
	// crashing a legitimate review because git rev-parse hiccupped would be
	// worse than skipping the check.
	if m.StartingSHA != "" {
		headSHA, headErr := currentHeadSHA(ctx)
		switch {
		case headErr != nil:
			logging.Debug(ctx, "adopt marker: resolve HEAD failed, skipping SHA check",
				slog.String("error", headErr.Error()))
		case headSHA != m.StartingSHA:
			logging.Warn(ctx, "adopt marker: HEAD moved since marker was written; discarding stale marker",
				slog.String("marker_sha", m.StartingSHA),
				slog.String("head_sha", headSHA))
			if clearErr := ClearPendingReviewMarker(ctx); clearErr != nil {
				logging.Debug(ctx, "failed to clear stale marker", slog.String("error", clearErr.Error()))
			}
			return s, false, nil
		}
	}
	s.Kind = session.KindAgentReview
	// Per-agent lookup wins for multi-agent markers — each agent's
	// session records the skills/prompt it actually ran, not a union or
	// representative. Top-level Skills/Prompt is the single-agent path
	// and the fallback when an entry is missing (legacy markers, or a
	// new agent whose hook fired without a registered entry).
	if entry, ok := m.AgentEntries[string(agentName)]; ok && multiAgent {
		s.ReviewSkills = entry.Skills
		s.ReviewPrompt = entry.Prompt
	} else {
		s.ReviewSkills = m.Skills
		s.ReviewPrompt = m.Prompt
	}
	if multiAgent {
		// The orchestrator owns marker cleanup for multi-agent runs; a per-
		// hook clear here would race the sibling agents still waiting to
		// adopt.
		return s, true, nil
	}
	if err := ClearPendingReviewMarker(ctx); err != nil {
		// Tagging succeeded; leftover marker self-heals on next session start
		// (since Kind is now set, the next turn will return modified=false
		// and the marker will be re-cleared on any next review session).
		logging.Warn(ctx, "failed to clear pending review marker", slog.String("error", err.Error()))
	}
	return s, true, nil
}

// confirmFirstRunSetup prints a banner framing the picker as first-run
// setup (rather than the review itself) and waits for the user to confirm.
// Returns false if the user cancels; caller should bail gracefully.
//
// Signposting matters here because `entire review` with no config silently
// drops into the picker — users running the command to start a review can
// mistake the picker for the review. The banner + confirmation makes the
// setup phase explicit, and the trailing "running review now" line in the
// caller closes the loop on what comes next.
func confirmFirstRunSetup(out io.Writer) bool {
	fmt.Fprintln(out, "No review config found — let's set one up first.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "You'll pick skills for each installed agent. They're saved to")
	fmt.Fprintln(out, ".entire/settings.json; edit later with `entire review --edit`.")
	fmt.Fprintln(out, "After setup, the review will run with your selection.")
	fmt.Fprintln(out)

	proceed := true
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Set up review skills now?").
			Affirmative("Yes").
			Negative("Cancel").
			Value(&proceed),
	))
	if err := form.Run(); err != nil {
		fmt.Fprintln(out, "Setup cancelled.")
		return false
	}
	if !proceed {
		fmt.Fprintln(out, "Setup cancelled.")
	}
	return proceed
}

// runReviewConfigPicker presents a huh multi-select for each installed agent
// that has curated review skills, and saves the selection to
// .entire/settings.json. Previously-saved skills are pre-checked via
// huh.Option.Selected(true), mirroring how `entire enable` preserves prior
// selections in its own agent picker.
func runReviewConfigPicker(ctx context.Context, out io.Writer) (map[string]settings.ReviewConfig, error) {
	installed := GetAgentsWithHooksInstalled(ctx)
	if len(installed) == 0 {
		return nil, errors.New(
			"no agents with hooks installed; " +
				"run 'entire configure --agent <name>' to install hooks for one, " +
				"or 'entire enable' to set up the repo",
		)
	}

	// Narrow to agents that have a curated skills list; others need manual
	// editing of settings.json under review.<agent-name>.
	type configurableAgent struct {
		name types.AgentName
		ag   agent.Agent
	}
	var configurable []configurableAgent
	for _, name := range installed {
		if !skilldiscovery.IsEligible(string(name)) {
			continue
		}
		ag, err := agent.Get(name)
		if err != nil {
			continue
		}
		configurable = append(configurable, configurableAgent{name: name, ag: ag})
	}
	if len(configurable) == 0 {
		return nil, errors.New(
			"no installed agents have curated review skills; " +
				"edit .entire/settings.json directly under review.<agent-name>",
		)
	}

	// Load existing config so we can pre-check saved skills and seed saved
	// prompts. A load error here means the settings file is malformed; log
	// at Warn so users debugging "my saved skills aren't pre-checked" can
	// see why, but keep going with an empty prefill — runReview already
	// surfaces the same error distinctly when it's the first load.
	existing := map[string]settings.ReviewConfig{}
	if s, err := settings.Load(ctx); err != nil {
		logging.Warn(ctx, "settings.Load failed when pre-filling picker", slog.String("error", err.Error()))
	} else if s != nil {
		existing = s.Review
	}

	// Up-front header: make the order and count obvious so users can spot
	// when an agent they expected isn't being offered (e.g., hooks not
	// installed for it yet).
	labels := make([]string, 0, len(configurable))
	for _, c := range configurable {
		labels = append(labels, string(c.ag.Type()))
	}
	fmt.Fprintf(out, "Configuring review for %d agent(s): %s\n", len(configurable), strings.Join(labels, ", "))
	fmt.Fprintln(out, "(Previously-saved skills are pre-checked. Space to toggle, enter to confirm.)")
	fmt.Fprintln(out)

	selected := map[string]settings.ReviewConfig{}
	for i, c := range configurable {
		curated := skilldiscovery.CuratedBuiltinsFor(string(c.name))

		// Discover + dedupe + filter hints.
		var discovered []agent.DiscoveredSkill
		if d, ok := c.ag.(agent.SkillDiscoverer); ok {
			if ds, dErr := d.DiscoverReviewSkills(ctx); dErr == nil {
				discovered = ds
			} else {
				logging.Debug(ctx, "review discovery failed",
					slog.String("agent", string(c.name)), slog.String("error", dErr.Error()))
			}
		}
		builtinNames := builtinNameSet(curated)
		discovered = filterOutBuiltinCollisions(discovered, builtinNames)

		discoveredSet := make(map[string]struct{}, len(discovered))
		for _, d := range discovered {
			discoveredSet[d.Name] = struct{}{}
		}
		activeHints := skilldiscovery.ActiveInstallHintsFor(string(c.name), discoveredSet)

		// Pre-populate pick slices from saved config so the picker preselects
		// them. The header promises "previously-saved skills are pre-checked";
		// without this split + Option.Selected(true) in buildReviewPickerFields,
		// --edit with accept-defaults silently wipes the agent's saved skills.
		builtinPicks, discoveredPicks := splitSavedPicks(
			existing[string(c.name)].Skills, curated, discovered,
		)
		prompt := existing[string(c.name)].Prompt

		fields := buildReviewPickerFields(
			string(c.name), curated, discovered, activeHints, prompt,
			&builtinPicks, &discoveredPicks, &prompt,
		)

		// Prepend a non-blocking header Note so the agent being configured
		// is always clearly visible. huh.Note defaults to skip:true — it
		// renders above the first interactive field but doesn't pause the
		// form. The previous attempt (type-asserting the first field's
		// Title method) silently did nothing because huh's concrete Title
		// methods return *MultiSelect[T]/*Text/*Note, not huh.Field.
		header := huh.NewNote().
			Title(string(c.ag.Type())).
			Description(fmt.Sprintf("Agent %d of %d · pick review skills and optional instructions", i+1, len(configurable)))
		fields = append([]huh.Field{header}, fields...)

		form := NewAccessibleForm(huh.NewGroup(fields...))
		if err := form.Run(); err != nil {
			return nil, fmt.Errorf("picker for %s: %w", c.name, err)
		}

		cfg := settings.ReviewConfig{
			Skills: dedupeStrings(append(builtinPicks, discoveredPicks...)),
			Prompt: strings.TrimSpace(prompt),
		}
		if !cfg.IsZero() {
			selected[string(c.name)] = cfg
		}
	}
	// Merge the picker's output with existing entries the picker could not
	// surface. Without the merge, save would replace s.Review wholesale and
	// silently drop entries the user had configured for external agents,
	// uncurated agents, or agents whose hooks are temporarily uninstalled —
	// exactly the "edit settings.json manually" case the help text suggests.
	offered := make(map[string]struct{}, len(configurable))
	for _, c := range configurable {
		offered[string(c.name)] = struct{}{}
	}
	merged := mergePickerResults(existing, offered, selected)

	// The emptiness check runs on `merged`, not `selected`: a user
	// deliberately deselecting all curated agents while keeping existing
	// external-agent entries is a valid outcome that must be saveable. Only
	// refuse if the final config would be empty — i.e., no picks/prompt
	// AND no pre-existing entries to preserve.
	if len(merged) == 0 {
		return nil, errors.New("no review skills or prompt configured")
	}

	if err := saveReviewConfig(ctx, merged); err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "Saved review config to .entire/settings.json. Edit directly or run `entire review --edit`.")
	return merged, nil
}

// mergePickerResults combines the picker's output with existing review
// config entries that the picker did not surface. Agents in `offered` are
// fully controlled by the picker: if they appear in `selected` with a
// non-zero config the entry is set, otherwise the entry is removed.
// Agents not in `offered` keep their existing config untouched.
//
// Exposed as a helper so tests can drive it directly — the picker itself
// can't run headless.
func mergePickerResults(existing map[string]settings.ReviewConfig, offered map[string]struct{}, selected map[string]settings.ReviewConfig) map[string]settings.ReviewConfig {
	merged := make(map[string]settings.ReviewConfig, len(existing)+len(selected))
	for name, cfg := range existing {
		if _, wasOffered := offered[name]; !wasOffered {
			merged[name] = cfg
		}
	}
	for name, cfg := range selected {
		merged[name] = cfg
	}
	return merged
}

func newReviewCmd() *cobra.Command {
	return newReviewCmdWithDeps(runReviewDeps{})
}

// newReviewCmdWithDeps returns the review subcommand wired against the
// provided deps. Tests use this constructor directly to inject stubs for
// branches that would otherwise require a real TTY. Callers should pass
// runReviewDeps{} in production; runReview fills in defaults for any nil
// fields.
func newReviewCmdWithDeps(deps runReviewDeps) *cobra.Command {
	var edit bool
	var agentOverride string

	cmd := &cobra.Command{
		Use: "review",
		// Hidden from `entire help` while the feature is still maturing —
		// users who know about it can still run `entire review` / `entire
		// review --help` and the command works normally. Remove when we're
		// ready to advertise it in the top-level command list.
		Hidden: true,
		Short:  "Run configured review skills against the current branch",
		Long: `Run the review skills configured in .entire/settings.json against
the current branch. On first run, an interactive picker writes the config.

The review session is recorded as part of the next checkpoint, so the
review metadata is permanently attached to the commit it covers.

Flags:
  --edit         re-open the review config picker
  --agent NAME   select a specific configured agent when more than one is
                 configured (default: alphabetically first)

Subcommands:
  attach <id>    tag an existing session as a review (equivalent to
                 'entire attach --review <id>')`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			// Discover external agents so review configs that target them
			// resolve correctly — without this, GetAgentsWithHooksInstalled
			// and agent.Get can't see them, and `entire review` on an
			// external-agent config fails even though hooks and `review
			// attach` work. Mirrors the call in newReviewAttachCmd for
			// consistent behavior across review entry points.
			external.DiscoverAndRegister(ctx)
			if edit {
				_, err := runReviewConfigPicker(ctx, cmd.OutOrStdout())
				return err
			}
			return runReview(ctx, cmd, agentOverride, deps)
		},
	}
	cmd.Flags().BoolVar(&edit, "edit", false, "re-open the review config picker")
	cmd.Flags().StringVar(&agentOverride, "agent", "", "select a specific configured agent (default: alphabetically first)")
	cmd.AddCommand(newReviewAttachCmd())
	return cmd
}

// newReviewAttachCmd is a thin wrapper around `entire attach --review`. It
// shares all wiring with runAttach; only the UX surface differs, letting
// users discover review-attach through `entire review` in help output.
func newReviewAttachCmd() *cobra.Command {
	var (
		force      bool
		agentFlag  string
		skillsFlag []string
	)
	cmd := &cobra.Command{
		Use:   "attach <session-id>",
		Short: "Tag an existing agent session as a review",
		Long: `Tag an existing agent session as an agent_review and link it to
the current commit's checkpoint. Use this when you ran a review manually
(without 'entire review') and want the review metadata attached after
the fact.

The first user prompt in the transcript is recorded as the review
prompt. Pass --skills to declare which skills were actually run; omit
to attach a review without a declared skills list.

Equivalent to 'entire attach --review <session-id>' — provided here for
discoverability alongside the other review subcommands.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmd.Help()
			}
			if checkDisabledGuard(cmd.Context(), cmd.OutOrStdout()) {
				return nil
			}
			// Discover external agents so --agent <external-name> is
			// recognized and auto-detection covers them. Mirrors the
			// contract of `entire attach` so the two entry points stay
			// behaviorally equivalent.
			external.DiscoverAndRegister(cmd.Context())
			return runAttachSurfaceReviewErrors(cmd, args[0], types.AgentName(agentFlag), attachOptions{
				Force:                force,
				Review:               true,
				ReviewSkillsOverride: skillsFlag,
			})
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation and amend the last commit with the checkpoint trailer")
	cmd.Flags().StringVarP(&agentFlag, "agent", "a", string(agent.DefaultAgentName), "Agent that created the session")
	cmd.Flags().StringSliceVar(&skillsFlag, "skills", nil, "Optional: declare which review skills were run in this session")
	return cmd
}

func runReview(ctx context.Context, cmd *cobra.Command, agentOverride string, deps runReviewDeps) error {
	out := cmd.OutOrStdout()

	// 1. Pre-flight: must be in a git repo.
	if _, err := paths.WorktreeRoot(ctx); err != nil {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), "Not a git repository. Run `entire enable` first.")
		return NewSilentError(errors.New("not a git repository"))
	}

	// 2. Load config. A load error means the settings file exists but is
	// malformed (Load returns a default-filled object when the file is
	// missing). Surface the error instead of silently opening the picker,
	// which would cause saveReviewConfig to write over the user's other
	// settings with an empty EntireSettings{}.
	s, err := settings.Load(ctx)
	if err != nil {
		cmd.SilenceUsage = true
		fmt.Fprintf(cmd.ErrOrStderr(), "Failed to load settings: %v\n", err)
		fmt.Fprintln(cmd.ErrOrStderr(), "Fix `.entire/settings.json` and re-run `entire review`.")
		return NewSilentError(err)
	}
	if s == nil || len(s.Review) == 0 {
		if !confirmFirstRunSetup(out) {
			return nil
		}
		picked, pickErr := runReviewConfigPicker(ctx, out)
		if pickErr != nil {
			return pickErr
		}
		if s == nil {
			s = &settings.EntireSettings{}
		}
		s.Review = picked
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Setup complete — running review now.")
	}

	// 3. Pick agent. When multiple agents are configured with hooks installed
	// and no --agent override is provided, prompt the user to pick one (or
	// many, for multi-agent review). Skipped when --agent is passed, and
	// skipped when only one eligible agent is configured (selectReviewAgent's
	// alphabetical default handles the single-agent case deterministically).
	if agentOverride == "" {
		override, dispatched, pickErr := pickAgentForReview(ctx, cmd, s, deps)
		if dispatched {
			// Multi-agent orchestrator took over; nothing more for runReview
			// to do. Propagate any error verbatim (may be nil).
			return pickErr
		}
		if pickErr != nil {
			return pickErr
		}
		agentOverride = override
	}

	agentName, cfg, err := selectReviewAgent(s.Review, agentOverride)
	if err != nil {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return NewSilentError(err)
	}

	// 3.5. Verify hooks are installed for the selected agent. Without the
	// agent's lifecycle hooks firing, UserPromptSubmit will never adopt the
	// pending marker and the review metadata will never be recorded — a
	// silent failure mode. Stale config (e.g. user ran `entire disable`
	// without removing the agent from review settings) hits this same path.
	if !slices.Contains(GetAgentsWithHooksInstalled(ctx), types.AgentName(agentName)) {
		cmd.SilenceUsage = true
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Hooks are not installed for %q. Run `entire configure --agent %s` first, "+
				"or remove %q from review settings.\n",
			agentName, agentName, agentName)
		return NewSilentError(fmt.Errorf("hooks not installed for %s", agentName))
	}

	// 3.6. Verify configured skills are actually installed on disk. Catches
	// the hand-edited-settings.json case where the user named a skill that
	// doesn't exist; without this guard the agent would spawn and fail
	// silently with "I don't have that skill".
	ag, agErr := agent.Get(types.AgentName(agentName))
	if agErr != nil {
		return fmt.Errorf("resolve agent %s: %w", agentName, agErr)
	}
	if err := verifyConfiguredSkillsInstalled(ctx, ag, cfg); err != nil {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return NewSilentError(err)
	}

	// 4. Re-run guard: check if HEAD's checkpoint already has a review.
	if reviewed, meta := headHasReviewCheckpoint(ctx); reviewed {
		var proceed bool
		form := NewAccessibleForm(huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Already reviewed: %s. Proceed anyway?", meta)).
				Value(&proceed),
		))
		if err := form.Run(); err != nil {
			fmt.Fprintln(out, "prompt cancelled")
			return err //nolint:wrapcheck // propagate huh cancellation
		}
		if !proceed {
			fmt.Fprintln(out, "Review cancelled.")
			return nil
		}
	}

	// 5. Resolve HEAD + worktree root for the pending marker. WorktreePath
	// scopes the marker so that a session in *another* worktree sharing the
	// same .git/ can't race to claim it.
	headSHA, err := currentHeadSHA(ctx)
	if err != nil {
		return fmt.Errorf("resolve HEAD: %w", err)
	}
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return fmt.Errorf("resolve worktree root: %w", err)
	}

	// 5.5. Prompt the user for per-run context (optional). Fires between
	// agent selection and marker write so the ephemeral context text is
	// composed into the prompt the session's hook records. Empty/Enter-
	// only is treated as "skip" — base prompt is used verbatim.
	runContext, err := resolveRunContext(ctx, cmd, deps)
	if err != nil {
		return err
	}

	// 6. Compose the review prompt once, then write the pending marker. The
	// composed prompt is carried on the marker so adoption records exactly
	// what the agent was asked to do (the same string passed to LaunchCmd
	// below), rather than recomposing on the hook side. When the user has
	// configured a custom Prompt, it wins verbatim; otherwise Skills are
	// composed into the default "run these in order" template. The scope
	// clause pins agents to "commits unique to this branch vs the closest
	// ancestor branch," excluding work inherited from sibling branches.
	prompt := composeReviewPrompt(cfg, runContext, reviewPromptScope{
		Branch:  currentBranch(ctx),
		BaseRef: detectScopeBaseRef(ctx, worktreeRoot),
	})
	if err := WritePendingReviewMarker(ctx, PendingReviewMarker{
		AgentName:    agentName,
		Skills:       cfg.Skills,
		Prompt:       prompt,
		StartingSHA:  headSHA,
		StartedAt:    time.Now().UTC(),
		WorktreePath: worktreeRoot,
	}); err != nil {
		return fmt.Errorf("write pending marker: %w", err)
	}

	// 7. Resolve launcher BEFORE installing the cleanup defer. Non-launchable
	// agents (cursor, opencode, factoryai-droid, etc.) can't be spawned as
	// subprocesses, so the marker must persist on disk for the user's
	// manually-started session to adopt. If we registered the defer first,
	// it would wipe the marker on this return path, silently breaking the
	// hand-off the message promises.
	launcher, ok := agent.LauncherFor(types.AgentName(agentName))
	if !ok {
		fmt.Fprintf(out, "%s does not support subprocess launch yet. Marker written.\n", agentName)
		fmt.Fprintf(out, "Start %s manually and use this prompt:\n\n%s\n", agentName, prompt)
		return nil
	}

	// From this point on, the marker lives on disk until either (a) the
	// spawned agent's hook adopts and clears it, or (b) we clear it here
	// as a fallback. The defer covers every spawn/launch/run failure path,
	// and also the case where the agent exits cleanly without ever firing
	// UserPromptSubmit (e.g. user `/quit`s immediately). Leaving the marker
	// in those cases would mis-tag the next unrelated session as a review.
	defer func() {
		_, exists, readErr := ReadPendingReviewMarker(ctx)
		if readErr != nil || !exists {
			return
		}
		if clearErr := ClearPendingReviewMarker(ctx); clearErr != nil {
			logging.Debug(ctx, "cleanup unadopted review marker", slog.String("error", clearErr.Error()))
		}
	}()
	// Best-effort: show the user what's in scope so they can tell whether
	// the review target is what they expected. Failures are silent — scope
	// is informational, not load-bearing.
	if scope, scopeErr := detectReviewScope(ctx); scopeErr == nil {
		fmt.Fprintln(out, formatReviewScope(scope))
	}
	execCmd, err := launcher.LaunchCmd(ctx, prompt)
	if err != nil {
		return fmt.Errorf("launch %s: %w", agentName, err)
	}
	if err := execCmd.Run(); err != nil {
		return fmt.Errorf("agent exited: %w", err)
	}
	return nil
}

// pickAgentForReview runs the agent-selection picker. Returns:
//
//   - override: the chosen agent name for the single-agent spawn path,
//     empty if no picker fired (caller falls through to selectReviewAgent's
//     alphabetical default).
//   - dispatched: true when the multi-agent orchestrator has already been
//     invoked; caller should return immediately.
//   - err: picker error or a silent-error shell used to print a user-facing
//     message without a usage dump.
//
// Dispatch order:
//
//   - If 2+ eligible agents implement HeadlessLauncher → multi-select
//     picker. Selecting 1 falls through to single-agent path; selecting
//     2+ routes to the orchestrator.
//   - Otherwise, if 2+ eligible → fall back to the 2.1 single-select.
//   - Otherwise, single-eligible case is handled by selectReviewAgent in
//     the caller (alphabetically first, deterministic).
func pickAgentForReview(ctx context.Context, cmd *cobra.Command, s *settings.EntireSettings, deps runReviewDeps) (override string, dispatched bool, err error) {
	eligibleConfigured := computeEligibleConfigured(ctx, s)

	// Filter to agents implementing HeadlessLauncher. The multi-agent
	// picker only offers these, because the orchestrator spawns them
	// headlessly. Single-agent fallback below operates on the full
	// eligible set.
	var runnable []agentChoice
	for _, e := range eligibleConfigured {
		ag, getErr := agent.Get(types.AgentName(e.Name))
		if getErr != nil {
			continue // already reported by earlier gates
		}
		if _, ok := ag.(agent.HeadlessLauncher); ok {
			runnable = append(runnable, e)
		}
	}

	switch {
	case len(runnable) >= 2:
		return pickMultiAgent(ctx, cmd, s, runnable, deps)
	case len(eligibleConfigured) > 1:
		return pickSingleAgent(ctx, cmd, eligibleConfigured, deps)
	}
	// <=1 eligible → no picker; selectReviewAgent picks alphabetically-first.
	return "", false, nil
}

// pickMultiAgent runs the multi-select picker over runnable agents. Returns
// override + dispatched=false when the user picks exactly one agent (caller
// continues with single-agent path), dispatched=true when the orchestrator
// has been invoked (caller should return).
func pickMultiAgent(ctx context.Context, cmd *cobra.Command, s *settings.EntireSettings, runnable []agentChoice, deps runReviewDeps) (string, bool, error) {
	fn := deps.promptForAgentsFn
	if fn == nil {
		fn = promptForAgents
	}
	selected, pickErr := fn(ctx, runnable)
	if pickErr != nil {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), pickErr.Error())
		return "", false, NewSilentError(pickErr)
	}
	if len(selected) == 0 {
		// User cancelled. SilenceUsage prevents cobra from dumping help
		// text on a deliberate cancel — the user clicked away from the
		// picker, they don't need usage instructions.
		cmd.SilenceUsage = true
		return "", false, NewSilentError(errors.New("multi-agent picker cancelled"))
	}
	if len(selected) == 1 {
		// Drop into single-agent path.
		return selected[0], false, nil
	}
	// Per-run context prompt fires after selection but before dispatch so
	// the same ephemeral text is composed into every task's prompt.
	runContext, err := resolveRunContext(ctx, cmd, deps)
	if err != nil {
		return "", false, err
	}
	// Route to multi-agent orchestrator. SilenceUsage on dispatch errors
	// matches the single-agent picker path: failure modes here (resolve
	// agent, write marker, launcher capability missing) are runtime
	// problems, not invocation problems, so cobra usage output would be
	// noise.
	if err := dispatchMultiAgent(ctx, cmd, s, selected, runContext, deps); err != nil {
		cmd.SilenceUsage = true
		return "", true, err
	}
	return "", true, nil
}

// pickSingleAgent runs the 2.1 single-select picker over the eligible set.
// Returns override=<picked> when the user picks a valid agent.
func pickSingleAgent(ctx context.Context, cmd *cobra.Command, eligible []agentChoice, deps runReviewDeps) (string, bool, error) {
	fn := deps.promptForAgentFn
	if fn == nil {
		fn = promptForAgent
	}
	picked, pickErr := fn(ctx, eligible)
	if pickErr != nil {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), pickErr.Error())
		return "", false, NewSilentError(pickErr)
	}
	if picked == "" {
		// Defensive: a nil-or-empty return from the picker must NOT fall
		// through to selectReviewAgent's alphabetical-first default. That's
		// exactly the silent-masking shape the picker was added to
		// eliminate. Refuse to proceed.
		cmd.SilenceUsage = true
		emptyErr := errors.New("agent picker returned empty agent name")
		fmt.Fprintln(cmd.ErrOrStderr(), emptyErr.Error())
		return "", false, NewSilentError(emptyErr)
	}
	return picked, false, nil
}

// dispatchMultiAgent composes per-agent tasks, resolves HeadlessLauncher
// capabilities, and hands off to the orchestrator via deps.runMultiAgentFn.
// Marker write, hook adoption, and cleanup are the orchestrator's
// responsibility — this function only prepares the task list and dispatches.
// runContext is the optional per-run text gathered by pickMultiAgent; it is
// composed into every selected agent's prompt alongside the persistent cfg.
func dispatchMultiAgent(ctx context.Context, cmd *cobra.Command, s *settings.EntireSettings, selected []string, runContext string, deps runReviewDeps) error {
	out := cmd.OutOrStdout()
	// Resolve scope once before composing per-agent prompts. Failures are
	// non-fatal — composeReviewPrompt drops the scope clause for any
	// missing field rather than refusing to spawn.
	repoRoot, rootErr := paths.WorktreeRoot(ctx)
	if rootErr != nil {
		logging.Debug(ctx, "multi-agent dispatch: repo root resolve failed; scope clause omitted",
			slog.String("error", rootErr.Error()))
	}
	scope := reviewPromptScope{
		Branch:  currentBranch(ctx),
		BaseRef: detectScopeBaseRef(ctx, repoRoot),
	}
	tasks := make([]MultiAgentTask, 0, len(selected))
	for _, name := range selected {
		cfg := s.Review[name]
		if cfg.IsZero() {
			return fmt.Errorf("internal error: agent %q selected but has no configured review config", name)
		}
		ag, err := agent.Get(types.AgentName(name))
		if err != nil {
			return fmt.Errorf("resolve agent %s: %w", name, err)
		}
		hl, ok := ag.(agent.HeadlessLauncher)
		if !ok {
			return fmt.Errorf("agent %s does not implement HeadlessLauncher (should have been filtered by picker)", name)
		}
		tasks = append(tasks, MultiAgentTask{
			Agent:  hl,
			Name:   name,
			Prompt: composeReviewPrompt(cfg, runContext, scope),
			Skills: cfg.Skills,
		})
	}

	fn := deps.runMultiAgentFn
	if fn == nil {
		worktreeRoot, err := paths.WorktreeRoot(ctx)
		if err != nil {
			return fmt.Errorf("resolve worktree root: %w", err)
		}
		orch := newMultiReviewOrchestrator(worktreeRoot)
		fn = orch.Run
	}
	result, err := fn(ctx, tasks, out)
	if err != nil {
		return err
	}
	// The orchestrator prints its own completion dump + summary line to
	// out; the returned result is informational for test assertions.
	_ = result
	return nil
}

// computeEligibleConfigured returns the sorted list of agents that are both
// configured (non-zero ReviewConfig entry) AND have hooks installed. Only
// eligible agents are valid picker targets — spawning a review for an agent
// without hooks would silently drop the review metadata.
func computeEligibleConfigured(ctx context.Context, s *settings.EntireSettings) []agentChoice {
	if s == nil {
		return nil
	}
	installed := GetAgentsWithHooksInstalled(ctx)
	installedSet := make(map[types.AgentName]struct{}, len(installed))
	for _, name := range installed {
		installedSet[name] = struct{}{}
	}
	out := make([]agentChoice, 0, len(s.Review))
	for name, cfg := range s.Review {
		if cfg.IsZero() {
			continue
		}
		if _, ok := installedSet[types.AgentName(name)]; !ok {
			continue
		}
		out = append(out, agentChoice{Name: name, Label: labelForAgentChoice(name, cfg)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// labelForAgentChoice builds the picker-visible label for an agent row.
// Annotates with "(N skills configured)" when skills are set, "(prompt-only)"
// when only a freeform prompt is configured, and falls back to the bare name
// otherwise (defensive — computeEligibleConfigured filters zero configs out
// before we get here).
func labelForAgentChoice(name string, cfg settings.ReviewConfig) string {
	switch {
	case len(cfg.Skills) > 0:
		return fmt.Sprintf("%s   (%d skills configured)", name, len(cfg.Skills))
	case cfg.Prompt != "":
		return name + "   (prompt-only)"
	default:
		return name
	}
}

// promptForAgent renders the single-select agent picker shown when more than
// one eligible agent is configured. Returns the chosen agent name. Respects
// accessibility mode via NewAccessibleForm.
func promptForAgent(ctx context.Context, eligible []agentChoice) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("agent picker: %w", err)
	}
	if len(eligible) == 0 {
		return "", errors.New("no eligible agents to prompt for")
	}
	options := make([]huh.Option[string], 0, len(eligible))
	for _, c := range eligible {
		options = append(options, huh.NewOption(c.Label, c.Name))
	}
	picked := eligible[0].Name
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Which agent should run this review?").
			Options(options...).
			Value(&picked),
	))
	if err := form.Run(); err != nil {
		return "", fmt.Errorf("agent picker: %w", err)
	}
	return picked, nil
}

// promptForAgents runs a multi-select huh picker over the given eligible
// agents and returns selected names. An empty return is an explicit user
// cancel — caller should exit silently, no marker written.
func promptForAgents(ctx context.Context, eligible []agentChoice) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("multi-agent picker: %w", err)
	}
	if len(eligible) == 0 {
		return nil, errors.New("no eligible agents to prompt for")
	}
	options := make([]huh.Option[string], 0, len(eligible))
	for _, c := range eligible {
		options = append(options, huh.NewOption(c.Label, c.Name))
	}
	var picked []string
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Which agents should run this review?").
			Description("Space to toggle, Enter to confirm. Select 2+ for parallel run; 1 drops into single-agent mode.").
			Options(options...).
			Value(&picked),
	))
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("multi-agent picker: %w", err)
	}
	return picked, nil
}

// promptForRunContext presents a small optional textarea for per-run
// context. Empty return = user skipped (Enter on empty). Never saved to
// settings — ephemeral per run.
func promptForRunContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("run-context prompt: %w", err)
	}
	var text string
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewText().
			Title("Additional context for this review (optional)").
			Description("What are you asking the agent(s) to focus on? Press Enter to skip.").
			Value(&text),
	))
	if err := form.Run(); err != nil {
		return "", fmt.Errorf("run-context prompt: %w", err)
	}
	return strings.TrimSpace(text), nil
}

// resolveRunContext runs the injection-hookable run-context prompt. Used
// by both the single-agent path (in runReview) and the multi-agent path
// (in pickMultiAgent) so the same ephemeral per-run text is composed
// into every selected agent's prompt.
func resolveRunContext(ctx context.Context, cmd *cobra.Command, deps runReviewDeps) (string, error) {
	fn := deps.promptForRunContextFn
	if fn == nil {
		fn = promptForRunContext
	}
	runContext, err := fn(ctx)
	if err != nil {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return "", NewSilentError(err)
	}
	return runContext, nil
}

// selectReviewAgent picks an agent from the configured review map.
//
// If override is non-empty, returns the config for that agent or an error
// listing the configured alternatives. Otherwise returns the alphabetically
// first configured agent — deterministic but user-overridable via --agent.
func selectReviewAgent(review map[string]settings.ReviewConfig, override string) (string, settings.ReviewConfig, error) {
	if len(review) == 0 {
		return "", settings.ReviewConfig{}, errors.New("no review config found")
	}
	var names []string
	for name, cfg := range review {
		if !cfg.IsZero() {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", settings.ReviewConfig{}, errors.New("no review config found")
	}
	sort.Strings(names)
	if override != "" {
		if cfg, ok := review[override]; ok && !cfg.IsZero() {
			return override, cfg, nil
		}
		return "", settings.ReviewConfig{}, fmt.Errorf(
			"agent %q is not configured for review; configured agents: %s",
			override, strings.Join(names, ", "),
		)
	}
	pick := names[0]
	return pick, review[pick], nil
}

// verifyConfiguredSkillsInstalled is the spawn-time backstop for the
// silent-failure vector. For each skill in cfg.Skills, check it's either a
// curated built-in or returned by the agent's SkillDiscoverer; fail with a
// user-facing error if any skill is missing. Empty Skills (prompt-only
// config) short-circuits to nil — a freeform prompt has no skill list to
// validate against.
func verifyConfiguredSkillsInstalled(ctx context.Context, ag agent.Agent, cfg settings.ReviewConfig) error {
	if len(cfg.Skills) == 0 {
		return nil
	}
	builtins := builtinNameSet(skilldiscovery.CuratedBuiltinsFor(string(ag.Name())))
	discoveredNames := map[string]struct{}{}
	if d, ok := ag.(agent.SkillDiscoverer); ok {
		if skills, err := d.DiscoverReviewSkills(ctx); err == nil {
			for _, s := range skills {
				discoveredNames[s.Name] = struct{}{}
			}
		} else {
			logging.Debug(ctx, "skill verification discovery failed",
				slog.String("agent", string(ag.Name())), slog.String("error", err.Error()))
		}
	}
	var missing []string
	for _, s := range cfg.Skills {
		if _, ok := builtins[s]; ok {
			continue
		}
		if _, ok := discoveredNames[s]; ok {
			continue
		}
		missing = append(missing, s)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"configured review skill(s) not installed: %s\n"+
			"run `entire review --edit` to reconfigure, or install the plugin and retry",
		strings.Join(missing, ", "),
	)
}

// reviewPromptScope is best-effort context about the surface to review.
// All fields are optional: empty values shrink or omit the scope clause
// rather than failing. Branch is the short branch name ("" = detached).
// BaseRef is the upstream comparison point — typically the closest
// non-self ancestor branch (for branch-on-branch workflows) or the
// repo's default base branch — used to scope the review to "commits
// unique to this branch."
type reviewPromptScope struct {
	Branch  string
	BaseRef string
}

// composeReviewPrompt builds the prompt sent to the agent from a
// ReviewConfig plus optional per-run context and scope. The base is
// whichever of cfg.Prompt (persistent) or a skills-composed template is
// set; per-run context, when non-empty, is appended with a clear marker
// so the agent can distinguish persistent preferences from per-run
// intent. The scope clause, when scope has a BaseRef, pins the review
// surface to "commits unique to this branch vs the base ref" — making
// claude and codex agree on what to review and excluding work
// inherited from sibling branches. Empty cfg + empty runContext +
// zero scope returns "".
func composeReviewPrompt(cfg settings.ReviewConfig, runContext string, scope reviewPromptScope) string {
	var base string
	if cfg.Prompt != "" {
		base = cfg.Prompt
	} else if len(cfg.Skills) > 0 {
		var sb strings.Builder
		sb.WriteString("Please run these review skills in order:\n")
		for i, skill := range cfg.Skills {
			fmt.Fprintf(&sb, "  %d. %s\n", i+1, skill)
		}
		base = sb.String()
	}

	if runContext != "" {
		if base == "" {
			base = runContext
		} else {
			// strings.TrimRight strips the trailing newline the skills
			// template emits before its last entry — without this, joining
			// with "\n\nFor this review:" produced three consecutive
			// newlines (the template's \n + the joiner's \n\n) and a
			// visually hollow gap in the agent prompt.
			base = strings.TrimRight(base, "\n") + "\n\nFor this review: " + runContext
		}
	}

	clause := scopeClause(scope)
	if clause == "" {
		return base
	}
	if base == "" {
		return clause
	}
	return base + "\n\n" + clause
}

// scopeClause renders the scope hint appended to the agent prompt.
// Returns "" when scope carries no usable information (no BaseRef),
// since without a comparison point the clause would be lying about what
// to review.
//
// The clause is deliberately commits-only — uncommitted working-tree
// changes (e.g. regenerated .pyc bytecode, editor lockfiles) are noise
// that pollutes the review when the user just wants feedback on
// committed work. Users who want uncommitted work reviewed should
// either commit it first or say so in the per-run prompt textarea.
func scopeClause(scope reviewPromptScope) string {
	if scope.BaseRef == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Review scope: only the commits unique to this branch")
	if scope.Branch != "" {
		fmt.Fprintf(&sb, " (`%s`)", scope.Branch)
	}
	fmt.Fprintf(&sb, " vs base `%s`.\n", scope.BaseRef)
	fmt.Fprintf(&sb, "Use `git diff %s...HEAD` to find the surface to review. ", scope.BaseRef)
	fmt.Fprintf(&sb, "The three-dot syntax (`%s...HEAD`) excludes commits that came from sibling branches and are already on `%s` — do not review those. ", scope.BaseRef, scope.BaseRef)
	sb.WriteString("Do not review uncommitted working-tree changes (regenerated bytecode, editor temp files, etc.) unless explicitly asked.")
	return sb.String()
}

// currentHeadSHA returns the current HEAD commit hash as a 40-char hex string.
func currentHeadSHA(ctx context.Context) (string, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return "", fmt.Errorf("locate repo root: %w", err)
	}
	execCmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "HEAD")
	output, err := execCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// currentBranch returns the short branch name, or "" when HEAD is
// detached or the lookup fails. Best-effort by design: callers use it to
// label the scope clause, not to drive control flow.
func currentBranch(ctx context.Context) string {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return ""
	}
	name := gitString(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if name == detachedHEADDisplay {
		return ""
	}
	return name
}

// detectScopeBaseRef finds the most appropriate base for "commits unique
// to this branch." Strategy:
//
//  1. Find local branches whose tip is an ancestor of HEAD~1 (i.e. the
//     branch's history flows through them, but they're not just HEAD
//     itself). Skip the current branch and entire/* internal branches.
//  2. Pick the candidate whose tip is most recently authored — that's
//     the branch this one was forked from.
//  3. Fall back to the repo's default base (`detectBaseBranch`, which
//     resolves origin/HEAD or main/master).
//
// Returns "" only when no usable base is found (e.g., orphan branch,
// missing git remote). Callers treat empty as "skip the scope clause."
func detectScopeBaseRef(ctx context.Context, repoRoot string) string {
	// HEAD~1 doesn't exist on root commits — guard for it explicitly
	// rather than letting later commands fail with confusing output.
	if !gitOK(ctx, repoRoot, "rev-parse", "--verify", "HEAD~1") {
		return detectBaseBranch(ctx, repoRoot)
	}
	headBranch := gitString(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	branchesRaw := gitString(ctx, repoRoot, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if branchesRaw == "" {
		return detectBaseBranch(ctx, repoRoot)
	}
	type candidate struct {
		ref      string
		unixTime int64
	}
	var best candidate
	for _, b := range strings.Split(branchesRaw, "\n") {
		b = strings.TrimSpace(b)
		if b == "" || b == headBranch || strings.HasPrefix(b, "entire/") {
			continue
		}
		// Branch tip must be an ancestor of HEAD's parent — ancestor of
		// HEAD itself would also match the current branch's prior tip.
		if !gitOK(ctx, repoRoot, "merge-base", "--is-ancestor", b, "HEAD~1") {
			continue
		}
		ts, ok := gitCount(ctx, repoRoot, "log", "-1", "--format=%ct", b)
		if !ok {
			continue
		}
		if int64(ts) > best.unixTime {
			best = candidate{ref: b, unixTime: int64(ts)}
		}
	}
	if best.ref != "" {
		return best.ref
	}
	return detectBaseBranch(ctx, repoRoot)
}

// reviewScope summarizes what's about to be reviewed, surfaced to the user
// before the agent launches. Zero-valued fields mean "not detected" — format
// helpers degrade gracefully.
type reviewScope struct {
	Branch       string // current branch short name; "" if detached
	HeadSHA      string // short HEAD SHA (always set when non-empty scope)
	Base         string // base branch (e.g. "main"); "" if unknown
	AheadCommits int    // commits in base..HEAD; 0 if Base == ""
	FilesChanged int    // files in base..HEAD diff; 0 if Base == ""
	Uncommitted  int    // files from `git status --porcelain`
}

// detectReviewScope runs a handful of cheap `git` queries to describe the
// set of changes the user is about to review. Best-effort: any individual
// query that fails leaves the corresponding field at its zero value.
func detectReviewScope(ctx context.Context) (reviewScope, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return reviewScope{}, fmt.Errorf("locate repo root: %w", err)
	}
	var s reviewScope
	s.Branch = gitString(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if s.Branch == detachedHEADDisplay {
		s.Branch = ""
	}
	s.HeadSHA = gitString(ctx, repoRoot, "rev-parse", "--short", "HEAD")
	s.Base = detectBaseBranch(ctx, repoRoot)
	if s.Base != "" && s.Branch != "" && s.Base != s.Branch {
		if n, ok := gitCount(ctx, repoRoot, "rev-list", "--count", s.Base+".."+s.Branch); ok {
			s.AheadCommits = n
		}
		if files := gitString(ctx, repoRoot, "diff", "--name-only", s.Base+"..."+s.Branch); files != "" {
			s.FilesChanged = len(strings.Split(files, "\n"))
		}
	}
	if status := gitString(ctx, repoRoot, "status", "--porcelain"); status != "" {
		s.Uncommitted = len(strings.Split(status, "\n"))
	}
	return s, nil
}

// detectBaseBranch resolves a base branch name by trying, in order:
// origin/HEAD → origin/main → origin/master → local main → local master.
// Returns "" if none match. Remote-tracking branches come first because they
// reflect the team's convention; local branches are the fallback for repos
// that haven't been `git fetch`'d recently.
func detectBaseBranch(ctx context.Context, repoRoot string) string {
	if target := gitString(ctx, repoRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); target != "" {
		return strings.TrimPrefix(target, "origin/")
	}
	candidates := []string{defaultBaseBranch, masterBaseBranch}
	for _, candidate := range candidates {
		if gitOK(ctx, repoRoot, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+candidate) {
			return candidate
		}
	}
	for _, candidate := range candidates {
		if gitOK(ctx, repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate) {
			return candidate
		}
	}
	return ""
}

// gitString runs `git -C repoRoot <args...>` and returns trimmed stdout, or
// "" on any error.
func gitString(ctx context.Context, repoRoot string, args ...string) string {
	full := append([]string{"-C", repoRoot}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitOK runs `git -C repoRoot <args...>` and reports whether it succeeded.
func gitOK(ctx context.Context, repoRoot string, args ...string) bool {
	full := append([]string{"-C", repoRoot}, args...)
	return exec.CommandContext(ctx, "git", full...).Run() == nil
}

// gitCount runs a git command expected to output an integer and parses it.
func gitCount(ctx context.Context, repoRoot string, args ...string) (int, bool) {
	s := gitString(ctx, repoRoot, args...)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// formatReviewScope renders a one-line summary for the user. Empty-review
// cases fall back to "no changes detected".
func formatReviewScope(s reviewScope) string {
	head := s.Branch
	if head == "" {
		head = detachedHEADDisplay + " " + s.HeadSHA
	}
	var parts []string
	if s.Base != "" && s.Branch != "" && s.Base != s.Branch && (s.AheadCommits > 0 || s.FilesChanged > 0) {
		parts = append(parts,
			fmt.Sprintf("%d commits", s.AheadCommits),
			fmt.Sprintf("%d files changed", s.FilesChanged),
		)
	}
	if s.Uncommitted > 0 {
		parts = append(parts, fmt.Sprintf("%d uncommitted", s.Uncommitted))
	}
	suffix := "no changes detected"
	if len(parts) > 0 {
		suffix = strings.Join(parts, ", ")
	}
	if s.Base != "" && s.Branch != "" && s.Base != s.Branch {
		return fmt.Sprintf("Reviewing %s vs %s: %s", head, s.Base, suffix)
	}
	return fmt.Sprintf("Reviewing %s: %s", head, suffix)
}

// headHasReviewCheckpoint checks whether HEAD's checkpoint metadata includes
// a review session. Returns (true, infoString) if HasReview is set.
// Single lookup: read the Entire-Checkpoint trailer from HEAD, then resolve
// the CheckpointSummary via ResolveCommittedReaderForCheckpoint so v2-enabled
// repos also work (v1 alone would miss v2-written summaries).
//
// Each early return logs at Debug so users debugging "why is no review badge
// showing?" have breadcrumbs without the caller having to ask for five
// distinct failure modes.
func headHasReviewCheckpoint(ctx context.Context) (bool, string) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		logging.Debug(ctx, "head review check: locate worktree root", slog.String("error", err.Error()))
		return false, ""
	}
	execCmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "log", "-1", "--format=%B")
	output, err := execCmd.Output()
	if err != nil {
		logging.Debug(ctx, "head review check: read HEAD commit message", slog.String("error", err.Error()))
		return false, ""
	}
	cpID, ok := trailers.ParseCheckpoint(string(output))
	if !ok {
		logging.Debug(ctx, "head review check: no Entire-Checkpoint trailer on HEAD")
		return false, ""
	}
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		logging.Debug(ctx, "head review check: open repository", slog.String("error", err.Error()))
		return false, ""
	}
	v1Store := checkpoint.NewGitStore(repo)
	v2URL, urlErr := remote.FetchURL(ctx)
	if urlErr != nil {
		logging.Debug(ctx, "head review check: no configured v2 fetch remote", slog.String("error", urlErr.Error()))
		v2URL = ""
	}
	v2Store := checkpoint.NewV2GitStore(repo, v2URL)
	_, summary, err := checkpoint.ResolveCommittedReaderForCheckpoint(ctx, cpID, v1Store, v2Store, settings.IsCheckpointsV2Enabled(ctx))
	if err != nil || summary == nil {
		logging.Debug(ctx, "head review check: resolve checkpoint summary",
			slog.String("checkpoint_id", cpID.String()),
			slog.Any("error", err))
		return false, ""
	}
	if !summary.HasReview {
		logging.Debug(ctx, "head review check: summary HasReview is false", slog.String("checkpoint_id", cpID.String()))
		return false, ""
	}
	return true, fmt.Sprintf("checkpoint %s", cpID)
}

// saveReviewConfig persists the review map into .entire/settings.json while
// preserving all other settings. A Load error means the file exists but is
// malformed — we must NOT silently overwrite it with an empty struct, or
// every unrelated setting the user had configured would be wiped. Return the
// error so the caller can surface it instead.
func saveReviewConfig(ctx context.Context, review map[string]settings.ReviewConfig) error {
	s, err := settings.Load(ctx)
	if err != nil {
		return fmt.Errorf("load settings before save: %w", err)
	}
	if s == nil {
		s = &settings.EntireSettings{}
	}
	s.Review = review
	if err := settings.Save(ctx, s); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// buildReviewPickerFields composes the per-agent group fields for the
// review picker. Returns a slice of huh.Field in render order:
//
//	0: built-in commands (multiselect) OR note
//	1: installed plugin skills (multiselect) OR note
//	2: install hints (note with all active hint messages) — OMITTED if empty
//	3: additional instructions (text) — always present
//
// splitSavedPicks partitions a flat saved-skills list into the subset that
// matches built-in curated commands and the subset that matches discovered
// plugin skills. Skill names that match neither (e.g., external-agent
// entries, or skills that were uninstalled since save) are dropped from
// both — they're preserved on the settings side via mergePickerResults
// when they belong to a picker-unaware agent entry.
func splitSavedPicks(saved []string, builtins []skilldiscovery.CuratedSkill, discovered []agent.DiscoveredSkill) ([]string, []string) {
	builtinNames := make(map[string]struct{}, len(builtins))
	for _, b := range builtins {
		builtinNames[b.Name] = struct{}{}
	}
	discoveredNames := make(map[string]struct{}, len(discovered))
	for _, d := range discovered {
		discoveredNames[d.Name] = struct{}{}
	}
	var builtinPicks, discoveredPicks []string
	for _, s := range saved {
		if _, ok := builtinNames[s]; ok {
			builtinPicks = append(builtinPicks, s)
			continue
		}
		if _, ok := discoveredNames[s]; ok {
			discoveredPicks = append(discoveredPicks, s)
		}
	}
	return builtinPicks, discoveredPicks
}

// preselectedSet turns a slice pointer's current contents into a lookup
// set for the picker's "previously-saved" pre-selection. Nil or empty
// returns nil — the caller's `if _, ok := set[name]; ok` works either way.
func preselectedSet(slice *[]string) map[string]struct{} {
	if slice == nil || len(*slice) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(*slice))
	for _, s := range *slice {
		out[s] = struct{}{}
	}
	return out
}

// Pure function — no side effects, no huh form running — so unit-testable.
// Value bindings (builtinPicksOut, discoveredPicksOut, promptOut) may be
// nil when the caller only needs the field count (tests). When the out
// slices contain values on entry, those values double as the pre-selected
// set: matching options get Option.Selected(true) so the picker preserves
// the saved config on accept-defaults.
func buildReviewPickerFields(
	agentName string,
	builtins []skilldiscovery.CuratedSkill,
	discovered []agent.DiscoveredSkill,
	activeHints []skilldiscovery.InstallHint,
	previousPrompt string,
	builtinPicksOut, discoveredPicksOut *[]string,
	promptOut *string,
) []huh.Field {
	var fields []huh.Field

	// Picker labels show only the invocation name — no descriptions. Agent
	// descriptions in particular can be pages of embedded usage examples,
	// which makes the picker unreadable. Users recognize skills by name;
	// the stored value is the name either way.
	// Pre-populated contents of the output slices double as the "previously
	// selected" set so the picker preselects saved skills via
	// Option.Selected(true). Accepting defaults in --edit therefore
	// preserves the agent's saved config instead of wiping it.
	builtinPreselected := preselectedSet(builtinPicksOut)
	discoveredPreselected := preselectedSet(discoveredPicksOut)

	if len(builtins) > 0 {
		opts := make([]huh.Option[string], 0, len(builtins))
		for _, b := range builtins {
			opt := huh.NewOption(b.Name, b.Name)
			if _, ok := builtinPreselected[b.Name]; ok {
				opt = opt.Selected(true)
			}
			opts = append(opts, opt)
		}
		ms := huh.NewMultiSelect[string]().Title("Built-in commands").Options(opts...)
		if builtinPicksOut != nil {
			ms = ms.Value(builtinPicksOut)
		}
		fields = append(fields, ms)
	} else {
		fields = append(fields, huh.NewNote().
			Title("Built-in commands").
			Description(fmt.Sprintf("No built-in review commands in %s.", agentName)))
	}

	if len(discovered) > 0 {
		opts := make([]huh.Option[string], 0, len(discovered))
		for _, d := range discovered {
			opt := huh.NewOption(d.Name, d.Name)
			if _, ok := discoveredPreselected[d.Name]; ok {
				opt = opt.Selected(true)
			}
			opts = append(opts, opt)
		}
		ms := huh.NewMultiSelect[string]().Title("Installed plugin skills").Options(opts...)
		if discoveredPicksOut != nil {
			ms = ms.Value(discoveredPicksOut)
		}
		fields = append(fields, ms)
	} else {
		fields = append(fields, huh.NewNote().
			Title("Installed plugin skills").
			Description("No plugin review skills detected on disk."))
	}

	if len(activeHints) > 0 {
		var sb strings.Builder
		for i, h := range activeHints {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString("• ")
			sb.WriteString(h.Message)
		}
		fields = append(fields, huh.NewNote().
			Title("Install more").
			Description(sb.String()))
	}

	text := huh.NewText().
		Title("Additional instructions (optional)").
		Description("Used verbatim as the review prompt when set. Leave blank to use the default 'run these skills in order' template.")
	if promptOut != nil {
		*promptOut = previousPrompt
		text = text.Value(promptOut)
	}
	fields = append(fields, text)

	return fields
}

func builtinNameSet(curated []skilldiscovery.CuratedSkill) map[string]struct{} {
	set := make(map[string]struct{}, len(curated))
	for _, c := range curated {
		set[c.Name] = struct{}{}
	}
	return set
}

// filterOutBuiltinCollisions drops any discovered skill whose name collides
// with a curated built-in. Built-in wins because it carries a richer,
// hand-authored description.
func filterOutBuiltinCollisions(discovered []agent.DiscoveredSkill, builtins map[string]struct{}) []agent.DiscoveredSkill {
	if len(discovered) == 0 || len(builtins) == 0 {
		return discovered
	}
	out := make([]agent.DiscoveredSkill, 0, len(discovered))
	for _, d := range discovered {
		if _, clash := builtins[d.Name]; clash {
			continue
		}
		out = append(out, d)
	}
	return out
}

func dedupeStrings(xs []string) []string {
	if len(xs) == 0 {
		return xs
	}
	seen := make(map[string]struct{}, len(xs))
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
