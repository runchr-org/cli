package gitbackend

import (
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// Hash is a git object hash. Aliased from go-git's plumbing.Hash
// since it's just a [20]byte with no storage dependency.
type Hash = plumbing.Hash

// ReferenceName is a fully qualified git reference name (e.g., "refs/heads/main").
type ReferenceName = plumbing.ReferenceName

// TreeEntry is a single entry in a git tree object.
// Aliased from go-git since it's a plain data struct with no storage dependency.
type TreeEntry = object.TreeEntry

// FileMode aliases from go-git for tree entry modes.
var (
	FileModeDir       = filemode.Dir
	FileModeRegular   = filemode.Regular
	FileModeSubmodule = filemode.Submodule
)

// ZeroHash is the zero-value hash (all zeros).
var ZeroHash = plumbing.ZeroHash

// ErrReferenceNotFound is returned when a reference cannot be found.
var ErrReferenceNotFound = plumbing.ErrReferenceNotFound

// NewHash parses a hex string into a Hash.
var NewHash = plumbing.NewHash

// NewBranchReferenceName creates a reference name for a local branch.
var NewBranchReferenceName = plumbing.NewBranchReferenceName

// NewRemoteReferenceName creates a reference name for a remote tracking branch.
var NewRemoteReferenceName = plumbing.NewRemoteReferenceName

// NewHashReference creates a direct reference pointing to a hash.
var NewHashReference = plumbing.NewHashReference

// Ref represents a git reference (branch, tag, etc.).
type Ref struct {
	// Name is the fully qualified reference name (e.g., "refs/heads/main").
	Name ReferenceName
	// Hash is the object hash this reference points to.
	Hash Hash
	// Target is the target reference name for symbolic refs (e.g., HEAD → refs/heads/main).
	// Empty for direct (non-symbolic) references.
	Target ReferenceName
}

// IsBranch returns true if this reference is a local branch.
func (r *Ref) IsBranch() bool {
	return r.Name.IsBranch()
}

// Short returns the short name of the reference (e.g., "main" instead of "refs/heads/main").
func (r *Ref) Short() string {
	return r.Name.Short()
}

// Signature represents a git author or committer.
type Signature struct {
	Name  string
	Email string
	When  time.Time
}

// Commit represents a git commit object.
type Commit struct {
	Hash      Hash
	TreeHash  Hash
	Parents   []Hash
	Author    Signature
	Committer Signature
	Message   string
}

// Tree represents a git tree object.
type Tree struct {
	Hash    Hash
	Entries []TreeEntry
}

// FileStatusCode represents the status of a file in a particular context (staging or worktree).
type FileStatusCode byte

const (
	StatusUnmodified FileStatusCode = ' '
	StatusModified   FileStatusCode = 'M'
	StatusAdded      FileStatusCode = 'A'
	StatusDeleted    FileStatusCode = 'D'
	StatusRenamed    FileStatusCode = 'R'
	StatusCopied     FileStatusCode = 'C'
	StatusUntracked  FileStatusCode = '?'
	StatusIgnored    FileStatusCode = '!'
)

// FileStatus represents the status of a single file.
type FileStatus struct {
	Staging  FileStatusCode
	Worktree FileStatusCode
}

// RemoteConfig holds basic remote configuration.
type RemoteConfig struct {
	Name string
	URLs []string
}
