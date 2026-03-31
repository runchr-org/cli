package paths

import (
	"fmt"
	"os"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/osroot"
)

// GetWorktreeID returns the internal git worktree identifier for the given path.
// For the main worktree (where .git is a directory), returns empty string.
// For linked worktrees (where .git is a file), extracts the name from
// .git/worktrees/<name>/ path. This name is stable across `git worktree move`.
// Uses os.Root for traversal-resistant access.
func GetWorktreeID(worktreePath string) (string, error) {
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return "", fmt.Errorf("failed to open worktree path: %w", err)
	}
	defer root.Close()

	info, err := root.Stat(".git")
	if err != nil {
		return "", fmt.Errorf("failed to stat .git: %w", err)
	}

	// Main worktree has .git as a directory
	if info.IsDir() {
		return "", nil
	}

	// Linked worktree has .git as a file with content: "gitdir: /path/to/.git/worktrees/<name>"
	content, err := osroot.ReadFile(root, ".git")
	if err != nil {
		return "", fmt.Errorf("failed to read .git file: %w", err)
	}

	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "gitdir: ") {
		return "", fmt.Errorf("invalid .git file format: %s", line)
	}

	gitdir := strings.TrimPrefix(line, "gitdir: ")

	// Extract worktree name from path like /repo/.git/worktrees/<name>
	// or /repo/.bare/worktrees/<name> (bare repo + worktree layout).
	// The path after the marker is the worktree identifier.
	var worktreeID string
	var found bool
	for _, marker := range []string{".git/worktrees/", ".bare/worktrees/"} {
		_, worktreeID, found = strings.Cut(gitdir, marker)
		if found {
			break
		}
	}
	if !found {
		return "", fmt.Errorf("unexpected gitdir format (no worktrees): %s", gitdir)
	}
	// Remove trailing slashes if any
	worktreeID = strings.TrimSuffix(worktreeID, "/")

	return worktreeID, nil
}
