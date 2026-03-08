package gitbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
)

// cliRepo implements Repository using the git CLI.
type cliRepo struct {
	dir string
	// goGitRepo is kept for GoGitRepository() during gradual migration.
	// It may be nil if no go-git repo was provided.
	goGitRepo *git.Repository
}

// Compile-time interface checks.
var (
	_ RefOps      = (*cliRepo)(nil)
	_ CommitOps   = (*cliRepo)(nil)
	_ ObjectOps   = (*cliRepo)(nil)
	_ WorktreeOps = (*cliRepo)(nil)
	_ RemoteOps   = (*cliRepo)(nil)
	_ DiffOps     = (*cliRepo)(nil)
	_ ConfigOps   = (*cliRepo)(nil)
	_ Repository  = (*cliRepo)(nil)
)

// NewCLIRepository creates a Repository backed by the git CLI.
// dir is the working directory for git commands.
// goGitRepo is optional — only needed for GoGitRepository() during migration.
func NewCLIRepository(dir string, goGitRepo *git.Repository) *cliRepo { //nolint:revive // unexported return is intentional; callers use Repository interface
	return &cliRepo{dir: dir, goGitRepo: goGitRepo}
}

func (c *cliRepo) GoGitRepository() *git.Repository { return c.goGitRepo }

// run executes a git command and returns stdout.
func (c *cliRepo) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s failed: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return stdout.String(), nil
}

// runRaw executes a git command and returns raw stdout bytes.
func (c *cliRepo) runRaw(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s failed: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

// --- RefOps ---

func (c *cliRepo) Head() (*Ref, error) {
	ctx := context.Background()
	// Get hash
	hashStr, err := c.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}
	hash := plumbing.NewHash(strings.TrimSpace(hashStr))

	// Get symbolic ref for branch name
	ref := &Ref{Hash: hash}
	symRef, err := c.run(ctx, "symbolic-ref", "HEAD")
	if err == nil {
		name := plumbing.ReferenceName(strings.TrimSpace(symRef))
		ref.Name = name
	} else {
		// Detached HEAD
		ref.Name = plumbing.HEAD
	}
	return ref, nil
}

func (c *cliRepo) Reference(name ReferenceName, _ bool) (*Ref, error) {
	ctx := context.Background()
	hashStr, err := c.run(ctx, "rev-parse", "--verify", string(name))
	if err != nil {
		return nil, plumbing.ErrReferenceNotFound
	}
	return &Ref{
		Name: name,
		Hash: plumbing.NewHash(strings.TrimSpace(hashStr)),
	}, nil
}

func (c *cliRepo) SetReference(name ReferenceName, hash Hash) error {
	ctx := context.Background()
	_, err := c.run(ctx, "update-ref", string(name), hash.String())
	return err
}

func (c *cliRepo) DeleteReference(name ReferenceName) error {
	ctx := context.Background()
	_, err := c.run(ctx, "update-ref", "-d", string(name))
	return err
}

func (c *cliRepo) ValidateBranchName(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", name)
	cmd.Dir = c.dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

func (c *cliRepo) IsEmpty() bool {
	ctx := context.Background()
	_, err := c.run(ctx, "rev-parse", "HEAD")
	return err != nil
}

// --- CommitOps ---

func (c *cliRepo) CommitObject(hash Hash) (*Commit, error) {
	ctx := context.Background()
	// Use git cat-file to read commit data
	output, err := c.run(ctx, "cat-file", "-p", hash.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit %s: %w", hash, err)
	}
	return parseCatFileCommit(hash, output)
}

func (c *cliRepo) LogEach(ctx context.Context, from Hash, fn func(*Commit) error) error {
	// Use git log with a format that's easy to parse
	output, err := c.run(ctx, "log", "--format=%H%n%T%n%P%n%an%n%ae%n%aI%n%cn%n%ce%n%cI%n%B%x00", from.String())
	if err != nil {
		return fmt.Errorf("failed to get log from %s: %w", from, err)
	}

	entries := strings.Split(output, "\x00")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		commit, parseErr := parseLogEntry(entry)
		if parseErr != nil {
			continue // skip unparseable entries
		}
		if err := fn(commit); err != nil {
			if errors.Is(err, ErrStop) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (c *cliRepo) CreateCommit(treeHash, parentHash Hash, message, authorName, authorEmail string) (Hash, error) {
	ctx := context.Background()
	args := []string{"commit-tree", treeHash.String()}
	if parentHash != ZeroHash {
		args = append(args, "-p", parentHash.String())
	}
	args = append(args, "-m", message)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME="+authorName,
		"GIT_AUTHOR_EMAIL="+authorEmail,
		"GIT_COMMITTER_NAME="+authorName,
		"GIT_COMMITTER_EMAIL="+authorEmail,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ZeroHash, fmt.Errorf("commit-tree failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return plumbing.NewHash(strings.TrimSpace(stdout.String())), nil
}

func (c *cliRepo) MergeBase(a, b Hash) ([]Hash, error) {
	ctx := context.Background()
	output, err := c.run(ctx, "merge-base", a.String(), b.String())
	if err != nil {
		return nil, fmt.Errorf("failed to find merge base: %w", err)
	}

	var hashes []Hash
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			hashes = append(hashes, plumbing.NewHash(line))
		}
	}
	return hashes, nil
}

// --- ObjectOps ---

func (c *cliRepo) TreeObject(hash Hash) (*Tree, error) {
	ctx := context.Background()
	output, err := c.run(ctx, "ls-tree", hash.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get tree %s: %w", hash, err)
	}

	entries, parseErr := parseLsTree(output)
	if parseErr != nil {
		return nil, parseErr
	}
	return &Tree{Hash: hash, Entries: entries}, nil
}

func (c *cliRepo) TreeFile(treeHash Hash, path string) (string, error) {
	ctx := context.Background()
	output, err := c.run(ctx, "cat-file", "-p", treeHash.String()+":"+path)
	if err != nil {
		return "", fmt.Errorf("file %s not found in tree %s: %w", path, treeHash, err)
	}
	return output, nil
}

func (c *cliRepo) TreeSubtree(treeHash Hash, path string) (*Tree, error) {
	ctx := context.Background()
	// Get the subtree hash via ls-tree
	output, err := c.run(ctx, "ls-tree", treeHash.String(), path+"/")
	if err != nil || strings.TrimSpace(output) == "" {
		return nil, fmt.Errorf("subtree %s not found in %s", path, treeHash)
	}

	// Parse to get the subtree hash
	line := strings.TrimSpace(strings.Split(output, "\n")[0])
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return nil, fmt.Errorf("unexpected ls-tree output for subtree %s", path)
	}
	subHash := plumbing.NewHash(parts[2])
	return c.TreeObject(subHash)
}

func (c *cliRepo) TreeFindEntry(treeHash Hash, path string) (*TreeEntry, error) {
	ctx := context.Background()
	output, err := c.run(ctx, "ls-tree", treeHash.String(), path)
	if err != nil || strings.TrimSpace(output) == "" {
		return nil, fmt.Errorf("entry %s not found in tree %s", path, treeHash)
	}

	entries, parseErr := parseLsTree(strings.Split(strings.TrimSpace(output), "\n")[0])
	if parseErr != nil || len(entries) == 0 {
		return nil, fmt.Errorf("failed to parse entry %s: %w", path, parseErr)
	}
	return &entries[0], nil
}

func (c *cliRepo) BlobContents(hash Hash) ([]byte, error) {
	ctx := context.Background()
	data, err := c.runRaw(ctx, "cat-file", "-p", hash.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get blob %s: %w", hash, err)
	}
	return data, nil
}

func (c *cliRepo) BlobReader(hash Hash) (io.ReadCloser, error) {
	content, err := c.BlobContents(hash)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (c *cliRepo) BlobSize(hash Hash) (int64, error) {
	ctx := context.Background()
	output, err := c.run(ctx, "cat-file", "-s", hash.String())
	if err != nil {
		return 0, fmt.Errorf("failed to get blob size %s: %w", hash, err)
	}
	size, parseErr := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if parseErr != nil {
		return 0, fmt.Errorf("failed to parse blob size: %w", parseErr)
	}
	return size, nil
}

func (c *cliRepo) IsBinaryBlob(hash Hash) (bool, error) {
	// Use git's diff heuristic to detect binary
	ctx := context.Background()
	output, err := c.run(ctx, "diff", "--no-index", "--numstat", "/dev/null", hash.String())
	if err != nil {
		// Fallback: read first bytes and check for null bytes
		content, contentErr := c.BlobContents(hash)
		if contentErr != nil {
			return false, contentErr
		}
		return bytes.ContainsRune(content[:min(len(content), 8000)], 0), nil
	}
	// Binary files show "-\t-" in numstat
	return strings.HasPrefix(strings.TrimSpace(output), "-\t-"), nil
}

func (c *cliRepo) CreateBlob(content []byte) (Hash, error) {
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "git", "hash-object", "-w", "--stdin")
	cmd.Dir = c.dir
	cmd.Stdin = bytes.NewReader(content)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ZeroHash, fmt.Errorf("hash-object failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return plumbing.NewHash(strings.TrimSpace(stdout.String())), nil
}

func (c *cliRepo) CreateTree(entries []TreeEntry) (Hash, error) {
	ctx := context.Background()
	// Build mktree input: "<mode> <type> <hash>\t<name>\n"
	var buf bytes.Buffer
	for _, e := range entries {
		objType := "blob"
		if e.Mode == filemode.Dir {
			objType = "tree"
		}
		fmt.Fprintf(&buf, "%06o %s %s\t%s\n", uint32(e.Mode), objType, e.Hash, e.Name)
	}

	cmd := exec.CommandContext(ctx, "git", "mktree")
	cmd.Dir = c.dir
	cmd.Stdin = &buf
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ZeroHash, fmt.Errorf("mktree failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return plumbing.NewHash(strings.TrimSpace(stdout.String())), nil
}

// --- WorktreeOps ---

func (c *cliRepo) WorktreeRoot() string {
	return c.dir
}

func (c *cliRepo) WorktreeStatus(ctx context.Context) (map[string]FileStatus, error) {
	output, err := c.runRaw(ctx, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	result := make(map[string]FileStatus)
	entries := bytes.Split(output, []byte{0})
	for _, entry := range entries {
		if len(entry) < 4 {
			continue
		}
		staging := FileStatusCode(entry[0])
		worktree := FileStatusCode(entry[1])
		path := string(entry[3:])
		if path != "" {
			result[path] = FileStatus{Staging: staging, Worktree: worktree}
		}
	}
	return result, nil
}

func (c *cliRepo) HasUncommittedChanges(ctx context.Context) (bool, error) {
	output, err := c.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("failed to get git status: %w", err)
	}
	return len(strings.TrimSpace(output)) > 0, nil
}

func (c *cliRepo) CollectChangedFiles(ctx context.Context) (changed, deleted []string, err error) {
	output, err := c.runRaw(ctx, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get git status: %w", err)
	}
	return parsePorcelainNul(output)
}

func (c *cliRepo) Checkout(ctx context.Context, ref string) error {
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("checkout failed: invalid ref %q", ref)
	}
	cmd := exec.CommandContext(ctx, "git", "checkout", ref)
	cmd.Dir = c.dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (c *cliRepo) HardReset(ctx context.Context, hash string) error {
	cmd := exec.CommandContext(ctx, "git", "reset", "--hard", hash)
	cmd.Dir = c.dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reset failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// --- RemoteOps ---

func (c *cliRepo) Fetch(ctx context.Context, remote, refSpec string, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", "fetch", remote, refSpec)
	cmd.Dir = c.dir
	if output, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("fetch timed out after %s", timeout)
		}
		return fmt.Errorf("failed to fetch from %s: %s: %w", remote, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (c *cliRepo) Push(ctx context.Context, remote, branch string, noVerify bool) error {
	args := []string{"push"}
	if noVerify {
		args = append(args, "--no-verify")
	}
	args = append(args, remote, branch)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to push to %s: %s: %w", remote, strings.TrimSpace(string(output)), err)
	}
	return nil
}

// --- DiffOps ---

func (c *cliRepo) DiffTreeFiles(ctx context.Context, dir, commit1, commit2 string) ([]string, error) {
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

func (c *cliRepo) Author() (name, email string) {
	ctx := context.Background()
	name = c.ConfigValue(ctx, "user.name")
	email = c.ConfigValue(ctx, "user.email")
	if name == "" {
		name = "Unknown"
	}
	if email == "" {
		email = "unknown@local"
	}
	return name, email
}

func (c *cliRepo) ConfigValue(ctx context.Context, key string) string {
	output, err := c.run(ctx, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func (c *cliRepo) RemoteURL(remote string) string {
	ctx := context.Background()
	output, err := c.run(ctx, "remote", "get-url", remote)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func (c *cliRepo) GitDir(ctx context.Context) (string, error) {
	return runRevParse(ctx, c.dir, "--git-dir")
}

func (c *cliRepo) HooksDir(ctx context.Context) (string, error) {
	return runRevParse(ctx, c.dir, "--git-path", "hooks")
}
