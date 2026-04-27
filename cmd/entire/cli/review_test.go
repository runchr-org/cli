package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/skilldiscovery"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// installHooksForTest installs the given agent's hooks into CWD-relative
// repo paths, so tests that exercise code paths gated on hook presence can
// proceed past agent selection.
func installHooksForTest(t *testing.T, agentName types.AgentName) {
	t.Helper()
	ag, err := agent.Get(agentName)
	if err != nil {
		t.Fatalf("agent.Get(%q): %v", agentName, err)
	}
	hs, ok := agent.AsHookSupport(ag)
	if !ok {
		t.Fatalf("agent %q does not support hooks", agentName)
	}
	if _, err := hs.InstallHooks(context.Background(), false, false); err != nil {
		t.Fatalf("InstallHooks(%q): %v", agentName, err)
	}
}

const (
	testReviewSkill   = "/pr-review-toolkit:review-pr"
	testMainBranch    = "main"
	testCodexAgent    = "codex"
	testExternalAgent = "my-external"
	testExternalSkill = "/external-skill"
)

// setupReviewTestRepoWithCommit initializes a temp git repo with a single
// commit and chdirs into it. Returns the tmp dir. Use for tests that just
// need "a git repo with at least one commit" — tests that care about
// specific filenames or commit content should set up explicitly.
func setupReviewTestRepoWithCommit(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)
	return tmp
}

func TestReviewMarker_RoundTrip(t *testing.T) {
	tmp := setupReviewTestRepoWithCommit(t)

	m := PendingReviewMarker{
		AgentName:   "claude-code",
		Skills:      []string{testReviewSkill},
		Prompt:      "Please run these review skills in order:\n  1. " + testReviewSkill + "\n",
		StartingSHA: "deadbeef",
		StartedAt:   time.Now().UTC(),
	}
	ctx := context.Background()
	if err := WritePendingReviewMarker(ctx, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, ok, err := ReadPendingReviewMarker(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok {
		t.Fatal("expected marker present")
	}
	if got.AgentName != m.AgentName || got.StartingSHA != m.StartingSHA {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if got.Prompt != m.Prompt {
		t.Errorf("Prompt roundtrip mismatch: got %q want %q", got.Prompt, m.Prompt)
	}

	// Marker file must live under .git/entire-sessions/, not the worktree.
	// Check before clearing so the file is actually present on disk.
	markerGlob := filepath.Join(tmp, ".git", "entire-sessions", "*")
	entries, err := filepath.Glob(markerGlob)
	if err != nil {
		t.Fatalf("glob sessions dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("no marker file found under %s — path resolution may have regressed", markerGlob)
	}

	if err := ClearPendingReviewMarker(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	_, ok, err = ReadPendingReviewMarker(ctx)
	if err != nil {
		t.Fatalf("read-after-clear: %v", err)
	}
	if ok {
		t.Error("expected marker absent after clear")
	}
}

func TestReviewCmd_Help(t *testing.T) {
	t.Parallel()
	rootCmd := NewRootCmd()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"review", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "review") {
		t.Errorf("--help output missing 'review': %s", out)
	}
}

func TestSaveReviewConfig_PersistsSettings(t *testing.T) {
	// NOTE: uses t.Chdir, so no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	t.Chdir(tmp)

	err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		"claude-code": {Skills: []string{testReviewSkill, "/test-auditor"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	s, err := settings.Load(context.Background())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	cfg := s.Review["claude-code"]
	if len(cfg.Skills) != 2 {
		t.Errorf("expected 2 skills saved, got %v", cfg.Skills)
	}
	if cfg.Skills[0] != testReviewSkill {
		t.Errorf("first skill = %q", cfg.Skills[0])
	}
}

// Regression: running `entire review` when the configured agent has no hooks
// installed must abort with a clear error instead of writing a marker no
// hook will ever adopt. Covers both stale config (user edited settings.json
// by hand) and post-disable state (user ran `entire disable` without
// cleaning up review settings).
func TestRunReview_MissingHooksAborts(t *testing.T) {
	setupReviewTestRepoWithCommit(t)

	// No installHooksForTest — this is the point.
	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testAgentName: {Skills: []string{testReviewSkill}},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := NewRootCmd()
	errBuf := &bytes.Buffer{}
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"review"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when hooks are not installed")
	}
	if !strings.Contains(errBuf.String(), "Hooks are not installed") {
		t.Errorf("expected 'Hooks are not installed' in stderr, got: %s", errBuf.String())
	}

	// Marker must not leak — the gate runs before WritePendingReviewMarker.
	if _, ok, readErr := ReadPendingReviewMarker(context.Background()); readErr != nil || ok {
		t.Errorf("marker should not exist when hooks are missing: ok=%v err=%v", ok, readErr)
	}
}

// Regression: non-launchable agents must preserve the pending marker so
// the manually-started agent can adopt it. Previously the cleanup defer
// was registered before the LauncherFor check, so the !ok fallback
// printed its "start manually" message but then wiped the marker on
// return, silently breaking the hand-off.
//
// Uses cursor because it has HookSupport but no Launcher, triggering
// the !ok fallback.
func TestRunReview_NonLaunchableAgentPreservesMarker(t *testing.T) {
	setupReviewTestRepoWithCommit(t)

	const nonLaunchableAgent = "cursor"
	installHooksForTest(t, types.AgentName(nonLaunchableAgent))

	// Confirm the precondition: cursor really has no Launcher. If a future
	// change adds one, this test's premise is invalid and needs a different
	// agent.
	if _, hasLauncher := agent.LauncherFor(types.AgentName(nonLaunchableAgent)); hasLauncher {
		t.Skipf("%s now implements Launcher; pick another non-launchable agent for this regression test", nonLaunchableAgent)
	}

	// Use a prompt-only config here: cursor has no curated built-ins and
	// no SkillDiscoverer, so listing a Skills value would trip the
	// spawn-time installed-skill guard (see
	// TestRunReview_MissingConfiguredSkillAbortsBeforeMarker) before we
	// reach the non-launchable code path this test pins. Prompt-only
	// configs skip verification and still exercise the same fallback.
	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		nonLaunchableAgent: {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	// Stub the run-context prompt so the test doesn't hit the real huh
	// textarea (no TTY in tests).
	deps := runReviewDeps{
		promptForRunContextFn: func(_ context.Context) (string, error) { return "", nil },
	}
	reviewCmd := newReviewCmdWithDeps(deps)
	buf := &bytes.Buffer{}
	reviewCmd.SetOut(buf)
	if err := reviewCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Marker written") {
		t.Errorf("expected marker-written message, got: %s", out)
	}

	m, ok, err := ReadPendingReviewMarker(context.Background())
	if err != nil {
		t.Fatalf("ReadPendingReviewMarker: %v", err)
	}
	if !ok {
		t.Fatal("marker was cleared by defer on !ok fallback — the hand-off message is a lie")
	}
	if m.AgentName != nonLaunchableAgent {
		t.Errorf("AgentName = %q, want %s", m.AgentName, nonLaunchableAgent)
	}
}

func TestComposeReviewPrompt_SkillsOnly(t *testing.T) {
	t.Parallel()
	prompt := composeReviewPrompt(settings.ReviewConfig{
		Skills: []string{"/review-pr", "/test-auditor"},
	}, "", reviewPromptScope{})
	if strings.Contains(prompt, "entire-review:finish") {
		t.Errorf("prompt should not reference finish skill; got: %s", prompt)
	}
	if !strings.Contains(prompt, "/review-pr") {
		t.Errorf("prompt missing skill name; got: %s", prompt)
	}
}

// Custom Prompt wins verbatim over skills — the user's words are the
// source of truth for what the agent receives.
func TestComposeReviewPrompt_CustomPromptWinsOverSkills(t *testing.T) {
	t.Parallel()
	custom := "Focus on security regressions this week."
	prompt := composeReviewPrompt(settings.ReviewConfig{
		Skills: []string{"/review-pr"},
		Prompt: custom,
	}, "", reviewPromptScope{})
	if prompt != custom {
		t.Errorf("composeReviewPrompt = %q, want verbatim custom prompt %q", prompt, custom)
	}
}

// Empty config returns an empty string — callers should avoid invoking
// the spawn path with empty config.
func TestComposeReviewPrompt_EmptyConfigReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := composeReviewPrompt(settings.ReviewConfig{}, "", reviewPromptScope{}); got != "" {
		t.Errorf("empty config = %q, want empty", got)
	}
}

// TestComposeReviewPrompt_AppendsRunContext covers the four composition cases
// when per-run context is provided alongside persistent cfg fields.
func TestComposeReviewPrompt_AppendsRunContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		cfg        settings.ReviewConfig
		runContext string
		want       string
	}{
		{
			name:       "no context, skills only",
			cfg:        settings.ReviewConfig{Skills: []string{"/review"}},
			runContext: "",
			want:       "Please run these review skills in order:\n  1. /review\n",
		},
		{
			name:       "context + skills",
			cfg:        settings.ReviewConfig{Skills: []string{"/review"}},
			runContext: "focus on auth",
			want:       "Please run these review skills in order:\n  1. /review\n\n\nFor this review: focus on auth",
		},
		{
			name:       "context + persistent prompt",
			cfg:        settings.ReviewConfig{Prompt: "always flag side effects"},
			runContext: "review the migration",
			want:       "always flag side effects\n\nFor this review: review the migration",
		},
		{
			name:       "context only, no cfg",
			cfg:        settings.ReviewConfig{},
			runContext: "one-off review",
			want:       "one-off review",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := composeReviewPrompt(tt.cfg, tt.runContext, reviewPromptScope{})
			if got != tt.want {
				t.Errorf("composeReviewPrompt(%+v, %q) =\n%q\nwant:\n%q", tt.cfg, tt.runContext, got, tt.want)
			}
		})
	}
}

// TestComposeReviewPrompt_AppendsScopeClause locks the format and
// presence rules for the scope clause: empty BaseRef omits it; BaseRef
// alone renders without the branch label; branch+BaseRef renders the
// full clause after the base prompt.
func TestComposeReviewPrompt_AppendsScopeClause(t *testing.T) {
	t.Parallel()
	cfg := settings.ReviewConfig{Skills: []string{"/review"}}
	tests := []struct {
		name           string
		scope          reviewPromptScope
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:           "no BaseRef, no clause",
			scope:          reviewPromptScope{Branch: "main"},
			wantNotContain: []string{"Review scope:"},
		},
		{
			name:         "BaseRef only, no branch label",
			scope:        reviewPromptScope{BaseRef: "main"},
			wantContains: []string{"Review scope:", "vs base `main`", "git diff main...HEAD"},
			wantNotContain: []string{
				"(``)",                   // empty branch label
				"git status --porcelain", // commits-only — uncommitted is noise
			},
		},
		{
			name:  "branch + BaseRef",
			scope: reviewPromptScope{Branch: "hackathon-slides", BaseRef: "audit-traceability"},
			wantContains: []string{
				"(`hackathon-slides`)",
				"vs base `audit-traceability`",
				"git diff audit-traceability...HEAD",
				"Do not review uncommitted",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := composeReviewPrompt(cfg, "", tt.scope)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, unwant := range tt.wantNotContain {
				if strings.Contains(got, unwant) {
					t.Errorf("unexpected %q in:\n%s", unwant, got)
				}
			}
		})
	}
}

// Empty cfg + empty runContext + scope-only should still produce the
// scope clause (an agent run with no skills configured still gets a
// useful instruction).
func TestComposeReviewPrompt_ScopeOnlyNoBase(t *testing.T) {
	t.Parallel()
	got := composeReviewPrompt(settings.ReviewConfig{}, "", reviewPromptScope{BaseRef: "main"})
	if !strings.HasPrefix(got, "Review scope:") {
		t.Errorf("scope-only should produce clause as full prompt; got:\n%s", got)
	}
}

// TestDetectScopeBaseRef_PicksClosestAncestorBranch reproduces the
// branch-on-branch topology that motivated the scope rework: HEAD is on
// `feature` which was forked from `intermediate` (forked from `main`).
// The scope base must resolve to `intermediate`, not `main`, so the
// review covers only the commits unique to `feature` (mirroring the
// demo-repo `hackathon-slides` / `audit-traceability` / `main` setup).
func TestDetectScopeBaseRef_PicksClosestAncestorBranch(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "main")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "main commit")

	// Fork `intermediate` off main, add a commit there.
	testutil.GitCheckoutNewBranch(t, tmp, "intermediate")
	testutil.WriteFile(t, tmp, "f.txt", "intermediate")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "intermediate commit")

	// Fork `feature` off intermediate, add a commit there.
	testutil.GitCheckoutNewBranch(t, tmp, "feature")
	testutil.WriteFile(t, tmp, "f.txt", "feature")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "feature commit")

	got := detectScopeBaseRef(context.Background(), tmp)
	if got != "intermediate" {
		t.Errorf("detectScopeBaseRef = %q, want \"intermediate\" (closest ancestor branch, not main)", got)
	}
}

// TestDetectScopeBaseRef_FallsBackToDefaultBaseBranch covers the simple
// case where the branch was forked directly from the default base —
// there's no intermediate branch tip to find, so we fall through to
// detectBaseBranch (which resolves origin/HEAD or main/master).
func TestDetectScopeBaseRef_FallsBackToDefaultBaseBranch(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "main")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "main commit")

	testutil.GitCheckoutNewBranch(t, tmp, "feature")
	testutil.WriteFile(t, tmp, "f.txt", "feature")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "feature commit")

	got := detectScopeBaseRef(context.Background(), tmp)
	// detectBaseBranch returns main (or master) for the fallback. Either
	// is acceptable — the point is we didn't pick "feature" itself.
	if got != "main" && got != "master" {
		t.Errorf("detectScopeBaseRef = %q, want \"main\" or \"master\" (fallback)", got)
	}
}

// TestDetectScopeBaseRef_SkipsEntireInternalBranches makes sure the
// `entire/*` shadow branches the strategy creates don't get picked as
// scope bases — that would produce scope clauses pointed at internal
// state and produce empty diffs.
func TestDetectScopeBaseRef_SkipsEntireInternalBranches(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "main")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "main commit")

	// An entire/* branch with the same tip as main shouldn't be
	// preferred over main.
	testutil.CreateBranch(t, tmp, "entire/abc1234-def567")

	testutil.GitCheckoutNewBranch(t, tmp, "feature")
	testutil.WriteFile(t, tmp, "f.txt", "feature")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "feature commit")

	got := detectScopeBaseRef(context.Background(), tmp)
	if strings.HasPrefix(got, "entire/") {
		t.Errorf("detectScopeBaseRef = %q, must not pick an entire/* internal branch", got)
	}
}

// TestRunReview_CallsPromptForRunContextBeforeSpawn pins that runReview
// calls the injection hook between agent selection and marker write/spawn,
// and that the returned run-context text is composed into the final
// prompt stored on PendingReviewMarker.Prompt.
func TestRunReview_CallsPromptForRunContextBeforeSpawn(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, "cursor") // non-launchable so !ok fallback preserves marker

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		"cursor": {Prompt: "always flag side effects"},
	}); err != nil {
		t.Fatal(err)
	}

	called := false
	deps := runReviewDeps{
		promptForRunContextFn: func(_ context.Context) (string, error) {
			called = true
			return "focus on auth refactor", nil
		},
	}

	reviewCmd := newReviewCmdWithDeps(deps)
	if err := reviewCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Error("promptForRunContextFn was not called")
	}

	m, ok, err := ReadPendingReviewMarker(context.Background())
	if err != nil || !ok {
		t.Fatalf("marker should exist (non-launchable fallback): ok=%v err=%v", ok, err)
	}
	// Match on the run-context composition pinned by this test; the scope
	// clause is asserted separately in TestComposeReviewPrompt_AppendsScopeClause
	// so we don't lock its exact wording here.
	wantSubstr := "always flag side effects\n\nFor this review: focus on auth refactor"
	if !strings.Contains(m.Prompt, wantSubstr) {
		t.Errorf("marker.Prompt missing %q; got:\n%s", wantSubstr, m.Prompt)
	}
}

// TestRunReview_MultiAgent_PassesRunContextToAllTasks pins that the per-run
// context fires in the multi-agent path and is composed into each task's
// prompt before reaching the orchestrator.
func TestRunReview_MultiAgent_PassesRunContextToAllTasks(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testAgentName)
	installHooksForTest(t, testCodexAgent)

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testAgentName:  {Prompt: "always flag side effects"},
		testCodexAgent: {Prompt: "always flag side effects"},
	}); err != nil {
		t.Fatal(err)
	}

	var gotPrompts []string
	deps := runReviewDeps{
		promptForAgentsFn: func(_ context.Context, eligible []agentChoice) ([]string, error) {
			return []string{eligible[0].Name, eligible[1].Name}, nil
		},
		promptForRunContextFn: func(_ context.Context) (string, error) {
			return "focus on auth", nil
		},
		runMultiAgentFn: func(_ context.Context, tasks []MultiAgentTask, _ io.Writer) (MultiRunResult, error) {
			for _, task := range tasks {
				gotPrompts = append(gotPrompts, task.Prompt)
			}
			return MultiRunResult{Runs: []AgentRunResult{
				{Name: tasks[0].Name, Status: AgentRunDone},
				{Name: tasks[1].Name, Status: AgentRunDone},
			}}, nil
		},
	}

	reviewCmd := newReviewCmdWithDeps(deps)
	if err := reviewCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(gotPrompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(gotPrompts))
	}
	wantSubstr := "always flag side effects\n\nFor this review: focus on auth"
	for i, prompt := range gotPrompts {
		if !strings.Contains(prompt, wantSubstr) {
			t.Errorf("task[%d].Prompt missing %q; got:\n%s", i, wantSubstr, prompt)
		}
	}
}

// --agent flag resolves a non-default configured agent when the map has
// multiple entries. Previously the alphabetically-first agent always won
// silently.
func TestSelectReviewAgent_OverrideResolvesSpecificAgent(t *testing.T) {
	t.Parallel()
	review := map[string]settings.ReviewConfig{
		testAgentName:  {Skills: []string{"/a"}},
		testCodexAgent: {Skills: []string{"/b"}},
	}

	name, cfg, err := selectReviewAgent(review, testCodexAgent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != testCodexAgent || len(cfg.Skills) != 1 || cfg.Skills[0] != "/b" {
		t.Errorf("override=%s returned name=%q cfg=%+v", testCodexAgent, name, cfg)
	}

	// Default (no override) must remain the alphabetically-first agent for
	// backwards compatibility.
	name, _, err = selectReviewAgent(review, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != testAgentName {
		t.Errorf("default pick = %q, want %q", name, testAgentName)
	}

	// Unknown override must surface a helpful error listing the configured
	// agents instead of silently falling back.
	_, _, err = selectReviewAgent(review, "gemini")
	if err == nil {
		t.Fatal("expected error for unconfigured --agent value")
	}
	if !strings.Contains(err.Error(), testAgentName) || !strings.Contains(err.Error(), testCodexAgent) {
		t.Errorf("error should list configured agents; got: %v", err)
	}
}

// mergePickerResults unit tests — the picker itself can't run headless, but
// its post-processing step is pure. Together these pin the data-loss
// regression where a manually-configured external-agent entry would be
// silently deleted the first time the user ran `entire review --edit`.
func TestMergePickerResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		existing map[string]settings.ReviewConfig
		offered  map[string]struct{}
		selected map[string]settings.ReviewConfig
		want     map[string]settings.ReviewConfig
	}{
		{
			name: "preserves uncurated/external entries the picker did not surface",
			existing: map[string]settings.ReviewConfig{
				testAgentName:     {Skills: []string{"/old-pick"}},
				testExternalAgent: {Skills: []string{testExternalSkill}},
			},
			offered:  map[string]struct{}{testAgentName: {}},
			selected: map[string]settings.ReviewConfig{testAgentName: {Skills: []string{"/new-pick"}}},
			want: map[string]settings.ReviewConfig{
				testAgentName:     {Skills: []string{"/new-pick"}},
				testExternalAgent: {Skills: []string{testExternalSkill}}, // MUST survive
			},
		},
		{
			name: "offered agent with no picks is removed (user unconfiguring)",
			existing: map[string]settings.ReviewConfig{
				testAgentName:  {Skills: []string{"/old-pick"}},
				testCodexAgent: {Skills: []string{"/codex-pick"}},
			},
			offered:  map[string]struct{}{testAgentName: {}, testCodexAgent: {}},
			selected: map[string]settings.ReviewConfig{testAgentName: {Skills: []string{"/new-pick"}}},
			want: map[string]settings.ReviewConfig{
				testAgentName: {Skills: []string{"/new-pick"}},
			},
		},
		{
			name:     "empty existing: merge is identity on selected",
			existing: map[string]settings.ReviewConfig{},
			offered:  map[string]struct{}{testAgentName: {}},
			selected: map[string]settings.ReviewConfig{testAgentName: {Skills: []string{"/a"}}},
			want:     map[string]settings.ReviewConfig{testAgentName: {Skills: []string{"/a"}}},
		},
		{
			// Regression: deselecting all curated agents while external
			// config remains must yield a non-empty merged map, so --edit
			// can save the "only external agent left" state.
			name: "deselected curated agent leaves only external entry",
			existing: map[string]settings.ReviewConfig{
				testAgentName:     {Skills: []string{"/old-pick"}},
				testExternalAgent: {Skills: []string{testExternalSkill}},
			},
			offered:  map[string]struct{}{testAgentName: {}},
			selected: map[string]settings.ReviewConfig{}, // user deselected everything offered
			want: map[string]settings.ReviewConfig{
				testExternalAgent: {Skills: []string{testExternalSkill}},
			},
		},
		{
			// A custom prompt without skills is a valid, non-zero entry.
			// Users can configure a freeform review with no skills list.
			name:     "prompt-only entry is preserved",
			existing: map[string]settings.ReviewConfig{},
			offered:  map[string]struct{}{testAgentName: {}},
			selected: map[string]settings.ReviewConfig{
				testAgentName: {Prompt: "Focus on security regressions."},
			},
			want: map[string]settings.ReviewConfig{
				testAgentName: {Prompt: "Focus on security regressions."},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mergePickerResults(tc.existing, tc.offered, tc.selected)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mergePickerResults =\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

func TestNewReviewCmd_NoHiddenFlags(t *testing.T) {
	t.Parallel()
	cmd := newReviewCmd()
	for _, name := range []string{"postreview", "finalize", "session"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("found removed flag: --%s", name)
		}
	}
}

func TestFormatReviewScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		scope reviewScope
		want  string
	}{
		{
			name: "branch ahead of base with uncommitted",
			scope: reviewScope{
				Branch: "feat/foo", Base: testMainBranch,
				AheadCommits: 3, FilesChanged: 7, Uncommitted: 2,
			},
			want: "Reviewing feat/foo vs main: 3 commits, 7 files changed, 2 uncommitted",
		},
		{
			name: "branch ahead of base, clean worktree",
			scope: reviewScope{
				Branch: "feat/foo", Base: testMainBranch,
				AheadCommits: 3, FilesChanged: 7,
			},
			want: "Reviewing feat/foo vs main: 3 commits, 7 files changed",
		},
		{
			name: "on default branch with uncommitted only",
			scope: reviewScope{
				Branch: testMainBranch, Base: testMainBranch, Uncommitted: 4,
			},
			want: "Reviewing main: 4 uncommitted",
		},
		{
			name: "clean default branch — nothing to review",
			scope: reviewScope{
				Branch: testMainBranch, Base: testMainBranch,
			},
			want: "Reviewing main: no changes detected",
		},
		{
			name: "detached HEAD with uncommitted",
			scope: reviewScope{
				HeadSHA: "a3b2c4d", Uncommitted: 1,
			},
			want: "Reviewing HEAD a3b2c4d: 1 uncommitted",
		},
		{
			name: "base unknown, branch only",
			scope: reviewScope{
				Branch: "feat/foo", Uncommitted: 2,
			},
			want: "Reviewing feat/foo: 2 uncommitted",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatReviewScope(tc.scope)
			if got != tc.want {
				t.Errorf("formatReviewScope() =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

func TestDetectReviewScope_FeatureBranchAheadOfMain(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "a.txt", "hello")
	testutil.GitAdd(t, tmp, "a.txt")
	testutil.GitCommit(t, tmp, "init")
	// go-git's PlainInit defaults to master; rename so tests can assume main.
	runGit(t, tmp, "branch", "-M", testMainBranch)
	// Create a feature branch and add two commits touching two files.
	testutil.GitCheckoutNewBranch(t, tmp, "feat/x")
	testutil.WriteFile(t, tmp, "a.txt", "hello v2")
	testutil.GitAdd(t, tmp, "a.txt")
	testutil.GitCommit(t, tmp, "edit a")
	testutil.WriteFile(t, tmp, "b.txt", "new")
	testutil.GitAdd(t, tmp, "b.txt")
	testutil.GitCommit(t, tmp, "add b")
	// And an uncommitted edit.
	testutil.WriteFile(t, tmp, "a.txt", "hello v3")
	t.Chdir(tmp)

	got, err := detectReviewScope(context.Background())
	if err != nil {
		t.Fatalf("detectReviewScope: %v", err)
	}
	if got.Branch != "feat/x" {
		t.Errorf("Branch = %q, want feat/x", got.Branch)
	}
	if got.Base != testMainBranch {
		t.Errorf("Base = %q, want %s", got.Base, testMainBranch)
	}
	if got.AheadCommits != 2 {
		t.Errorf("AheadCommits = %d, want 2", got.AheadCommits)
	}
	if got.FilesChanged != 2 {
		t.Errorf("FilesChanged = %d, want 2", got.FilesChanged)
	}
	if got.Uncommitted != 1 {
		t.Errorf("Uncommitted = %d, want 1", got.Uncommitted)
	}
}

func TestDetectReviewScope_OnDefaultBranchCleanRepo(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "a.txt", "hello")
	testutil.GitAdd(t, tmp, "a.txt")
	testutil.GitCommit(t, tmp, "init")
	runGit(t, tmp, "branch", "-M", testMainBranch)
	t.Chdir(tmp)

	got, err := detectReviewScope(context.Background())
	if err != nil {
		t.Fatalf("detectReviewScope: %v", err)
	}
	if got.Branch != testMainBranch {
		t.Errorf("Branch = %q, want %s", got.Branch, testMainBranch)
	}
	if got.AheadCommits != 0 || got.FilesChanged != 0 || got.Uncommitted != 0 {
		t.Errorf("expected zeros, got %+v", got)
	}
}

// Simulates a fork-like layout: no local `main`, no origin/HEAD symref, but an
// origin/main remote-tracking branch exists. Reproduces the bug where
// detectBaseBranch previously skipped remote-tracking branches and returned "".
func TestDetectBaseBranch_UsesOriginMainWhenNoLocalMain(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "a.txt", "hello")
	testutil.GitAdd(t, tmp, "a.txt")
	testutil.GitCommit(t, tmp, "init")
	// Fake an origin/main remote-tracking ref without configuring origin/HEAD
	// or keeping a local `main` branch. After this: the only place `main`
	// lives is refs/remotes/origin/main.
	headSHA := testutil.GetHeadHash(t, tmp)
	runGit(t, tmp, "update-ref", "refs/remotes/origin/main", headSHA)
	testutil.GitCheckoutNewBranch(t, tmp, "feat/x")
	runGit(t, tmp, "branch", "-D", "master") // go-git default branch

	got := detectBaseBranch(context.Background(), tmp)
	if got != testMainBranch {
		t.Errorf("detectBaseBranch = %q, want %s (should resolve via refs/remotes/origin/main)", got, testMainBranch)
	}
}

// Pins the documented fallback order: when origin/HEAD is unset, ALL remote-
// tracking branches are tried before ANY local branch. Reproduces the drift
// the comment-analyzer caught: if local `main` + remote `origin/master` both
// exist, the code must prefer `master` (the remote) since the remote reflects
// the team's canonical base.
func TestDetectBaseBranch_PrefersAllRemotesOverLocals(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "a.txt", "hello")
	testutil.GitAdd(t, tmp, "a.txt")
	testutil.GitCommit(t, tmp, "init")
	// go-git default branch is `master` — rename to `main` so we have a local
	// `main` but not a local `master`.
	runGit(t, tmp, "branch", "-M", testMainBranch)
	// Fake an origin/master remote-tracking ref; no origin/HEAD.
	headSHA := testutil.GetHeadHash(t, tmp)
	runGit(t, tmp, "update-ref", "refs/remotes/origin/master", headSHA)

	got := detectBaseBranch(context.Background(), tmp)
	if got != "master" {
		t.Errorf("detectBaseBranch = %q, want master (remote origin/master should beat local main)", got)
	}
}

// TestKindIsReview pins the invariant that the umbrella HasReview flag is
// derived from Kind.IsReview. Anyone adding a new review-kind Kind value must
// also add it here, or this test fails.
func TestKindIsReview(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind session.Kind
		want bool
	}{
		{session.KindAgentReview, true},
		{session.Kind(""), false},
		{session.Kind("unknown_kind"), false},
	}
	for _, tc := range tests {
		if got := tc.kind.IsReview(); got != tc.want {
			t.Errorf("(%q).IsReview() = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

// Regression test for the settings-wipe bug: saveReviewConfig must NOT
// overwrite existing settings when settings.json is malformed. It should
// propagate the load error so the caller can surface it, rather than
// silently discarding unrelated configuration.
func TestSaveReviewConfig_ReturnsErrorOnMalformedSettings(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	t.Chdir(tmp)

	// Write a deliberately malformed settings.json with user content we
	// must not clobber.
	entireDir := filepath.Join(tmp, ".entire")
	if err := os.MkdirAll(entireDir, 0o750); err != nil {
		t.Fatal(err)
	}
	malformed := []byte(`{"enabled": true, "strategy": "manual-commit", "review": {`) // truncated
	if err := os.WriteFile(filepath.Join(entireDir, "settings.json"), malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(entireDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	err = saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testAgentName: {Skills: []string{testReviewSkill}},
	})
	if err == nil {
		t.Fatal("expected saveReviewConfig to error on malformed settings; returned nil (data-loss bug)")
	}

	after, err := os.ReadFile(filepath.Join(entireDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("settings.json was overwritten on load error:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestPickerForm_StructureWithDiscovery(t *testing.T) {
	t.Parallel()
	fields := buildReviewPickerFields(
		"claude-code",
		[]skilldiscovery.CuratedSkill{{Name: "/review", Desc: "x"}},
		[]agent.DiscoveredSkill{{Name: "/pr-review-toolkit:review-pr", Description: "y"}},
		[]skilldiscovery.InstallHint{{Message: "install more"}},
		"",                                                           /* previousPrompt */
		nil /* builtinPicksOut */, nil /* discoveredPicksOut */, nil, /* promptOut */
	)
	if len(fields) != 4 {
		t.Fatalf("picker fields = %d, want 4 (built-in, discovered, hint, prompt)", len(fields))
	}
}

func TestPickerForm_EmptyBuiltinsRendersNote(t *testing.T) {
	t.Parallel()
	fields := buildReviewPickerFields(
		"gemini-cli",
		nil,
		nil,
		[]skilldiscovery.InstallHint{{Message: "install gemini-code-review"}},
		"",
		nil, nil, nil,
	)
	if len(fields) != 4 {
		t.Fatalf("fields = %d, want 4 even with empty built-ins and discovered", len(fields))
	}
	for i, f := range fields {
		if f == nil {
			t.Errorf("fields[%d] is nil — every slot must be populated", i)
		}
	}
}

// Regression: the picker header claims "Previously-saved skills are
// pre-checked" — without a functioning split + Option.Selected(true),
// running `entire review --edit` and accepting defaults silently wipes
// the agent's saved skills. splitSavedPicks partitions the saved flat
// list into the two picker buckets so matching options can be
// pre-selected downstream.
func TestSplitSavedPicks(t *testing.T) {
	t.Parallel()
	builtins := []skilldiscovery.CuratedSkill{
		{Name: "/review"},
		{Name: "/test-auditor"},
	}
	discovered := []agent.DiscoveredSkill{
		{Name: "/pr-review-toolkit:review-pr"},
		{Name: "/my-plugin:lint"},
	}

	tests := []struct {
		name           string
		saved          []string
		wantBuiltin    []string
		wantDiscovered []string
	}{
		{
			name:        "all matches — both buckets populated",
			saved:       []string{"/review", "/pr-review-toolkit:review-pr", "/test-auditor"},
			wantBuiltin: []string{"/review", "/test-auditor"},
			wantDiscovered: []string{
				"/pr-review-toolkit:review-pr",
			},
		},
		{
			name:           "only builtins saved",
			saved:          []string{"/review"},
			wantBuiltin:    []string{"/review"},
			wantDiscovered: nil,
		},
		{
			name:           "only discovered saved",
			saved:          []string{"/my-plugin:lint"},
			wantBuiltin:    nil,
			wantDiscovered: []string{"/my-plugin:lint"},
		},
		{
			name:           "unknown saved skill drops from both (uninstalled/external)",
			saved:          []string{"/ghost"},
			wantBuiltin:    nil,
			wantDiscovered: nil,
		},
		{
			name:           "empty saved returns empty",
			saved:          nil,
			wantBuiltin:    nil,
			wantDiscovered: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotBuiltin, gotDiscovered := splitSavedPicks(tc.saved, builtins, discovered)
			if !reflect.DeepEqual(gotBuiltin, tc.wantBuiltin) {
				t.Errorf("builtin = %v, want %v", gotBuiltin, tc.wantBuiltin)
			}
			if !reflect.DeepEqual(gotDiscovered, tc.wantDiscovered) {
				t.Errorf("discovered = %v, want %v", gotDiscovered, tc.wantDiscovered)
			}
		})
	}
}

// preselectedSet turns the caller's output slice into a lookup set the
// picker uses to mark options Selected(true). Nil or empty input yields
// nil so `if _, ok := set[name]; ok` works in either case.
func TestPreselectedSet(t *testing.T) {
	t.Parallel()
	if got := preselectedSet(nil); got != nil {
		t.Errorf("nil slice = %v, want nil", got)
	}
	var empty []string
	if got := preselectedSet(&empty); got != nil {
		t.Errorf("empty slice = %v, want nil", got)
	}
	populated := []string{"/a", "/b"}
	set := preselectedSet(&populated)
	if _, ok := set["/a"]; !ok {
		t.Error("set missing /a")
	}
	if _, ok := set["/b"]; !ok {
		t.Error("set missing /b")
	}
	if _, ok := set["/c"]; ok {
		t.Error("set unexpectedly contains /c")
	}
}

func TestPickerForm_AllHintsSuppressedHidesSection(t *testing.T) {
	t.Parallel()
	fields := buildReviewPickerFields(
		"claude-code",
		[]skilldiscovery.CuratedSkill{{Name: "/review", Desc: "x"}},
		nil,
		nil, /* no active hints */
		"",
		nil, nil, nil,
	)
	if len(fields) != 3 {
		t.Errorf("fields count = %d, want 3 (hint section omitted when empty)", len(fields))
	}
}

// Regression: when settings.json names a review skill that isn't installed
// as a built-in or on-disk plugin skill, `entire review` must abort with a
// user-facing error before writing the pending marker. Without this guard,
// the marker gets claimed by the agent's session and the agent silently
// fails with "I don't have that skill" — no recoverable signal to the user.
func TestRunReview_MissingConfiguredSkillAbortsBeforeMarker(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testAgentName)

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testAgentName: {Skills: []string{"/bogus:skill-does-not-exist"}},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := NewRootCmd()
	errBuf := &bytes.Buffer{}
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"review"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when configured skill not installed")
	}
	if !strings.Contains(errBuf.String(), "not installed") {
		t.Errorf("stderr should mention 'not installed', got: %s", errBuf.String())
	}
	_, markerExists, markerErr := ReadPendingReviewMarker(context.Background())
	if markerErr != nil {
		t.Fatalf("ReadPendingReviewMarker: %v", markerErr)
	}
	if markerExists {
		t.Error("pending marker should not exist when verification fails")
	}
}

// Prompt-only configs (no Skills list) must skip the installed-skill
// verification: a freeform review prompt can't be validated against any
// registry, and blocking would break a documented use case. The marker
// must still be written normally.
func TestRunReview_PromptOnlyConfigSkipsVerification(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, "cursor")

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		"cursor": {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	// Stub the run-context prompt — no TTY in tests.
	deps := runReviewDeps{
		promptForRunContextFn: func(_ context.Context) (string, error) { return "", nil },
	}
	reviewCmd := newReviewCmdWithDeps(deps)
	buf := &bytes.Buffer{}
	reviewCmd.SetOut(buf)
	if err := reviewCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, markerExists, markerErr := ReadPendingReviewMarker(context.Background())
	if markerErr != nil {
		t.Fatalf("ReadPendingReviewMarker: %v", markerErr)
	}
	if !markerExists {
		t.Error("marker should exist for prompt-only config")
	}
}

// Non-launchable agents used by the picker tests below. Non-launchable is
// important: runReview registers a defer that clears the marker on exit
// for launchable agents, which would defeat the "marker was written with
// agent X" assertion. See TestRunReview_NonLaunchableAgentPreservesMarker
// for the same pattern.
const (
	testPickerAgentA = "cursor"   // alphabetically first
	testPickerAgentB = "opencode" // alphabetically second
)

// TestPromptForAgent_SingleEligibleSkipsPicker: when only one agent is
// configured AND has hooks, the picker never fires and runReview proceeds
// directly to spawn.
func TestPromptForAgent_SingleEligibleSkipsPicker(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testPickerAgentA)

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testPickerAgentA: {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	called := false
	deps := runReviewDeps{
		promptForAgentFn: func(_ context.Context, _ []agentChoice) (string, error) {
			called = true
			return "", nil
		},
		// Stub run-context prompt — no TTY in tests.
		promptForRunContextFn: func(_ context.Context) (string, error) { return "", nil },
	}

	reviewCmd := newReviewCmdWithDeps(deps)
	if err := reviewCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("promptForAgentFn should not be called when only one eligible agent is configured")
	}
}

// TestPromptForAgent_FlagOverrideSkipsPicker: when --agent is passed, the
// picker is skipped even with multiple eligible agents configured.
func TestPromptForAgent_FlagOverrideSkipsPicker(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testPickerAgentA)
	installHooksForTest(t, testPickerAgentB)

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testPickerAgentA: {Prompt: "review the diff"},
		testPickerAgentB: {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	called := false
	deps := runReviewDeps{
		promptForAgentFn: func(_ context.Context, _ []agentChoice) (string, error) {
			called = true
			return "", nil
		},
		// Stub run-context prompt — no TTY in tests.
		promptForRunContextFn: func(_ context.Context) (string, error) { return "", nil },
	}

	reviewCmd := newReviewCmdWithDeps(deps)
	reviewCmd.SetArgs([]string{"--agent", testPickerAgentB})
	if err := reviewCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("promptForAgentFn should not be called when --agent is passed")
	}

	m, ok, err := ReadPendingReviewMarker(context.Background())
	if err != nil || !ok {
		t.Fatalf("marker should be written: ok=%v err=%v", ok, err)
	}
	if m.AgentName != testPickerAgentB {
		t.Errorf("AgentName = %q, want %s", m.AgentName, testPickerAgentB)
	}
}

// TestPromptForAgent_FlagOverrideMustBeEligibleAgent: --agent NAME where
// NAME is configured but has no hooks → clear error via existing gate.
func TestPromptForAgent_FlagOverrideMustBeEligibleAgent(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testPickerAgentA)
	// Configure a second agent too but do NOT install its hooks.

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testPickerAgentA: {Prompt: "review the diff"},
		testPickerAgentB: {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := NewRootCmd()
	errBuf := &bytes.Buffer{}
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"review", "--agent", testPickerAgentB})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --agent points at hookless agent")
	}
	if !strings.Contains(errBuf.String(), "Hooks are not installed") {
		t.Errorf("stderr should mention 'Hooks are not installed', got: %s", errBuf.String())
	}
}

// TestRunReview_MultiAgentNoFlagTriggersPicker: canonical picker-fires path.
// Two eligible agents, no flag, stubbed promptForAgentFn returns the second
// (alphabetically) → marker written with that agent.
func TestRunReview_MultiAgentNoFlagTriggersPicker(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testPickerAgentA)
	installHooksForTest(t, testPickerAgentB)

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testPickerAgentA: {Prompt: "review the diff"},
		testPickerAgentB: {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	var seen []string
	deps := runReviewDeps{
		promptForAgentFn: func(_ context.Context, eligible []agentChoice) (string, error) {
			for _, e := range eligible {
				seen = append(seen, e.Name)
			}
			if len(eligible) < 2 {
				t.Fatalf("expected >=2 eligible, got %d", len(eligible))
			}
			// Alphabetical ordering; pick the second one.
			return eligible[1].Name, nil
		},
		// Stub run-context prompt — no TTY in tests.
		promptForRunContextFn: func(_ context.Context) (string, error) { return "", nil },
	}

	reviewCmd := newReviewCmdWithDeps(deps)
	if err := reviewCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(seen) < 2 || seen[0] != testPickerAgentA || seen[1] != testPickerAgentB {
		t.Errorf("eligible presented to picker = %v, want [%s %s]", seen, testPickerAgentA, testPickerAgentB)
	}

	m, ok, err := ReadPendingReviewMarker(context.Background())
	if err != nil || !ok {
		t.Fatalf("marker should be written: ok=%v err=%v", ok, err)
	}
	if m.AgentName != testPickerAgentB {
		t.Errorf("AgentName = %q, want %s", m.AgentName, testPickerAgentB)
	}
}

// TestPromptForAgents_CallsInjection pins the dispatch contract: when the
// deps hook is set and ≥2 HeadlessLauncher agents are eligible, runReview
// routes to the multi-select picker AND the multi-agent orchestrator. Proves
// the injection wiring works before Chunk 3's real orchestrator lands.
//
// Uses claude-code + codex because they implement HeadlessLauncher — only
// these get offered in the multi-agent picker per the dispatch switch.
func TestPromptForAgents_CallsInjection(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testAgentName)
	installHooksForTest(t, testCodexAgent)

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testAgentName:  {Prompt: "review the diff"},
		testCodexAgent: {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	promptCalled := false
	multiCalled := false
	deps := runReviewDeps{
		promptForAgentsFn: func(_ context.Context, eligible []agentChoice) ([]string, error) {
			promptCalled = true
			if len(eligible) < 2 {
				t.Fatalf("expected >=2 eligible, got %d", len(eligible))
			}
			return []string{eligible[0].Name, eligible[1].Name}, nil
		},
		// Stub run-context prompt — no TTY in tests.
		promptForRunContextFn: func(_ context.Context) (string, error) { return "", nil },
		runMultiAgentFn: func(_ context.Context, tasks []MultiAgentTask, _ io.Writer) (MultiRunResult, error) {
			multiCalled = true
			if len(tasks) != 2 {
				t.Fatalf("expected 2 tasks, got %d", len(tasks))
			}
			// Minimal MultiRunResult so the dispatch returns OK.
			return MultiRunResult{
				Runs: []AgentRunResult{
					{Name: tasks[0].Name, Status: AgentRunDone},
					{Name: tasks[1].Name, Status: AgentRunDone},
				},
			}, nil
		},
	}

	reviewCmd := newReviewCmdWithDeps(deps)
	if err := reviewCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !promptCalled {
		t.Error("promptForAgentsFn should have been called")
	}
	if !multiCalled {
		t.Error("runMultiAgentFn should have been called")
	}
}

// TestPromptForAgents_SingleSelectionFallsBackToSingleAgent pins that
// selecting exactly one agent in the multi-select picker routes to the 2.1
// single-agent path rather than the orchestrator.
//
// Deviation from plan: the plan proposes using non-launchable agents
// (cursor, opencode) to avoid spawning a real subprocess. But the dispatch
// switch filters the multi-select picker to HeadlessLauncher agents only —
// cursor/opencode wouldn't reach promptForAgentsFn at all. Using
// claude-code/codex (HeadlessLauncher) fires the picker, then the picker
// callback cancels the shared context so the downstream single-agent spawn
// aborts immediately (rather than actually running an agent). We assert on
// runMultiAgentFn NOT being called rather than on marker state, since the
// spawn-failure defer clears the marker anyway.
func TestPromptForAgents_SingleSelectionFallsBackToSingleAgent(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testAgentName)
	installHooksForTest(t, testCodexAgent)

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testAgentName:  {Prompt: "review"},
		testCodexAgent: {Prompt: "review"},
	}); err != nil {
		t.Fatal(err)
	}

	// Cancellable context so the single-agent fallback's spawn is aborted
	// before it actually runs the agent. The picker callback cancels right
	// after it returns a single selection.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	promptCalled := false
	multiCalled := false
	deps := runReviewDeps{
		promptForAgentsFn: func(_ context.Context, eligible []agentChoice) ([]string, error) {
			promptCalled = true
			// Cancel so the single-agent fallback's exec.CommandContext
			// is already cancelled when Run() fires — exits immediately
			// instead of running a real agent.
			cancel()
			// Return just one — single-agent fallback expected.
			return []string{eligible[0].Name}, nil
		},
		runMultiAgentFn: func(_ context.Context, _ []MultiAgentTask, _ io.Writer) (MultiRunResult, error) {
			multiCalled = true
			return MultiRunResult{}, nil
		},
	}

	reviewCmd := newReviewCmdWithDeps(deps)
	// The final error is expected (the single-agent fallback's spawn is
	// cancelled via the shared ctx). What matters for dispatch is the call
	// sites below — if execute unexpectedly succeeds, that's fine too.
	if err := reviewCmd.ExecuteContext(ctx); err != nil {
		t.Logf("execute (ctx-cancelled, expected): %v", err)
	}

	if !promptCalled {
		t.Error("promptForAgentsFn should have been called")
	}
	if multiCalled {
		t.Error("runMultiAgentFn should NOT have been called for single-selection fallback")
	}
}

// TestPromptForAgents_EmptySelectionCancels pins that returning [] from the
// multi-select picker is treated as user cancel — silent error, no marker
// written, no orchestrator call.
func TestPromptForAgents_EmptySelectionCancels(t *testing.T) {
	setupReviewTestRepoWithCommit(t)
	installHooksForTest(t, testAgentName)
	installHooksForTest(t, testCodexAgent)

	if err := saveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		testAgentName:  {Prompt: "review"},
		testCodexAgent: {Prompt: "review"},
	}); err != nil {
		t.Fatal(err)
	}

	multiCalled := false
	deps := runReviewDeps{
		promptForAgentsFn: func(_ context.Context, _ []agentChoice) ([]string, error) {
			return []string{}, nil // empty = cancel
		},
		runMultiAgentFn: func(_ context.Context, _ []MultiAgentTask, _ io.Writer) (MultiRunResult, error) {
			multiCalled = true
			return MultiRunResult{}, nil
		},
	}

	reviewCmd := newReviewCmdWithDeps(deps)
	err := reviewCmd.Execute()
	if err == nil {
		t.Fatal("expected silent cancel error, got nil")
	}
	if multiCalled {
		t.Error("runMultiAgentFn should NOT have been called after user cancel")
	}
	_, markerExists, readErr := ReadPendingReviewMarker(context.Background())
	if readErr != nil {
		t.Fatalf("ReadPendingReviewMarker: %v", readErr)
	}
	if markerExists {
		t.Error("marker should not have been written on cancel")
	}
}
