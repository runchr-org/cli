// Package gitprovider defines interfaces for git operations and provides
// implementations backed by go-git and the git CLI. A factory selects the
// best implementation per operation category based on settings.
package gitprovider

import (
	"context"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/storer"
	"github.com/go-git/go-git/v6/storage"
)

// ReferenceProvider handles git reference (branch/tag) operations.
type ReferenceProvider interface {
	// Head returns the HEAD reference.
	Head() (*plumbing.Reference, error)

	// GetReference resolves a reference by name.
	// When resolve is true, symbolic references are dereferenced.
	GetReference(name plumbing.ReferenceName, resolve bool) (*plumbing.Reference, error)

	// SetReference creates or updates a reference.
	SetReference(ref *plumbing.Reference) error

	// ListBranches returns an iterator over all local branches.
	ListBranches() (storer.ReferenceIter, error)

	// ListReferences returns an iterator over all references.
	ListReferences() (storer.ReferenceIter, error)

	// BranchExists checks whether a local branch exists.
	BranchExists(name string) (bool, error)

	// DeleteBranch force-deletes a local branch.
	DeleteBranch(name string) error
}

// ObjectProvider handles git object (commit, tree, blob) operations.
type ObjectProvider interface {
	// CommitObject returns the commit identified by hash.
	CommitObject(hash plumbing.Hash) (*object.Commit, error)

	// TreeObject returns the tree identified by hash.
	TreeObject(hash plumbing.Hash) (*object.Tree, error)

	// BlobObject returns the blob identified by hash.
	BlobObject(hash plumbing.Hash) (*object.Blob, error)

	// Log returns a commit iterator starting from the given options.
	Log(opts *git.LogOptions) (object.CommitIter, error)

	// IsEmpty returns true when the repository has no commits.
	IsEmpty() (bool, error)

	// Storer returns the underlying storage backend for low-level plumbing.
	// This is needed by the checkpoint package for in-memory tree building
	// and reference manipulation.
	Storer() storage.Storer
}

// WorktreeProvider handles working-tree operations.
type WorktreeProvider interface {
	// Checkout switches the working tree to the given ref (branch name or commit hash).
	Checkout(ctx context.Context, ref string) error

	// HardReset resets the working tree and index to the given commit.
	HardReset(ctx context.Context, commit plumbing.Hash) error

	// HasUncommittedChanges returns true when there are staged, unstaged,
	// or untracked changes in the working tree.
	HasUncommittedChanges(ctx context.Context) (bool, error)

	// DetectFileChanges returns a detailed list of all file changes
	// (staged, unstaged, untracked) using null-separated porcelain output.
	DetectFileChanges(ctx context.Context) ([]byte, error)

	// StagedFiles returns the paths of files staged for the next commit.
	StagedFiles(ctx context.Context) ([]string, error)

	// UntrackedFiles returns paths of untracked files that are not gitignored.
	UntrackedFiles(ctx context.Context) ([]string, error)
}

// RemoteProvider handles fetch and push operations.
type RemoteProvider interface {
	// Fetch fetches a refspec from the named remote.
	// A zero timeout means no deadline.
	Fetch(ctx context.Context, remote, refSpec string, timeout time.Duration) error

	// Push pushes a refspec to the named remote.
	// When noVerify is true, pre-push hooks are skipped.
	// A zero timeout means no deadline.
	Push(ctx context.Context, remote, refSpec string, noVerify bool, timeout time.Duration) error
}

// ConfigProvider handles configuration and path-resolution operations.
type ConfigProvider interface {
	// ConfigValue returns the value for a git config key, or "" if unset.
	ConfigValue(ctx context.Context, key string) (string, error)

	// GitDir returns the .git directory path (worktree-aware).
	GitDir(ctx context.Context) (string, error)

	// GitCommonDir returns the shared .git directory (same as GitDir for
	// non-worktree repos, points to the main repo's .git for linked worktrees).
	GitCommonDir(ctx context.Context) (string, error)

	// HooksDir returns the active hooks directory, respecting core.hooksPath.
	HooksDir(ctx context.Context) (string, error)

	// WorktreeRoot returns the repository working-tree root directory.
	WorktreeRoot(ctx context.Context) (string, error)

	// ValidateBranchName checks whether a branch name is valid.
	ValidateBranchName(ctx context.Context, name string) error

	// RevParseSymbolicFullName resolves a ref to its full symbolic name
	// (e.g. "HEAD" → "refs/heads/main").
	RevParseSymbolicFullName(ctx context.Context, ref string) (string, error)
}

// DiffProvider handles diff and ancestry operations.
type DiffProvider interface {
	// DiffTree returns the list of file paths changed between two commits.
	// For initial commits (commit1 is empty), all files in commit2 are returned.
	// repoDir is the working-tree directory to run in.
	DiffTree(ctx context.Context, repoDir, commit1, commit2 string) ([]string, error)

	// IsDisconnected returns true when two commits share no common ancestor.
	IsDisconnected(ctx context.Context, hash1, hash2 string) (bool, error)
}

// Repository combines all provider sub-interfaces into a single facade.
type Repository interface {
	ReferenceProvider
	ObjectProvider
	WorktreeProvider
	RemoteProvider
	ConfigProvider
	DiffProvider
}
