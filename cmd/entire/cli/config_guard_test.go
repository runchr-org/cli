package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// initTestRepo creates a minimal git repo in a temp dir and chdir into it.
func initTestRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	repo, err := git.PlainInit(tmpDir, false)
	if err != nil {
		t.Fatalf("Failed to init repo: %v", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Failed to get worktree: %v", err)
	}
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	if _, err := w.Add("test.txt"); err != nil {
		t.Fatalf("Failed to add test file: %v", err)
	}
	if _, err := w.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@example.com"},
	}); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	return tmpDir
}

// captureStderr captures stderr output during fn execution.
// Restores os.Stderr before reading to avoid resource leaks.
// NOT safe for parallel tests (mutates process-global os.Stderr).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	defer r.Close()
	os.Stderr = w

	fn()

	w.Close()
	os.Stderr = oldStderr // Restore BEFORE reading to avoid leak on panic

	data, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("Failed to read from pipe: %v", readErr)
	}
	return string(data)
}

func TestValidateConfigNotCorrupted_Clean(t *testing.T) {
	initTestRepo(t)

	// Verify precondition: no local user.name set (initTestRepo uses go-git commit author, not local config)
	name := getLocalGitConfigValue("user.name")
	if name != "" {
		t.Fatalf("precondition failed: expected no local user.name, got %q", name)
	}

	output := captureStderr(t, func() {
		validateConfigNotCorrupted(context.Background())
	})

	if len(output) > 0 {
		t.Errorf("expected no warning for clean config, got: %s", output)
	}
}

func TestValidateConfigNotCorrupted_Detects456(t *testing.T) {
	dir := initTestRepo(t)

	// Set user.name to the #456 corruption pattern
	setLocalConfig(t, dir, "user.name", "user.email")

	// Verify precondition
	name := getLocalGitConfigValue("user.name")
	if name != "user.email" {
		t.Fatalf("precondition failed: expected user.name=%q, got %q", "user.email", name)
	}

	output := captureStderr(t, func() {
		validateConfigNotCorrupted(context.Background())
	})

	if len(output) == 0 {
		t.Fatal("expected warning for corrupted config, got nothing")
	}
	if !strings.Contains(output, "WARNING") {
		t.Errorf("expected WARNING for corrupted config, got: %s", output)
	}
	if !strings.Contains(output, "456") {
		t.Errorf("expected #456 reference, got: %s", output)
	}
	if !strings.Contains(output, "git config --local user.name") {
		t.Errorf("expected fix instructions, got: %s", output)
	}
}

func setLocalConfig(t *testing.T, dir, key, value string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "config", "--local", key, value)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to set local config %s=%s: %v", key, value, err)
	}
}
