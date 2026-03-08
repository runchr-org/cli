//nolint:wrapcheck // Composite delegation — errors are already wrapped by the underlying implementations.
package gitbackend

import (
	"context"
	"io"
	"time"

	"github.com/go-git/go-git/v6"
)

func (c *compositeRepo) GoGitRepository() *git.Repository { return c.goGitRepo }

// --- RefOps ---

func (c *compositeRepo) Head() (*Ref, error) { return c.refs.Head() }
func (c *compositeRepo) Reference(name ReferenceName, resolved bool) (*Ref, error) {
	return c.refs.Reference(name, resolved)
}
func (c *compositeRepo) SetReference(name ReferenceName, hash Hash) error {
	return c.refs.SetReference(name, hash)
}
func (c *compositeRepo) DeleteReference(name ReferenceName) error {
	return c.refs.DeleteReference(name)
}
func (c *compositeRepo) ValidateBranchName(ctx context.Context, name string) error {
	return c.refs.ValidateBranchName(ctx, name)
}
func (c *compositeRepo) IsEmpty() bool { return c.refs.IsEmpty() }

// --- CommitOps ---

func (c *compositeRepo) CommitObject(hash Hash) (*Commit, error) {
	return c.commits.CommitObject(hash)
}
func (c *compositeRepo) LogEach(ctx context.Context, from Hash, fn func(*Commit) error) error {
	return c.commits.LogEach(ctx, from, fn)
}
func (c *compositeRepo) CreateCommit(treeHash, parentHash Hash, message, authorName, authorEmail string) (Hash, error) {
	return c.commits.CreateCommit(treeHash, parentHash, message, authorName, authorEmail)
}
func (c *compositeRepo) MergeBase(a, b Hash) ([]Hash, error) {
	return c.commits.MergeBase(a, b)
}

// --- ObjectOps ---

func (c *compositeRepo) TreeObject(hash Hash) (*Tree, error) {
	return c.objects.TreeObject(hash)
}
func (c *compositeRepo) TreeFile(treeHash Hash, path string) (string, error) {
	return c.objects.TreeFile(treeHash, path)
}
func (c *compositeRepo) TreeSubtree(treeHash Hash, path string) (*Tree, error) {
	return c.objects.TreeSubtree(treeHash, path)
}
func (c *compositeRepo) TreeFindEntry(treeHash Hash, path string) (*TreeEntry, error) {
	return c.objects.TreeFindEntry(treeHash, path)
}
func (c *compositeRepo) BlobContents(hash Hash) ([]byte, error) {
	return c.objects.BlobContents(hash)
}
func (c *compositeRepo) BlobReader(hash Hash) (io.ReadCloser, error) {
	return c.objects.BlobReader(hash)
}
func (c *compositeRepo) BlobSize(hash Hash) (int64, error) {
	return c.objects.BlobSize(hash)
}
func (c *compositeRepo) IsBinaryBlob(hash Hash) (bool, error) {
	return c.objects.IsBinaryBlob(hash)
}
func (c *compositeRepo) CreateBlob(content []byte) (Hash, error) {
	return c.objects.CreateBlob(content)
}
func (c *compositeRepo) CreateTree(entries []TreeEntry) (Hash, error) {
	return c.objects.CreateTree(entries)
}

// --- WorktreeOps ---

func (c *compositeRepo) WorktreeRoot() string { return c.worktree.WorktreeRoot() }
func (c *compositeRepo) WorktreeStatus(ctx context.Context) (map[string]FileStatus, error) {
	return c.worktree.WorktreeStatus(ctx)
}
func (c *compositeRepo) HasUncommittedChanges(ctx context.Context) (bool, error) {
	return c.worktree.HasUncommittedChanges(ctx)
}
func (c *compositeRepo) CollectChangedFiles(ctx context.Context) (changed, deleted []string, err error) {
	return c.worktree.CollectChangedFiles(ctx)
}
func (c *compositeRepo) Checkout(ctx context.Context, ref string) error {
	return c.worktree.Checkout(ctx, ref)
}
func (c *compositeRepo) HardReset(ctx context.Context, hash string) error {
	return c.worktree.HardReset(ctx, hash)
}

// --- RemoteOps ---

func (c *compositeRepo) Fetch(ctx context.Context, remote, refSpec string, timeout time.Duration) error {
	return c.remote.Fetch(ctx, remote, refSpec, timeout)
}
func (c *compositeRepo) Push(ctx context.Context, remote, branch string, noVerify bool) error {
	return c.remote.Push(ctx, remote, branch, noVerify)
}

// --- DiffOps ---

func (c *compositeRepo) DiffTreeFiles(ctx context.Context, dir, commit1, commit2 string) ([]string, error) {
	return c.diff.DiffTreeFiles(ctx, dir, commit1, commit2)
}

// --- ConfigOps ---

func (c *compositeRepo) Author() (name, email string) { return c.config.Author() }
func (c *compositeRepo) ConfigValue(ctx context.Context, key string) string {
	return c.config.ConfigValue(ctx, key)
}
func (c *compositeRepo) RemoteURL(remote string) string { return c.config.RemoteURL(remote) }
func (c *compositeRepo) GitDir(ctx context.Context) (string, error) {
	return c.config.GitDir(ctx)
}
func (c *compositeRepo) HooksDir(ctx context.Context) (string, error) {
	return c.config.HooksDir(ctx)
}
