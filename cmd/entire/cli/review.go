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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	git "github.com/go-git/go-git/v6"

	"github.com/entireio/cli/cmd/entire/cli/agent"
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
	AgentName    string    `json:"agent_name"`
	Skills       []string  `json:"skills"`
	StartingSHA  string    `json:"starting_sha"`
	StartedAt    time.Time `json:"started_at"`
	WorktreePath string    `json:"worktree_path,omitempty"`
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

// curatedSkill represents a known review skill/command surfaced by the
// first-run picker. Users can add custom skills by editing
// .entire/settings.json directly.
type curatedSkill struct {
	Name string
	Desc string
}

// curatedReviewSkills groups known review skills by agent name (as a string
// matching types.AgentName values). Agents not listed here still work via
// the picker — users just see an empty list and should edit settings.json
// manually to add skills.
var curatedReviewSkills = map[string][]curatedSkill{
	"claude-code": {
		{Name: "/pr-review-toolkit:review-pr", Desc: "Full PR review"},
		{Name: "/pr-review-toolkit:code-reviewer", Desc: "Code review for standards"},
		{Name: "/test-auditor", Desc: "Test coverage audit"},
		{Name: "/verification-before-completion", Desc: "Verify before marking done"},
		{Name: "/requesting-code-review", Desc: "Prepare code for review"},
		{Name: "/pr-review-toolkit:silent-failure-hunter", Desc: "Find suppressed errors"},
	},
	"codex": {
		{Name: "/codex:review", Desc: "Codex review"},
		{Name: "/codex:adversarial-review", Desc: "Adversarial review — red-team"},
	},
}

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
// Worktree scoping: when the marker carries a WorktreePath, only a session
// whose own WorktreePath matches will adopt it. This prevents a Claude
// session in worktree A from racing to claim a marker that was meant for a
// session `entire review` just launched in worktree B (both worktrees share
// the same .git/entire-sessions/ directory where the marker lives).
// Pre-fix markers with no WorktreePath fall back to unscoped adoption.
func adoptPendingReviewMarkerInto(ctx context.Context, s session.State) (session.State, bool, error) {
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
	s.Kind = session.KindAgentReview
	s.ReviewSkills = m.Skills
	if err := ClearPendingReviewMarker(ctx); err != nil {
		// Tagging succeeded; leftover marker self-heals on next session start
		// (since Kind is now set, the next turn will return modified=false
		// and the marker will be re-cleared on any next review session).
		logging.Warn(ctx, "failed to clear pending review marker", slog.String("error", err.Error()))
	}
	return s, true, nil
}

// runReviewConfigPicker presents a huh multi-select for each installed agent
// that has curated review skills, and saves the selection to
// .entire/settings.json. Previously-saved skills are pre-checked via
// huh.Option.Selected(true), mirroring how `entire enable` preserves prior
// selections in its own agent picker.
func runReviewConfigPicker(ctx context.Context, out io.Writer) (map[string][]string, error) {
	installed := GetAgentsWithHooksInstalled(ctx)
	if len(installed) == 0 {
		return nil, errors.New("no agents installed; run 'entire enable' first")
	}

	// Narrow to agents that have a curated skills list; others need manual
	// editing of settings.json under review.<agent-name>.
	type configurableAgent struct {
		name types.AgentName
		ag   agent.Agent
	}
	var configurable []configurableAgent
	for _, name := range installed {
		curated, ok := curatedReviewSkills[string(name)]
		if !ok || len(curated) == 0 {
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

	// Load existing config so we can pre-check saved skills. A load error
	// here means the settings file is malformed; log at Warn so users
	// debugging "my saved skills aren't pre-checked" can see why, but keep
	// going with an empty prefill — runReview already surfaces the same
	// error distinctly when it's the first load.
	existing := map[string][]string{}
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
	fmt.Fprintf(out, "Configuring review skills for %d agent(s): %s\n", len(configurable), strings.Join(labels, ", "))
	fmt.Fprintln(out, "(Previously-saved skills are pre-checked. Space to toggle, enter to confirm.)")
	fmt.Fprintln(out)

	selected := map[string][]string{}
	for i, c := range configurable {
		curated := curatedReviewSkills[string(c.name)]
		savedSet := map[string]struct{}{}
		for _, s := range existing[string(c.name)] {
			savedSet[s] = struct{}{}
		}

		options := make([]huh.Option[string], 0, len(curated))
		for _, s := range curated {
			opt := huh.NewOption(fmt.Sprintf("%s — %s", s.Name, s.Desc), s.Name)
			if _, ok := savedSet[s.Name]; ok {
				opt = opt.Selected(true)
			}
			options = append(options, opt)
		}

		// huh populates picks from Option.Selected(true) — do NOT pre-seed,
		// matching the convention used in detectOrSelectAgent (setup.go).
		var picks []string
		title := fmt.Sprintf("[%d/%d] Review skills for %s", i+1, len(configurable), c.ag.Type())
		form := NewAccessibleForm(huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(title).
				Description(fmt.Sprintf("Agent: %s", c.ag.Type())).
				Options(options...).
				Value(&picks),
		))
		if err := form.Run(); err != nil {
			return nil, fmt.Errorf("picker for %s: %w", c.name, err)
		}
		if len(picks) > 0 {
			selected[string(c.name)] = picks
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("no review skills selected")
	}
	if err := saveReviewConfig(ctx, selected); err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "Saved review config to .entire/settings.json. Edit directly or run `entire review --edit`.")
	return selected, nil
}

func newReviewCmd() *cobra.Command {
	var edit bool
	var trackOnly bool

	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run configured review skills against the current branch",
		Long: `Run the review skills configured in .entire/settings.json against
the current branch. On first run, an interactive picker writes the config.

The review session is recorded as part of the next checkpoint, so the
review metadata is permanently attached to the commit it covers.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if edit {
				_, err := runReviewConfigPicker(ctx, cmd.OutOrStdout())
				return err
			}
			return runReview(ctx, cmd, trackOnly)
		},
	}
	cmd.Flags().BoolVar(&edit, "edit", false, "re-open the review config picker")
	cmd.Flags().BoolVar(&trackOnly, "track-only", false, "write pending marker without spawning agent")
	return cmd
}

func runReview(ctx context.Context, cmd *cobra.Command, trackOnly bool) error {
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
		picked, pickErr := runReviewConfigPicker(ctx, out)
		if pickErr != nil {
			return pickErr
		}
		if s == nil {
			s = &settings.EntireSettings{}
		}
		s.Review = picked
	}

	// 3. Pick agent.
	agentName, skills, err := selectReviewAgent(s.Review)
	if err != nil {
		return err
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

	// 6. Write pending marker (agent hook will adopt it).
	if err := WritePendingReviewMarker(ctx, PendingReviewMarker{
		AgentName:    agentName,
		Skills:       skills,
		StartingSHA:  headSHA,
		StartedAt:    time.Now().UTC(),
		WorktreePath: worktreeRoot,
	}); err != nil {
		return fmt.Errorf("write pending marker: %w", err)
	}

	if trackOnly {
		// Marker must persist — the user will start the agent manually and
		// its hook will adopt the marker.
		fmt.Fprintln(out, "Pending review marker written.")
		fmt.Fprintf(out, "Start %s and run these skills manually: %s\n", agentName, strings.Join(skills, ", "))
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

	// 7. Spawn agent with the composed initial prompt.
	launcher, ok := agent.LauncherFor(types.AgentName(agentName))
	if !ok {
		fmt.Fprintf(out, "%s does not support subprocess launch yet. Falling back to --track-only.\n", agentName)
		fmt.Fprintf(out, "Start %s manually and run: %s\n", agentName, strings.Join(skills, ", "))
		return nil
	}
	// Best-effort: show the user what's in scope so they can tell whether
	// the review target is what they expected. Failures are silent — scope
	// is informational, not load-bearing.
	if scope, scopeErr := detectReviewScope(ctx); scopeErr == nil {
		fmt.Fprintln(out, formatReviewScope(scope))
	}
	prompt := composeReviewPrompt(skills)
	execCmd, err := launcher.LaunchCmd(ctx, prompt)
	if err != nil {
		return fmt.Errorf("launch %s: %w", agentName, err)
	}
	if err := execCmd.Run(); err != nil {
		return fmt.Errorf("agent exited: %w", err)
	}
	return nil
}

// selectReviewAgent picks an agent from the configured review map. v1: single
// agent. If multiple are configured, returns the one that sorts first by name
// (deterministic default). Returns an error if the map is empty.
func selectReviewAgent(review map[string][]string) (string, []string, error) {
	if len(review) == 0 {
		return "", nil, errors.New("no review skills configured")
	}
	// Deterministic pick: alphabetical by agent name.
	var names []string
	for name, skills := range review {
		if len(skills) > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", nil, errors.New("no review skills configured")
	}
	sort.Strings(names)
	pick := names[0]
	return pick, review[pick], nil
}

// composeReviewPrompt builds the initial prompt the agent receives.
func composeReviewPrompt(skills []string) string {
	var sb strings.Builder
	sb.WriteString("Please run these review skills in order:\n")
	for i, skill := range skills {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, skill)
	}
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
func saveReviewConfig(ctx context.Context, review map[string][]string) error {
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
