package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/osroot"
)

// worktreeMarkers are the path segments that identify a git worktree admin
// directory inside the gitdir value. Both standard (.git/worktrees/) and
// bare-repo (.bare/worktrees/) layouts are supported.
var worktreeMarkers = []string{".git/worktrees/", ".bare/worktrees/"}

// parseGitfile reads a .git gitfile via os.Root and returns the raw gitdir value.
// Returns empty string and no error if .git is a directory (main worktree).
// Returns an error if .git doesn't exist or cannot be read.
func parseGitfile(worktreePath string) (string, error) {
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return "", fmt.Errorf("failed to open path: %w", err)
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

	content, err := osroot.ReadFile(root, ".git")
	if err != nil {
		return "", fmt.Errorf("failed to read .git file: %w", err)
	}

	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "gitdir: ") {
		return "", fmt.Errorf("invalid .git file format: %s", line)
	}

	gitdir := strings.TrimPrefix(line, "gitdir: ")

	// Resolve relative gitdir paths against the worktree root.
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktreePath, gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	// Normalize to forward slashes for consistent marker matching.
	gitdir = filepath.ToSlash(gitdir)

	return gitdir, nil
}

// hasWorktreeMarker reports whether a gitdir value contains a worktree admin
// marker (e.g. ".git/worktrees/" or ".bare/worktrees/"). This distinguishes
// linked worktrees from submodules and other gitfile-based layouts.
func hasWorktreeMarker(gitdir string) bool {
	for _, marker := range worktreeMarkers {
		if strings.Contains(gitdir, marker) {
			return true
		}
	}
	return false
}

// GetWorktreeID returns the internal git worktree identifier for the given path.
// For the main worktree (where .git is a directory), returns empty string.
// For linked worktrees (where .git is a file pointing into a worktree admin dir),
// extracts the name from the .git/worktrees/<name>/ path. This name is stable
// across `git worktree move`.
// Returns an error for non-worktree gitfiles (e.g. submodules).
// Uses os.Root for traversal-resistant access.
func GetWorktreeID(worktreePath string) (string, error) {
	gitdir, err := parseGitfile(worktreePath)
	if err != nil {
		return "", err
	}

	// Main worktree: .git is a directory, gitdir is empty.
	if gitdir == "" {
		return "", nil
	}

	// Extract worktree name from path like /repo/.git/worktrees/<name>
	// or /repo/.bare/worktrees/<name> (bare repo + worktree layout).
	var worktreeID string
	var found bool
	for _, marker := range worktreeMarkers {
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
