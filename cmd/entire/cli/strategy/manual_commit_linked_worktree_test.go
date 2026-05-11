package strategy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/session"
)

// TestFindSessionsForWorktree_LinkedWorktreeMatchesMain verifies that a hook
// firing from inside a linked worktree (such as Claude Code's
// .claude/worktrees/<branch> agent feature) can still find sessions whose
// WorktreePath was registered against the main worktree.
//
// Regression coverage for the path where prepare-commit-msg / post-commit
// silently bail with "no active sessions" — strict path equality on
// WorktreePath was returning no matches because the session was created from
// the main worktree (where the user's prompt and UserPromptSubmit hook fired)
// but the commit fires from inside an agent-spawned linked worktree.
func TestFindSessionsForWorktree_LinkedWorktreeMatchesMain(t *testing.T) {
	mainDir := t.TempDir()
	// EvalSymlinks so /var → /private/var on macOS — git rev-parse returns the
	// canonical form, and we need to compare like-for-like in the test below.
	resolved, err := filepath.EvalSymlinks(mainDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	mainDir = resolved
	initTestRepo(t, mainDir)

	linkedDir := filepath.Join(mainDir, ".claude", "worktrees", "feature-x")
	if err := createWorktree(mainDir, linkedDir, "feature-x"); err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	t.Cleanup(func() { removeWorktree(mainDir, linkedDir) })

	t.Chdir(linkedDir)

	strat := NewManualCommitStrategy()
	ctx := context.Background()

	// Active session registered against the MAIN worktree path. Phase=Active so
	// listAllSessionStates doesn't prune it for lacking a shadow branch.
	mainSession := &session.State{
		SessionID:    "11111111-1111-1111-1111-111111111111",
		BaseCommit:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		WorktreePath: mainDir,
		StartedAt:    time.Now(),
		Phase:        session.PhaseActive,
	}
	if err := strat.saveSessionState(ctx, mainSession); err != nil {
		t.Fatalf("saveSessionState (main): %v", err)
	}

	// Session registered against an unrelated sibling worktree — must NOT
	// match a commit firing from linkedDir.
	siblingDir := filepath.Join(mainDir, ".claude", "worktrees", "sibling")
	otherSession := &session.State{
		SessionID:    "22222222-2222-2222-2222-222222222222",
		BaseCommit:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		WorktreePath: siblingDir,
		StartedAt:    time.Now(),
		Phase:        session.PhaseActive,
	}
	if err := strat.saveSessionState(ctx, otherSession); err != nil {
		t.Fatalf("saveSessionState (sibling): %v", err)
	}

	matched, err := strat.findSessionsForWorktree(ctx, linkedDir)
	if err != nil {
		t.Fatalf("findSessionsForWorktree: %v", err)
	}

	gotIDs := map[string]bool{}
	for _, s := range matched {
		gotIDs[s.SessionID] = true
	}
	if !gotIDs[mainSession.SessionID] {
		t.Errorf("expected main-worktree session %q to be returned from linked worktree, got %+v", mainSession.SessionID, gotIDs)
	}
	if gotIDs[otherSession.SessionID] {
		t.Errorf("sibling-worktree session %q should NOT match from linked worktree %q", otherSession.SessionID, linkedDir)
	}
}

// TestFindSessionsForWorktree_MainWorktreeUnchanged verifies the widening is
// strictly one-directional: from the main worktree we must not start
// returning sessions whose WorktreePath points at a linked worktree.
// Otherwise a commit on main would silently pick up an unrelated agent
// session running in .claude/worktrees/<branch> and link the commit to it.
func TestFindSessionsForWorktree_MainWorktreeUnchanged(t *testing.T) {
	mainDir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(mainDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	mainDir = resolved
	initTestRepo(t, mainDir)
	t.Chdir(mainDir)

	strat := NewManualCommitStrategy()
	ctx := context.Background()

	mainSession := &session.State{
		SessionID:    "33333333-3333-3333-3333-333333333333",
		BaseCommit:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		WorktreePath: mainDir,
		StartedAt:    time.Now(),
		Phase:        session.PhaseActive,
	}
	if err := strat.saveSessionState(ctx, mainSession); err != nil {
		t.Fatalf("saveSessionState (main): %v", err)
	}

	linkedDir := filepath.Join(mainDir, ".claude", "worktrees", "feature-x")
	linkedSession := &session.State{
		SessionID:    "44444444-4444-4444-4444-444444444444",
		BaseCommit:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		WorktreePath: linkedDir,
		StartedAt:    time.Now(),
		Phase:        session.PhaseActive,
	}
	if err := strat.saveSessionState(ctx, linkedSession); err != nil {
		t.Fatalf("saveSessionState (linked): %v", err)
	}

	matched, err := strat.findSessionsForWorktree(ctx, mainDir)
	if err != nil {
		t.Fatalf("findSessionsForWorktree: %v", err)
	}

	gotIDs := map[string]bool{}
	for _, s := range matched {
		gotIDs[s.SessionID] = true
	}
	if !gotIDs[mainSession.SessionID] {
		t.Errorf("main-worktree session must be returned when looking up from main, got %+v", gotIDs)
	}
	if gotIDs[linkedSession.SessionID] {
		t.Errorf("linked-worktree session %q must NOT bleed into main-worktree lookup, got %+v", linkedSession.SessionID, gotIDs)
	}
}
