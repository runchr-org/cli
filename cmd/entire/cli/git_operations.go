package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/gitprovider"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/strategy"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// openRepository opens the git repository with linked worktree support enabled.
// This is a convenience wrapper around strategy.OpenRepository() for use in the CLI package.
// Kept for backward compatibility with tests and code that still needs *git.Repository.
func openRepository(ctx context.Context) (*git.Repository, error) {
	repo, err := strategy.OpenRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}
	return repo, nil
}

// openProvider opens a gitprovider.Repository using the default configuration.
// This is the preferred way to access git operations in the CLI package.
func openProvider(ctx context.Context) (gitprovider.Repository, error) {
	repo, err := gitprovider.OpenDefault(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open provider: %w", err)
	}
	return repo, nil
}

// GitAuthor represents the git user configuration
type GitAuthor struct {
	Name  string
	Email string
}

// GetGitAuthor retrieves the git user.name and user.email from the repository config.
// Uses gitprovider.Repository.ConfigValue to read git config values.
// Returns fallback defaults if no user is configured anywhere.
func GetGitAuthor(ctx context.Context) (*GitAuthor, error) {
	repo, err := openProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}

	name, _ := repo.ConfigValue(ctx, "user.name") //nolint:errcheck // Best-effort, falls back to default
	if name == "" {
		name = "Unknown"
	}

	email, _ := repo.ConfigValue(ctx, "user.email") //nolint:errcheck // Best-effort, falls back to default
	if email == "" {
		email = "unknown@local"
	}

	return &GitAuthor{
		Name:  name,
		Email: email,
	}, nil
}

// IsOnDefaultBranch checks if the repository is currently on the default branch.
// It determines the default branch by:
// 1. Checking the remote origin's HEAD reference
// 2. Falling back to common names (main, master) if remote HEAD is unavailable
// Returns (isDefault, branchName, error)
func IsOnDefaultBranch(ctx context.Context) (bool, string, error) {
	repo, err := openProvider(ctx)
	if err != nil {
		return false, "", fmt.Errorf("failed to open git repository: %w", err)
	}

	// Get current branch
	head, err := repo.Head()
	if err != nil {
		return false, "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	if !head.Name().IsBranch() {
		// Detached HEAD - not on any branch
		return false, "", nil
	}

	currentBranch := head.Name().Short()

	// Try to get default branch from remote origin's HEAD
	defaultBranch := getDefaultBranchFromRemote(repo)

	// If we couldn't determine from remote, use common defaults
	if defaultBranch == "" {
		// Check if current branch is a common default name
		if currentBranch == "main" || currentBranch == "master" {
			return true, currentBranch, nil
		}
		return false, currentBranch, nil
	}

	return currentBranch == defaultBranch, currentBranch, nil
}

// getDefaultBranchFromRemote tries to determine the default branch from the origin remote.
// Returns empty string if unable to determine.
func getDefaultBranchFromRemote(repo gitprovider.Repository) string {
	// Try to get the symbolic reference for origin/HEAD
	ref, err := repo.GetReference(plumbing.NewRemoteReferenceName("origin", "HEAD"), true)
	if err == nil && ref != nil {
		// ref.Target() gives us something like "refs/remotes/origin/main"
		target := ref.Target().String()
		if strings.HasPrefix(target, "refs/remotes/origin/") {
			return strings.TrimPrefix(target, "refs/remotes/origin/")
		}
	}

	// Fallback: check if origin/main or origin/master exists
	if _, err := repo.GetReference(plumbing.NewRemoteReferenceName("origin", "main"), true); err == nil {
		return "main"
	}
	if _, err := repo.GetReference(plumbing.NewRemoteReferenceName("origin", "master"), true); err == nil {
		return "master"
	}

	return ""
}

// ShouldSkipOnDefaultBranch checks if we're on the default branch.
// Returns (shouldSkip, branchName). If shouldSkip is true, the caller should
// skip the operation to avoid polluting main/master history.
// If the branch cannot be determined, returns (false, "") to allow the operation.
func ShouldSkipOnDefaultBranch(ctx context.Context) (bool, string) {
	isDefault, branchName, err := IsOnDefaultBranch(ctx)
	if err != nil {
		// If we can't determine, allow the operation
		return false, ""
	}
	return isDefault, branchName
}

// GetCurrentBranch returns the name of the current branch.
// Returns an error if in detached HEAD state or if not in a git repository.
func GetCurrentBranch(ctx context.Context) (string, error) {
	repo, err := openProvider(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to open git repository: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	if !head.Name().IsBranch() {
		return "", errors.New("not on a branch (detached HEAD)")
	}

	return head.Name().Short(), nil
}

// GetMergeBase finds the common ancestor (merge-base) between two branches.
// Returns the hash of the merge-base commit.
func GetMergeBase(ctx context.Context, branch1, branch2 string) (*plumbing.Hash, error) {
	repo, err := openProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}

	// Resolve branch references
	ref1, err := repo.GetReference(plumbing.NewBranchReferenceName(branch1), true)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve branch %s: %w", branch1, err)
	}

	ref2, err := repo.GetReference(plumbing.NewBranchReferenceName(branch2), true)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve branch %s: %w", branch2, err)
	}

	// Get commit objects
	commit1, err := repo.CommitObject(ref1.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit for %s: %w", branch1, err)
	}

	commit2, err := repo.CommitObject(ref2.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit for %s: %w", branch2, err)
	}

	// Find common ancestor
	mergeBase, err := commit1.MergeBase(commit2)
	if err != nil {
		return nil, fmt.Errorf("failed to find merge base: %w", err)
	}

	if len(mergeBase) == 0 {
		return nil, errors.New("no common ancestor found")
	}

	hash := mergeBase[0].Hash
	return &hash, nil
}

// HasUncommittedChanges checks if there are any uncommitted changes in the repository.
// This includes staged changes, unstaged changes, and untracked files.
// Delegates to gitprovider.Repository which uses the git CLI to respect global gitignore.
func HasUncommittedChanges(ctx context.Context) (bool, error) {
	repo, err := openProvider(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to open git repository: %w", err)
	}
	hasChanges, err := repo.HasUncommittedChanges(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check uncommitted changes: %w", err)
	}
	return hasChanges, nil
}

// findNewUntrackedFiles finds files that are newly untracked (not in pre-existing list)
func findNewUntrackedFiles(current, preExisting []string) []string {
	preExistingSet := make(map[string]bool)
	for _, file := range preExisting {
		preExistingSet[file] = true
	}

	var newFiles []string
	for _, file := range current {
		if !preExistingSet[file] {
			newFiles = append(newFiles, file)
		}
	}
	return newFiles
}

// BranchExistsOnRemote checks if a branch exists on the origin remote.
// Returns true if the branch is tracked on origin, false otherwise.
func BranchExistsOnRemote(ctx context.Context, branchName string) (bool, error) {
	repo, err := openProvider(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to open git repository: %w", err)
	}

	// Check for remote reference: refs/remotes/origin/<branchName>
	_, err = repo.GetReference(plumbing.NewRemoteReferenceName("origin", branchName), true)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check remote branch: %w", err)
	}

	return true, nil
}

// BranchExistsLocally checks if a local branch exists.
func BranchExistsLocally(ctx context.Context, branchName string) (bool, error) {
	repo, err := openProvider(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to open git repository: %w", err)
	}

	_, err = repo.GetReference(plumbing.NewBranchReferenceName(branchName), true)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check branch: %w", err)
	}

	return true, nil
}

// CheckoutBranch switches to the specified local branch or commit.
// Delegates to gitprovider.Repository.Checkout which uses the git CLI.
// Returns an error if the ref doesn't exist or checkout fails.
func CheckoutBranch(ctx context.Context, ref string) error {
	repo, err := openProvider(ctx)
	if err != nil {
		return fmt.Errorf("failed to open git repository: %w", err)
	}
	if err := repo.Checkout(ctx, ref); err != nil {
		return fmt.Errorf("checkout failed: %w", err)
	}
	return nil
}

// ValidateBranchName checks if a branch name is valid using git check-ref-format.
// Returns an error if the name is invalid or contains unsafe characters.
func ValidateBranchName(ctx context.Context, branchName string) error {
	repo, err := openProvider(ctx)
	if err != nil {
		return fmt.Errorf("failed to open git repository: %w", err)
	}
	if err := repo.ValidateBranchName(ctx, branchName); err != nil {
		return fmt.Errorf("invalid branch name: %w", err)
	}
	return nil
}

// FetchAndCheckoutRemoteBranch fetches a branch from origin and creates a local tracking branch.
// Delegates to gitprovider.Repository for fetch, reference, and checkout operations.
func FetchAndCheckoutRemoteBranch(ctx context.Context, branchName string) error {
	// Validate branch name before using (branchName comes from user CLI input)
	if err := ValidateBranchName(ctx, branchName); err != nil {
		return err
	}

	repo, err := openProvider(ctx)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	refSpec := fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branchName, branchName)
	if err := repo.Fetch(ctx, "origin", refSpec, 2*time.Minute); err != nil {
		return fmt.Errorf("failed to fetch branch from origin: %w", err)
	}

	// Get the remote branch reference
	remoteRef, err := repo.GetReference(plumbing.NewRemoteReferenceName("origin", branchName), true)
	if err != nil {
		return fmt.Errorf("branch '%s' not found on origin: %w", branchName, err)
	}

	// Create local branch pointing to the same commit
	localRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName(branchName), remoteRef.Hash())
	if err := repo.SetReference(localRef); err != nil {
		return fmt.Errorf("failed to create local branch: %w", err)
	}

	// Checkout the new local branch
	if err := repo.Checkout(ctx, branchName); err != nil {
		return fmt.Errorf("failed to checkout branch: %w", err)
	}
	return nil
}

// FetchMetadataBranch fetches the entire/checkpoints/v1 branch from origin and creates/updates the local branch.
// This is used when the metadata branch exists on remote but not locally.
// Delegates to gitprovider.Repository for fetch, reference, and set-reference operations.
func FetchMetadataBranch(ctx context.Context) error {
	branchName := paths.MetadataBranchName

	repo, err := openProvider(ctx)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	refSpec := fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branchName, branchName)
	if err := repo.Fetch(ctx, "origin", refSpec, 2*time.Minute); err != nil {
		return fmt.Errorf("failed to fetch %s from origin: %w", branchName, err)
	}

	// Get the remote branch reference
	remoteRef, err := repo.GetReference(plumbing.NewRemoteReferenceName("origin", branchName), true)
	if err != nil {
		return fmt.Errorf("branch '%s' not found on origin: %w", branchName, err)
	}

	// Create or update local branch pointing to the same commit
	localRef := plumbing.NewHashReference(plumbing.NewBranchReferenceName(branchName), remoteRef.Hash())
	if err := repo.SetReference(localRef); err != nil {
		return fmt.Errorf("failed to create local %s branch: %w", branchName, err)
	}

	return nil
}
