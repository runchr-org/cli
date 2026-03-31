package paths

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestIsSubpath(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		// Basic containment
		{name: "child inside parent", parent: "/a/b", child: "/a/b/c", want: true},
		{name: "equal paths", parent: "/a/b", child: "/a/b", want: true},
		{name: "child outside parent", parent: "/a/b", child: "/a/c", want: false},
		{name: "parent prefix but not subpath", parent: "/a/b", child: "/a/bc", want: false},

		// Traversal attacks
		{name: "dot-dot escape", parent: "/a/b", child: "/a/b/../../../etc/passwd", want: false},
		{name: "dot-dot at end", parent: "/a/b", child: "/a/b/..", want: false},
		{name: "dot-dot in middle", parent: "/a/b/c", child: "/a/b/c/../../d", want: false},

		// Relative paths
		{name: "relative child inside", parent: ".entire", child: ".entire/metadata/test", want: true},
		{name: "relative equal", parent: ".entire", child: ".entire", want: true},
		{name: "relative outside", parent: ".entire", child: "src/main.go", want: false},
		{name: "relative prefix not subpath", parent: ".entire", child: ".entirefile", want: false},

		// Edge cases
		{name: "root parent", parent: "/", child: "/anything", want: true},
		{name: "dot current dir", parent: ".", child: "foo/bar", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSubpath(tt.parent, tt.child)
			if got != tt.want {
				t.Errorf("IsSubpath(%q, %q) = %v, want %v", tt.parent, tt.child, got, tt.want)
			}
		})
	}
}

func TestIsInfrastructurePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{".entire/metadata/test", true},
		{".entire", true},
		{"src/main.go", false},
		{".entirefile", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := IsInfrastructurePath(tt.path)
			if got != tt.want {
				t.Errorf("IsInfrastructurePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestSanitizePathForClaude(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/Users/test/myrepo", "-Users-test-myrepo"},
		{"/home/user/project", "-home-user-project"},
		{"simple", "simple"},
		{"/path/with spaces/here", "-path-with-spaces-here"},
		{"/path.with.dots/file", "-path-with-dots-file"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizePathForClaude(tt.input)
			if got != tt.want {
				t.Errorf("SanitizePathForClaude(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetClaudeProjectDir_Override(t *testing.T) {
	// Set the override environment variable
	t.Setenv("ENTIRE_TEST_CLAUDE_PROJECT_DIR", "/tmp/test-claude-project")

	result, err := GetClaudeProjectDir("/some/repo/path")
	if err != nil {
		t.Fatalf("GetClaudeProjectDir() error = %v", err)
	}

	if result != "/tmp/test-claude-project" {
		t.Errorf("GetClaudeProjectDir() = %q, want %q", result, "/tmp/test-claude-project")
	}
}

func TestMainRepoRoot_MainRepo(t *testing.T) {
	// Cannot use t.Parallel: uses t.Chdir
	tmpDir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	tmpDir = resolved

	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")

	ClearWorktreeRootCache()
	t.Chdir(tmpDir)

	root, err := MainRepoRoot(context.Background())
	if err != nil {
		t.Fatalf("MainRepoRoot() error: %v", err)
	}
	if root != tmpDir {
		t.Errorf("MainRepoRoot() = %q, want %q", root, tmpDir)
	}
}

func TestMainRepoRoot_LinkedWorktree(t *testing.T) {
	// Cannot use t.Parallel: uses t.Chdir
	tmpDir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	tmpDir = resolved

	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")

	// Create linked worktree
	worktreeDir := filepath.Join(tmpDir, ".claude", "worktrees", "test-branch")
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cmd := exec.Command("git", "worktree", "add", worktreeDir, "-b", "test-branch") //nolint:noctx // test code
	cmd.Dir = tmpDir
	cmd.Env = testutil.GitIsolatedEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, output)
	}

	ClearWorktreeRootCache()
	t.Chdir(worktreeDir)

	root, err := MainRepoRoot(context.Background())
	if err != nil {
		t.Fatalf("MainRepoRoot() error: %v", err)
	}
	if root != tmpDir {
		t.Errorf("MainRepoRoot() = %q, want %q (should resolve to main repo, not worktree)", root, tmpDir)
	}
}

func TestIsLinkedWorktree(t *testing.T) {
	t.Parallel()

	t.Run("main repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if IsLinkedWorktree(dir) {
			t.Error("IsLinkedWorktree() = true for main repo, want false")
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /repo/.git/worktrees/wt\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !IsLinkedWorktree(dir) {
			t.Error("IsLinkedWorktree() = false for linked worktree, want true")
		}
	})

	t.Run("no git", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if IsLinkedWorktree(dir) {
			t.Error("IsLinkedWorktree() = true for dir without .git, want false")
		}
	})
}

func TestGetClaudeProjectDir_Default(t *testing.T) {
	// Ensure env var is not set by setting it to empty string
	t.Setenv("ENTIRE_TEST_CLAUDE_PROJECT_DIR", "")

	result, err := GetClaudeProjectDir("/Users/test/myrepo")
	if err != nil {
		t.Fatalf("GetClaudeProjectDir() error = %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() error = %v", err)
	}
	expected := filepath.Join(homeDir, ".claude", "projects", "-Users-test-myrepo")

	if result != expected {
		t.Errorf("GetClaudeProjectDir() = %q, want %q", result, expected)
	}
}
