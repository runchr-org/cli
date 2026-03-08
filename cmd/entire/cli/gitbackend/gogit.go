package gitbackend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/utils/binary"
)

// goGitRepo implements Repository using the go-git library.
type goGitRepo struct {
	repo        *git.Repository
	worktreeDir string
}

// Compile-time interface checks.
var (
	_ RefOps      = (*goGitRepo)(nil)
	_ CommitOps   = (*goGitRepo)(nil)
	_ ObjectOps   = (*goGitRepo)(nil)
	_ WorktreeOps = (*goGitRepo)(nil)
	_ RemoteOps   = (*goGitRepo)(nil)
	_ DiffOps     = (*goGitRepo)(nil)
	_ ConfigOps   = (*goGitRepo)(nil)
	_ Repository  = (*goGitRepo)(nil)
)

// NewGoGitRepository creates a Repository backed by go-git.
// worktreeDir is the absolute path to the worktree root.
func NewGoGitRepository(repo *git.Repository, worktreeDir string) *goGitRepo { //nolint:revive // unexported return is intentional; callers use Repository interface
	return &goGitRepo{repo: repo, worktreeDir: worktreeDir}
}

func (g *goGitRepo) GoGitRepository() *git.Repository { return g.repo }

// --- RefOps ---

func (g *goGitRepo) Head() (*Ref, error) {
	ref, err := g.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}
	return convertRef(ref), nil
}

func (g *goGitRepo) Reference(name ReferenceName, resolved bool) (*Ref, error) {
	ref, err := g.repo.Reference(name, resolved)
	if err != nil {
		return nil, err //nolint:wrapcheck // preserve ErrReferenceNotFound identity
	}
	return convertRef(ref), nil
}

func (g *goGitRepo) SetReference(name ReferenceName, hash Hash) error {
	ref := plumbing.NewHashReference(name, hash)
	if err := g.repo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("failed to set reference %s: %w", name, err)
	}
	return nil
}

func (g *goGitRepo) DeleteReference(name ReferenceName) error {
	if err := g.repo.Storer.RemoveReference(name); err != nil {
		return fmt.Errorf("failed to delete reference %s: %w", name, err)
	}
	return nil
}

func (g *goGitRepo) ValidateBranchName(ctx context.Context, name string) error {
	// go-git doesn't have branch name validation, so we use the CLI.
	cmd := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

func (g *goGitRepo) IsEmpty() bool {
	_, err := g.repo.Head()
	return errors.Is(err, plumbing.ErrReferenceNotFound)
}

// --- CommitOps ---

func (g *goGitRepo) CommitObject(hash Hash) (*Commit, error) {
	c, err := g.repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit %s: %w", hash, err)
	}
	return convertCommit(c), nil
}

func (g *goGitRepo) LogEach(ctx context.Context, from Hash, fn func(*Commit) error) error {
	iter, err := g.repo.Log(&git.LogOptions{From: from})
	if err != nil {
		return fmt.Errorf("failed to start log from %s: %w", from, err)
	}
	defer iter.Close()

	err = iter.ForEach(func(c *object.Commit) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr //nolint:wrapcheck // propagating context cancellation
		}
		return fn(convertCommit(c))
	})
	if err != nil && !errors.Is(err, ErrStop) {
		return fmt.Errorf("log iteration error: %w", err)
	}
	return nil
}

func (g *goGitRepo) CreateCommit(treeHash, parentHash Hash, message, authorName, authorEmail string) (Hash, error) {
	now := time.Now()
	sig := object.Signature{
		Name:  authorName,
		Email: authorEmail,
		When:  now,
	}

	commit := &object.Commit{
		TreeHash:  treeHash,
		Author:    sig,
		Committer: sig,
		Message:   message,
	}
	if parentHash != ZeroHash {
		commit.ParentHashes = []Hash{parentHash}
	}

	obj := g.repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return ZeroHash, fmt.Errorf("failed to encode commit: %w", err)
	}
	hash, err := g.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return ZeroHash, fmt.Errorf("failed to store commit: %w", err)
	}
	return hash, nil
}

func (g *goGitRepo) MergeBase(a, b Hash) ([]Hash, error) {
	commitA, err := g.repo.CommitObject(a)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit %s: %w", a, err)
	}
	commitB, err := g.repo.CommitObject(b)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit %s: %w", b, err)
	}

	bases, err := commitA.MergeBase(commitB)
	if err != nil {
		return nil, fmt.Errorf("failed to find merge base: %w", err)
	}

	hashes := make([]Hash, len(bases))
	for i, c := range bases {
		hashes[i] = c.Hash
	}
	return hashes, nil
}

// --- ObjectOps ---

func (g *goGitRepo) TreeObject(hash Hash) (*Tree, error) {
	t, err := g.repo.TreeObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree %s: %w", hash, err)
	}
	return convertTree(t), nil
}

func (g *goGitRepo) TreeFile(treeHash Hash, path string) (string, error) {
	t, err := g.repo.TreeObject(treeHash)
	if err != nil {
		return "", fmt.Errorf("failed to get tree %s: %w", treeHash, err)
	}
	f, err := t.File(path)
	if err != nil {
		return "", fmt.Errorf("file %s not found in tree: %w", path, err)
	}
	content, err := f.Contents()
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return content, nil
}

func (g *goGitRepo) TreeSubtree(treeHash Hash, path string) (*Tree, error) {
	t, err := g.repo.TreeObject(treeHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree %s: %w", treeHash, err)
	}
	sub, err := t.Tree(path)
	if err != nil {
		return nil, fmt.Errorf("subtree %s not found: %w", path, err)
	}
	return convertTree(sub), nil
}

func (g *goGitRepo) TreeFindEntry(treeHash Hash, path string) (*TreeEntry, error) {
	t, err := g.repo.TreeObject(treeHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree %s: %w", treeHash, err)
	}
	entry, err := t.FindEntry(path)
	if err != nil {
		return nil, fmt.Errorf("entry %s not found: %w", path, err)
	}
	return entry, nil
}

func (g *goGitRepo) BlobContents(hash Hash) ([]byte, error) {
	blob, err := g.repo.BlobObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob %s: %w", hash, err)
	}
	reader, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("failed to read blob %s: %w", hash, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read blob %s content: %w", hash, err)
	}
	return data, nil
}

func (g *goGitRepo) BlobReader(hash Hash) (io.ReadCloser, error) {
	blob, err := g.repo.BlobObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob %s: %w", hash, err)
	}
	reader, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("failed to read blob %s: %w", hash, err)
	}
	return reader, nil
}

func (g *goGitRepo) BlobSize(hash Hash) (int64, error) {
	blob, err := g.repo.BlobObject(hash)
	if err != nil {
		return 0, fmt.Errorf("failed to get blob %s: %w", hash, err)
	}
	return blob.Size, nil
}

func (g *goGitRepo) IsBinaryBlob(hash Hash) (bool, error) {
	reader, err := g.BlobReader(hash)
	if err != nil {
		return false, err
	}
	defer reader.Close()

	isBin, err := binary.IsBinary(reader)
	if err != nil {
		return false, fmt.Errorf("failed to check if blob %s is binary: %w", hash, err)
	}
	return isBin, nil
}

func (g *goGitRepo) CreateBlob(content []byte) (Hash, error) {
	obj := g.repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(content)))

	writer, err := obj.Writer()
	if err != nil {
		return ZeroHash, fmt.Errorf("failed to create blob writer: %w", err)
	}
	if _, err := writer.Write(content); err != nil {
		_ = writer.Close()
		return ZeroHash, fmt.Errorf("failed to write blob content: %w", err)
	}
	if err := writer.Close(); err != nil {
		return ZeroHash, fmt.Errorf("failed to close blob writer: %w", err)
	}

	hash, err := g.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return ZeroHash, fmt.Errorf("failed to store blob: %w", err)
	}
	return hash, nil
}

func (g *goGitRepo) CreateTree(entries []TreeEntry) (Hash, error) {
	tree := &object.Tree{Entries: entries}
	obj := g.repo.Storer.NewEncodedObject()
	if err := tree.Encode(obj); err != nil {
		return ZeroHash, fmt.Errorf("failed to encode tree: %w", err)
	}
	hash, err := g.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return ZeroHash, fmt.Errorf("failed to store tree: %w", err)
	}
	return hash, nil
}

// --- WorktreeOps ---

func (g *goGitRepo) WorktreeRoot() string {
	return g.worktreeDir
}

func (g *goGitRepo) WorktreeStatus(_ context.Context) (map[string]FileStatus, error) {
	wt, err := g.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree status: %w", err)
	}

	result := make(map[string]FileStatus, len(status))
	for path, st := range status {
		result[path] = FileStatus{
			Staging:  convertStatusCode(st.Staging),
			Worktree: convertStatusCode(st.Worktree),
		}
	}
	return result, nil
}

func (g *goGitRepo) HasUncommittedChanges(ctx context.Context) (bool, error) {
	// Use git CLI for this — go-git doesn't respect global gitignore
	// (core.excludesfile), which causes false positives.
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = g.worktreeDir
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to get git status: %w", err)
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

func (g *goGitRepo) CollectChangedFiles(ctx context.Context) (changed, deleted []string, err error) {
	// Use git CLI for accurate status with global gitignore support.
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "-z", "-uall")
	cmd.Dir = g.worktreeDir
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get git status: %w", err)
	}
	return parsePorcelainNul(output)
}

func (g *goGitRepo) Checkout(ctx context.Context, ref string) error {
	// Use git CLI — go-git's Checkout deletes untracked directories in .gitignore.
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("checkout failed: invalid ref %q", ref)
	}
	cmd := exec.CommandContext(ctx, "git", "checkout", ref)
	cmd.Dir = g.worktreeDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (g *goGitRepo) HardReset(ctx context.Context, hash string) error {
	// Use git CLI — go-git's HardReset deletes untracked directories in .gitignore.
	cmd := exec.CommandContext(ctx, "git", "reset", "--hard", hash)
	cmd.Dir = g.worktreeDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reset failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// --- RemoteOps ---

func (g *goGitRepo) Fetch(ctx context.Context, remote, refSpec string, timeout time.Duration) error {
	// Use git CLI — go-git doesn't support credential helpers for HTTPS auth.
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", "fetch", remote, refSpec)
	cmd.Dir = g.worktreeDir
	if output, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("fetch timed out after %s", timeout)
		}
		return fmt.Errorf("failed to fetch from %s: %s: %w", remote, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (g *goGitRepo) Push(ctx context.Context, remote, branch string, noVerify bool) error {
	// Use git CLI — go-git doesn't support credential helpers.
	args := []string{"push"}
	if noVerify {
		args = append(args, "--no-verify")
	}
	args = append(args, remote, branch)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.worktreeDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to push to %s: %s: %w", remote, strings.TrimSpace(string(output)), err)
	}
	return nil
}

// --- DiffOps ---

func (g *goGitRepo) DiffTreeFiles(ctx context.Context, dir, commit1, commit2 string) ([]string, error) {
	// Use git CLI — go-git doesn't expose efficient diff-tree.
	var cmd *exec.Cmd
	if commit1 == "" {
		cmd = exec.CommandContext(ctx, "git", "diff-tree", "--root", "--no-commit-id", "-r", "-z", commit2)
	} else {
		cmd = exec.CommandContext(ctx, "git", "diff-tree", "--no-commit-id", "-r", "-z", commit1, commit2)
	}
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff-tree failed: %w", err)
	}
	return parseDiffTreeOutput(output), nil
}

// --- ConfigOps ---

func (g *goGitRepo) Author() (name, email string) {
	cfg, err := g.repo.Config()
	if err == nil {
		name = cfg.User.Name
		email = cfg.User.Email
	}
	if name == "" {
		name = "Unknown"
	}
	if email == "" {
		email = "unknown@local"
	}
	return name, email
}

func (g *goGitRepo) ConfigValue(ctx context.Context, key string) string {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", key)
	cmd.Dir = g.worktreeDir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (g *goGitRepo) RemoteURL(remote string) string {
	cfg, err := g.repo.Config()
	if err != nil {
		return ""
	}
	r, ok := cfg.Remotes[remote]
	if !ok || len(r.URLs) == 0 {
		return ""
	}
	return r.URLs[0]
}

func (g *goGitRepo) GitDir(ctx context.Context) (string, error) {
	return runRevParse(ctx, g.worktreeDir, "--git-dir")
}

func (g *goGitRepo) HooksDir(ctx context.Context) (string, error) {
	return runRevParse(ctx, g.worktreeDir, "--git-path", "hooks")
}

// --- Helpers ---

func convertRef(ref *plumbing.Reference) *Ref {
	r := &Ref{
		Name: ref.Name(),
		Hash: ref.Hash(),
	}
	if ref.Target() != "" {
		r.Target = ref.Target()
	}
	return r
}

func convertCommit(c *object.Commit) *Commit {
	parents := make([]Hash, len(c.ParentHashes))
	copy(parents, c.ParentHashes)
	return &Commit{
		Hash:     c.Hash,
		TreeHash: c.TreeHash,
		Parents:  parents,
		Author: Signature{
			Name:  c.Author.Name,
			Email: c.Author.Email,
			When:  c.Author.When,
		},
		Committer: Signature{
			Name:  c.Committer.Name,
			Email: c.Committer.Email,
			When:  c.Committer.When,
		},
		Message: c.Message,
	}
}

func convertTree(t *object.Tree) *Tree {
	entries := make([]TreeEntry, len(t.Entries))
	copy(entries, t.Entries)
	return &Tree{
		Hash:    t.Hash,
		Entries: entries,
	}
}

func convertStatusCode(code git.StatusCode) FileStatusCode {
	switch code { //nolint:exhaustive // default covers UpdatedButUnmerged and any future codes
	case git.Modified:
		return StatusModified
	case git.Added:
		return StatusAdded
	case git.Deleted:
		return StatusDeleted
	case git.Renamed:
		return StatusRenamed
	case git.Copied:
		return StatusCopied
	case git.Untracked:
		return StatusUntracked
	default:
		return StatusUnmodified
	}
}

func runRevParse(ctx context.Context, dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"rev-parse"}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("not a git repository")
	}
	return strings.TrimSpace(string(output)), nil
}
