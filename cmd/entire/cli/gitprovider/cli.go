package gitprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
)

// CLI implements WorktreeProvider, RemoteProvider, ConfigProvider, DiffProvider,
// and the branch-related subset of ReferenceProvider by shelling out to the git binary.
type CLI struct {
	// dir is the working directory for git commands.
	// Empty string means use the process's current directory.
	dir string
}

// NewCLI creates a CLI-backed provider that runs git commands in dir.
// If dir is empty, the current working directory is used.
func NewCLI(dir string) *CLI {
	return &CLI{dir: dir}
}

// run executes a git command and returns its trimmed stdout.
func (c *CLI) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if c.dir != "" {
		cmd.Dir = c.dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// runRaw executes a git command and returns raw stdout bytes (no trimming).
func (c *CLI) runRaw(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if c.dir != "" {
		cmd.Dir = c.dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

// --- ReferenceProvider (branch subset) ---

func (c *CLI) BranchExists(name string) (bool, error) {
	ctx := context.Background()
	_, err := c.run(ctx, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	if err != nil {
		// show-ref exits 1 when the ref doesn't exist
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *CLI) DeleteBranch(name string) error {
	ctx := context.Background()

	exists, err := c.BranchExists(name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("branch %q does not exist", name)
	}

	_, err = c.run(ctx, "branch", "-D", "--", name)
	return err
}

// --- WorktreeProvider ---

func (c *CLI) Checkout(ctx context.Context, ref string) error {
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("checkout failed: invalid ref %q", ref)
	}
	_, err := c.run(ctx, "checkout", ref)
	return err
}

func (c *CLI) HardReset(ctx context.Context, commit plumbing.Hash) error {
	_, err := c.run(ctx, "reset", "--hard", commit.String())
	return err
}

func (c *CLI) HasUncommittedChanges(ctx context.Context) (bool, error) {
	output, err := c.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return len(output) > 0, nil
}

func (c *CLI) DetectFileChanges(ctx context.Context) ([]byte, error) {
	return c.runRaw(ctx, "status", "--porcelain", "-z", "-uall")
}

func (c *CLI) StagedFiles(ctx context.Context) ([]string, error) {
	output, err := c.run(ctx, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

func (c *CLI) UntrackedFiles(ctx context.Context) ([]string, error) {
	raw, err := c.runRaw(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	parts := bytes.Split(raw, []byte{0})
	var files []string
	for _, p := range parts {
		if len(p) > 0 {
			files = append(files, string(p))
		}
	}
	return files, nil
}

// --- RemoteProvider ---

func (c *CLI) Fetch(ctx context.Context, remote, refSpec string, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	_, err := c.run(ctx, "fetch", remote, refSpec)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("fetch timed out after %s", timeout)
	}
	return err
}

func (c *CLI) Push(ctx context.Context, remote, refSpec string, noVerify bool, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"push"}
	if noVerify {
		args = append(args, "--no-verify")
	}
	args = append(args, remote, refSpec)

	_, err := c.run(ctx, args...)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("push timed out after %s", timeout)
	}
	return err
}

// --- ConfigProvider ---

func (c *CLI) ConfigValue(ctx context.Context, key string) (string, error) {
	output, err := c.run(ctx, "config", "--get", key)
	if err != nil {
		// git config --get exits 1 when the key is not set
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}
		return "", err
	}
	return output, nil
}

func (c *CLI) GitDir(ctx context.Context) (string, error) {
	output, err := c.run(ctx, "rev-parse", "--git-dir")
	if err != nil {
		return "", errors.New("not a git repository")
	}
	return c.makeAbs(output), nil
}

func (c *CLI) GitCommonDir(ctx context.Context) (string, error) {
	output, err := c.run(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", errors.New("not a git repository")
	}
	return c.makeAbs(output), nil
}

func (c *CLI) HooksDir(ctx context.Context) (string, error) {
	output, err := c.run(ctx, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", errors.New("not a git repository")
	}
	return c.makeAbs(output), nil
}

func (c *CLI) WorktreeRoot(ctx context.Context) (string, error) {
	return c.run(ctx, "rev-parse", "--show-toplevel")
}

func (c *CLI) ValidateBranchName(ctx context.Context, name string) error {
	_, err := c.run(ctx, "check-ref-format", "--branch", name)
	if err != nil {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

func (c *CLI) RevParseSymbolicFullName(ctx context.Context, ref string) (string, error) {
	return c.run(ctx, "rev-parse", "--symbolic-full-name", ref)
}

// --- DiffProvider ---

func (c *CLI) DiffTree(ctx context.Context, repoDir, commit1, commit2 string) ([]string, error) {
	var args []string
	if commit1 == "" {
		args = []string{"diff-tree", "--root", "--no-commit-id", "-r", "-z", commit2}
	} else {
		args = []string{"diff-tree", "--no-commit-id", "-r", "-z", commit1, commit2}
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff-tree: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	return parseDiffTreeOutput(stdout.Bytes()), nil
}

func (c *CLI) IsDisconnected(ctx context.Context, hash1, hash2 string) (bool, error) {
	_, err := c.run(ctx, "merge-base", hash1, hash2)
	if err != nil {
		// merge-base exits 1 when there is no common ancestor
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// makeAbs converts a potentially relative path to absolute using the CLI's dir.
func (c *CLI) makeAbs(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	base := c.dir
	if base == "" {
		base = "."
	}
	return filepath.Clean(filepath.Join(base, path))
}

// parseDiffTreeOutput parses null-separated git diff-tree -r -z output.
func parseDiffTreeOutput(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	parts := bytes.Split(data, []byte{0})
	var files []string
	i := 0
	for i < len(parts) {
		part := string(parts[i])
		if strings.HasPrefix(part, ":") {
			status := extractDiffStatus(part)
			i++
			if i >= len(parts) {
				break
			}
			if path := string(parts[i]); path != "" {
				files = append(files, path)
			}
			i++
			// Renames (R) and copies (C) have a second path
			if (status == 'R' || status == 'C') && i < len(parts) {
				if path2 := string(parts[i]); path2 != "" && !strings.HasPrefix(path2, ":") {
					files = append(files, path2)
					i++
				}
			}
		} else {
			i++
		}
	}
	return files
}

// extractDiffStatus extracts the single-char status from a diff-tree status line.
func extractDiffStatus(statusLine string) byte {
	fields := strings.Fields(strings.TrimSpace(statusLine))
	if len(fields) < 5 || len(fields[4]) == 0 {
		return 0
	}
	return fields[4][0]
}
