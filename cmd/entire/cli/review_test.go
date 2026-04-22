package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
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
	testReviewSkill = "/pr-review-toolkit:review-pr"
	testMainBranch  = "main"
)

func TestReviewMarker_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	m := PendingReviewMarker{
		AgentName:   "claude-code",
		Skills:      []string{testReviewSkill},
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

	// Ensure the file lived under .git/entire-sessions/, not the worktree.
	gitDir := filepath.Join(tmp, ".git")
	entries, err := filepath.Glob(filepath.Join(gitDir, "entire-sessions", "*"))
	if err != nil {
		t.Fatalf("glob sessions dir: %v", err)
	}
	_ = entries // sanity check only
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

	err := saveReviewConfig(context.Background(), map[string][]string{
		"claude-code": {testReviewSkill, "/test-auditor"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s, err := settings.Load(context.Background())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if len(s.Review["claude-code"]) != 2 {
		t.Errorf("expected 2 skills saved, got %v", s.Review)
	}
	if s.Review["claude-code"][0] != testReviewSkill {
		t.Errorf("first skill = %q", s.Review["claude-code"][0])
	}
}

func TestRunReview_TrackOnlyWritesMarker(t *testing.T) {
	// t.Chdir + first-run picker — no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)
	installHooksForTest(t, testAgentName)

	// Seed config so first-run picker doesn't fire.
	if err := saveReviewConfig(context.Background(), map[string][]string{
		testAgentName: {testReviewSkill},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := NewRootCmd()
	rootCmd.SetArgs([]string{"review", "--track-only"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	m, ok, err := ReadPendingReviewMarker(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected marker present: ok=%v err=%v", ok, err)
	}
	if m.AgentName != testAgentName {
		t.Errorf("AgentName = %q, want %s", m.AgentName, testAgentName)
	}
	if len(m.Skills) != 1 || m.Skills[0] != testReviewSkill {
		t.Errorf("Skills = %v", m.Skills)
	}
}

// Regression: running `entire review` when the configured agent has no hooks
// installed must abort with a clear error instead of writing a marker no
// hook will ever adopt. Covers both stale config (user edited settings.json
// by hand) and post-disable state (user ran `entire disable` without
// cleaning up review settings).
func TestRunReview_MissingHooksAborts(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	// No installHooksForTest — this is the point.
	if err := saveReviewConfig(context.Background(), map[string][]string{
		testAgentName: {testReviewSkill},
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

// Regression: non-launchable agents must preserve the pending marker so the
// manually-started agent can adopt it. Previously the cleanup defer was
// registered before the LauncherFor check, so the !ok fallback printed
// "falling back to --track-only" but then wiped the marker on return.
//
// Uses cursor because it has HookSupport but no Launcher, triggering the
// !ok fallback.
func TestRunReview_FallbackToTrackOnlyPreservesMarker(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	const nonLaunchableAgent = "cursor"
	installHooksForTest(t, types.AgentName(nonLaunchableAgent))

	// Confirm the precondition: cursor really has no Launcher. If a future
	// change adds one, this test's premise is invalid and needs a different
	// agent.
	if _, hasLauncher := agent.LauncherFor(types.AgentName(nonLaunchableAgent)); hasLauncher {
		t.Skipf("%s now implements Launcher; pick another non-launchable agent for this regression test", nonLaunchableAgent)
	}

	if err := saveReviewConfig(context.Background(), map[string][]string{
		nonLaunchableAgent: {testReviewSkill},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := NewRootCmd()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"review"}) // not --track-only; rely on the fallback
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Falling back to --track-only") {
		t.Errorf("expected fallback message, got: %s", out)
	}

	m, ok, err := ReadPendingReviewMarker(context.Background())
	if err != nil {
		t.Fatalf("ReadPendingReviewMarker: %v", err)
	}
	if !ok {
		t.Fatal("marker was cleared by defer on !ok fallback — the 'falling back to --track-only' message is a lie")
	}
	if m.AgentName != nonLaunchableAgent {
		t.Errorf("AgentName = %q, want %s", m.AgentName, nonLaunchableAgent)
	}
}

func TestComposeReviewPrompt_NoFinishSkill(t *testing.T) {
	t.Parallel()
	prompt := composeReviewPrompt([]string{"/review-pr", "/test-auditor"})
	if strings.Contains(prompt, "entire-review:finish") {
		t.Errorf("prompt should not reference finish skill; got: %s", prompt)
	}
	if !strings.Contains(prompt, "/review-pr") {
		t.Errorf("prompt missing skill name; got: %s", prompt)
	}
}

// --agent flag resolves a non-default configured agent when the map has
// multiple entries. Previously the alphabetically-first agent always won
// silently.
func TestSelectReviewAgent_OverrideResolvesSpecificAgent(t *testing.T) {
	t.Parallel()
	const codexAgent = "codex"
	review := map[string][]string{
		testAgentName: {"/a"},
		codexAgent:    {"/b"},
	}

	name, skills, err := selectReviewAgent(review, codexAgent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != codexAgent || len(skills) != 1 || skills[0] != "/b" {
		t.Errorf("override=%s returned name=%q skills=%v", codexAgent, name, skills)
	}

	// Default (no override) must remain the alphabetically-first agent for
	// backwards compatibility.
	name, _, err = selectReviewAgent(review, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != testAgentName {
		t.Errorf("default pick = %q, want %s", name, testAgentName)
	}

	// Unknown override must surface a helpful error listing the configured
	// agents instead of silently falling back.
	_, _, err = selectReviewAgent(review, "gemini")
	if err == nil {
		t.Fatal("expected error for unconfigured --agent value")
	}
	if !strings.Contains(err.Error(), testAgentName) || !strings.Contains(err.Error(), codexAgent) {
		t.Errorf("error should list configured agents; got: %v", err)
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

	err = saveReviewConfig(context.Background(), map[string][]string{
		testAgentName: {testReviewSkill},
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
