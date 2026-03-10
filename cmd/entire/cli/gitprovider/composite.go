//nolint:wrapcheck // Composite is a pure delegation layer; wrapping would add noise without value.
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

// Composite implements Repository by delegating each sub-interface to a
// potentially different provider. This allows mixing go-git for object
// operations with the CLI for working-tree and remote operations.
type Composite struct {
	refs     ReferenceProvider
	objects  ObjectProvider
	worktree WorktreeProvider
	remote   RemoteProvider
	config   ConfigProvider
	diff     DiffProvider
}

// CompositeOption configures a Composite repository.
type CompositeOption func(*Composite)

// WithReferenceProvider sets the reference provider.
func WithReferenceProvider(p ReferenceProvider) CompositeOption {
	return func(c *Composite) { c.refs = p }
}

// WithObjectProvider sets the object provider.
func WithObjectProvider(p ObjectProvider) CompositeOption {
	return func(c *Composite) { c.objects = p }
}

// WithWorktreeProvider sets the worktree provider.
func WithWorktreeProvider(p WorktreeProvider) CompositeOption {
	return func(c *Composite) { c.worktree = p }
}

// WithRemoteProvider sets the remote provider.
func WithRemoteProvider(p RemoteProvider) CompositeOption {
	return func(c *Composite) { c.remote = p }
}

// WithConfigProvider sets the config provider.
func WithConfigProvider(p ConfigProvider) CompositeOption {
	return func(c *Composite) { c.config = p }
}

// WithDiffProvider sets the diff provider.
func WithDiffProvider(p DiffProvider) CompositeOption {
	return func(c *Composite) { c.diff = p }
}

// NewComposite creates a composite repository with the given options.
// Any sub-interface left nil will panic on use.
func NewComposite(opts ...CompositeOption) *Composite {
	c := &Composite{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// --- ReferenceProvider ---

func (c *Composite) Head() (*plumbing.Reference, error) {
	return c.refs.Head()
}

func (c *Composite) GetReference(name plumbing.ReferenceName, resolve bool) (*plumbing.Reference, error) {
	return c.refs.GetReference(name, resolve)
}

func (c *Composite) SetReference(ref *plumbing.Reference) error {
	return c.refs.SetReference(ref)
}

func (c *Composite) ListBranches() (storer.ReferenceIter, error) {
	return c.refs.ListBranches()
}

func (c *Composite) ListReferences() (storer.ReferenceIter, error) {
	return c.refs.ListReferences()
}

func (c *Composite) BranchExists(name string) (bool, error) {
	return c.refs.BranchExists(name)
}

func (c *Composite) DeleteBranch(name string) error {
	return c.refs.DeleteBranch(name)
}

// --- ObjectProvider ---

func (c *Composite) CommitObject(hash plumbing.Hash) (*object.Commit, error) {
	return c.objects.CommitObject(hash)
}

func (c *Composite) TreeObject(hash plumbing.Hash) (*object.Tree, error) {
	return c.objects.TreeObject(hash)
}

func (c *Composite) BlobObject(hash plumbing.Hash) (*object.Blob, error) {
	return c.objects.BlobObject(hash)
}

func (c *Composite) Log(opts *git.LogOptions) (object.CommitIter, error) {
	return c.objects.Log(opts)
}

func (c *Composite) IsEmpty() (bool, error) {
	return c.objects.IsEmpty()
}

func (c *Composite) Storer() storage.Storer {
	return c.objects.Storer()
}

// --- WorktreeProvider ---

func (c *Composite) Checkout(ctx context.Context, ref string) error {
	return c.worktree.Checkout(ctx, ref)
}

func (c *Composite) HardReset(ctx context.Context, commit plumbing.Hash) error {
	return c.worktree.HardReset(ctx, commit)
}

func (c *Composite) HasUncommittedChanges(ctx context.Context) (bool, error) {
	return c.worktree.HasUncommittedChanges(ctx)
}

func (c *Composite) DetectFileChanges(ctx context.Context) ([]byte, error) {
	return c.worktree.DetectFileChanges(ctx)
}

func (c *Composite) StagedFiles(ctx context.Context) ([]string, error) {
	return c.worktree.StagedFiles(ctx)
}

func (c *Composite) UntrackedFiles(ctx context.Context) ([]string, error) {
	return c.worktree.UntrackedFiles(ctx)
}

// --- RemoteProvider ---

func (c *Composite) Fetch(ctx context.Context, remote, refSpec string, timeout time.Duration) error {
	return c.remote.Fetch(ctx, remote, refSpec, timeout)
}

func (c *Composite) Push(ctx context.Context, remote, refSpec string, noVerify bool, timeout time.Duration) error {
	return c.remote.Push(ctx, remote, refSpec, noVerify, timeout)
}

// --- ConfigProvider ---

func (c *Composite) ConfigValue(ctx context.Context, key string) (string, error) {
	return c.config.ConfigValue(ctx, key)
}

func (c *Composite) GitDir(ctx context.Context) (string, error) {
	return c.config.GitDir(ctx)
}

func (c *Composite) GitCommonDir(ctx context.Context) (string, error) {
	return c.config.GitCommonDir(ctx)
}

func (c *Composite) HooksDir(ctx context.Context) (string, error) {
	return c.config.HooksDir(ctx)
}

func (c *Composite) WorktreeRoot(ctx context.Context) (string, error) {
	return c.config.WorktreeRoot(ctx)
}

func (c *Composite) ValidateBranchName(ctx context.Context, name string) error {
	return c.config.ValidateBranchName(ctx, name)
}

func (c *Composite) RevParseSymbolicFullName(ctx context.Context, ref string) (string, error) {
	return c.config.RevParseSymbolicFullName(ctx, ref)
}

// --- DiffProvider ---

func (c *Composite) DiffTree(ctx context.Context, repoDir, commit1, commit2 string) ([]string, error) {
	return c.diff.DiffTree(ctx, repoDir, commit1, commit2)
}

func (c *Composite) IsDisconnected(ctx context.Context, hash1, hash2 string) (bool, error) {
	return c.diff.IsDisconnected(ctx, hash1, hash2)
}
