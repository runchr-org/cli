// Package gitbackend provides an abstraction layer over git operations.
//
// The CLI uses two git backends: the go-git library and the git CLI.
// This package defines interfaces for all git operations and allows
// selecting which implementation to use on a per-operation-category basis.
//
// Each operation category (refs, commits, objects, worktree, remote, diff, config)
// can be independently configured via .entire/settings.json:
//
//	{
//	  "git_backend": {
//	    "default": "auto",
//	    "overrides": {
//	      "worktree": "cli",
//	      "remote": "cli"
//	    }
//	  }
//	}
//
// The "auto" provider selects sensible defaults per category based on
// known limitations of each backend (see DefaultProvider).
package gitbackend

import (
	"context"
	"io"
	"time"

	"github.com/go-git/go-git/v6"
)

// RefOps provides reference and branch operations.
type RefOps interface {
	// Head returns the HEAD reference.
	Head() (*Ref, error)

	// Reference resolves a reference by name. If resolved is true,
	// symbolic references are followed to their target.
	Reference(name ReferenceName, resolved bool) (*Ref, error)

	// SetReference creates or updates a reference to point to the given hash.
	SetReference(name ReferenceName, hash Hash) error

	// DeleteReference removes a reference.
	DeleteReference(name ReferenceName) error

	// ValidateBranchName checks if a branch name is valid.
	ValidateBranchName(ctx context.Context, name string) error

	// IsEmpty returns true if the repository has no commits.
	IsEmpty() bool
}

// CommitOps provides commit-related operations.
type CommitOps interface {
	// CommitObject returns a commit by its hash.
	CommitObject(hash Hash) (*Commit, error)

	// LogEach iterates over commits starting from the given hash,
	// calling fn for each commit. Iteration stops when fn returns
	// a non-nil error or all commits have been visited.
	// Use ErrStop to break out of iteration without error.
	LogEach(ctx context.Context, from Hash, fn func(*Commit) error) error

	// CreateCommit creates a new commit object. If parentHash is ZeroHash,
	// an orphan commit (no parents) is created.
	CreateCommit(treeHash, parentHash Hash, message, authorName, authorEmail string) (Hash, error)

	// MergeBase finds the best common ancestor(s) between two commits.
	MergeBase(a, b Hash) ([]Hash, error)
}

// ObjectOps provides tree and blob object store operations.
type ObjectOps interface {
	// TreeObject returns a tree by its hash.
	TreeObject(hash Hash) (*Tree, error)

	// TreeFile reads a file's content from a tree by path.
	// The path is relative to the tree root (e.g., "src/main.go").
	TreeFile(treeHash Hash, path string) (string, error)

	// TreeSubtree returns a subtree at the given path within a tree.
	TreeSubtree(treeHash Hash, path string) (*Tree, error)

	// TreeFindEntry finds a single entry at the given path within a tree.
	// The path can include slashes for nested lookups.
	TreeFindEntry(treeHash Hash, path string) (*TreeEntry, error)

	// BlobContents returns the full content of a blob as bytes.
	BlobContents(hash Hash) ([]byte, error)

	// BlobReader returns a reader for a blob's content.
	// The caller must close the reader when done.
	BlobReader(hash Hash) (io.ReadCloser, error)

	// BlobSize returns the size of a blob in bytes.
	BlobSize(hash Hash) (int64, error)

	// IsBinaryBlob returns true if the blob content appears to be binary.
	IsBinaryBlob(hash Hash) (bool, error)

	// CreateBlob stores content as a new blob object and returns its hash.
	CreateBlob(content []byte) (Hash, error)

	// CreateTree stores a tree with the given entries and returns its hash.
	CreateTree(entries []TreeEntry) (Hash, error)
}

// WorktreeOps provides working directory operations.
type WorktreeOps interface {
	// WorktreeRoot returns the absolute path to the worktree root directory.
	WorktreeRoot() string

	// WorktreeStatus returns the status of all files in the working directory.
	// The map key is the file path relative to the worktree root.
	WorktreeStatus(ctx context.Context) (map[string]FileStatus, error)

	// HasUncommittedChanges returns true if there are any uncommitted changes
	// (staged, unstaged, or untracked files).
	HasUncommittedChanges(ctx context.Context) (bool, error)

	// CollectChangedFiles returns lists of changed and deleted files.
	// Changed includes modified, added, and untracked files.
	// Paths are relative to the worktree root.
	CollectChangedFiles(ctx context.Context) (changed, deleted []string, err error)

	// Checkout switches the working directory to the specified ref (branch name or commit hash).
	Checkout(ctx context.Context, ref string) error

	// HardReset resets the working directory and index to the specified commit,
	// discarding all changes. Uses git CLI to avoid go-git's bug with
	// deleting .gitignore'd directories.
	HardReset(ctx context.Context, hash string) error
}

// RemoteOps provides network operations (fetch, push).
type RemoteOps interface {
	// Fetch fetches refs from a remote. The refSpec format is the standard
	// git refspec (e.g., "+refs/heads/main:refs/remotes/origin/main").
	// timeout of 0 means no timeout.
	Fetch(ctx context.Context, remote, refSpec string, timeout time.Duration) error

	// Push pushes a branch to a remote. If noVerify is true, pre-push hooks are skipped.
	Push(ctx context.Context, remote, branch string, noVerify bool) error
}

// DiffOps provides diff operations between commits.
type DiffOps interface {
	// DiffTreeFiles returns the list of files changed between two commits.
	// For initial commits (commit1 is empty), all files in commit2 are returned.
	// dir is the repository working directory for running the command.
	DiffTreeFiles(ctx context.Context, dir, commit1, commit2 string) ([]string, error)
}

// ConfigOps provides git configuration and utility operations.
type ConfigOps interface {
	// Author returns the configured git author name and email.
	// Checks repository config first, then falls back to global config.
	Author() (name, email string)

	// ConfigValue retrieves a git config value by key (e.g., "user.name").
	// Returns empty string if the key is not set.
	ConfigValue(ctx context.Context, key string) string

	// RemoteURL returns the first URL configured for the given remote.
	// Returns empty string if the remote doesn't exist.
	RemoteURL(remote string) string

	// GitDir returns the path to the .git directory.
	// For worktrees, this returns the worktree-specific git dir.
	GitDir(ctx context.Context) (string, error)

	// HooksDir returns the path to the git hooks directory.
	// Respects core.hooksPath and linked worktree resolution.
	HooksDir(ctx context.Context) (string, error)
}

// Repository composes all git operation interfaces into a single type.
// It can be backed by go-git, git CLI, or a per-category composite.
type Repository interface {
	RefOps
	CommitOps
	ObjectOps
	WorktreeOps
	RemoteOps
	DiffOps
	ConfigOps

	// GoGitRepository returns the underlying go-git repository.
	// This exists for gradual migration — code that needs direct go-git access
	// (e.g., checkpoint tree surgery) can use this until fully migrated.
	GoGitRepository() *git.Repository
}

// ErrStop is a sentinel error for breaking out of iteration (e.g., LogEach)
// without signaling a real error.
var ErrStop = errStopError{}

type errStopError struct{}

func (errStopError) Error() string { return "stop iteration" }
