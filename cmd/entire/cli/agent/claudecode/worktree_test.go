package claudecode

import (
	"testing"
)

// TestGetSessionDir_WorktreePathDiffers verifies that GetSessionDir produces
// different paths for a main repo vs a worktree under .claude/worktrees/.
// This confirms the root cause of the bug: naive use of WorktreeRoot() as the
// GetSessionDir input produces the wrong project directory in Claude worktrees.
//
// The fix is in cli/transcript.go which now uses paths.MainRepoRoot() instead
// of paths.WorktreeRoot() to resolve the main repo path before calling GetSessionDir.
func TestGetSessionDir_WorktreePathDiffers(t *testing.T) {
	t.Parallel()

	ag := &ClaudeCodeAgent{}

	// Simulate paths that WorktreeRoot would return
	mainRepo := "/home/user/my-project"
	worktree := "/home/user/my-project/.claude/worktrees/feature-branch"

	mainDir, err := ag.GetSessionDir(mainRepo)
	if err != nil {
		t.Fatalf("GetSessionDir(mainRepo) error: %v", err)
	}

	worktreeDir, err := ag.GetSessionDir(worktree)
	if err != nil {
		t.Fatalf("GetSessionDir(worktree) error: %v", err)
	}

	// These MUST differ, proving that GetSessionDir cannot be called with
	// a worktree path when the intent is to find the main repo's project dir.
	if mainDir == worktreeDir {
		t.Fatalf("expected different session dirs for main repo vs worktree paths, but both are %q", mainDir)
	}

	// Verify the worktree path contains the extra segments
	expectedMainSanitized := SanitizePathForClaude(mainRepo)
	expectedWTSanitized := SanitizePathForClaude(worktree)

	if expectedMainSanitized == expectedWTSanitized {
		t.Fatalf("sanitized paths should differ: main=%q, worktree=%q",
			expectedMainSanitized, expectedWTSanitized)
	}
}
