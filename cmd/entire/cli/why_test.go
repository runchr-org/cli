package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestWhyCmd_Flags(t *testing.T) {
	t.Parallel()

	cmd := newWhyCmd()

	tests := []struct {
		name      string
		shorthand string
	}{
		{name: "interactive", shorthand: "i"},
		{name: "no-pager"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flag := cmd.Flags().Lookup(tt.name)
			if flag == nil {
				t.Fatalf("expected --%s flag to exist", tt.name)
			}
			if flag.Shorthand != tt.shorthand {
				t.Fatalf("--%s shorthand = %q, want %q", tt.name, flag.Shorthand, tt.shorthand)
			}
		})
	}
}

func TestWhyCmd_LineFlagsAreNotRegistered(t *testing.T) {
	t.Parallel()

	cmd := newWhyCmd()

	if flag := cmd.Flags().Lookup("lines"); flag != nil {
		t.Fatalf("did not expect --lines flag to be registered")
	}
	if flag := cmd.Flags().ShorthandLookup("L"); flag != nil {
		t.Fatalf("did not expect -L shorthand to be registered")
	}
}

func TestWhyCmd_NoPathNonInteractiveErrors(t *testing.T) {
	t.Parallel()

	cmd := newWhyCmd()
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected no-path non-interactive command to fail")
	}
	if !strings.Contains(err.Error(), "path required") {
		t.Fatalf("expected path-required error, got: %v", err)
	}
}

func TestWhyCmd_ExplicitInteractiveRequiresTerminalBeforePath(t *testing.T) {
	t.Parallel()

	cmd := newWhyCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--interactive"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected explicit interactive command to fail without a terminal")
	}
	if !strings.Contains(err.Error(), "interactive mode requires a real terminal") {
		t.Fatalf("expected interactive terminal error, got: %v", err)
	}
}

func TestResolveWhyPath(t *testing.T) {
	repoDir := t.TempDir()
	wantRepoRoot := normalizeWhyPathForRel(repoDir)
	testutil.InitRepo(t, repoDir)
	testutil.WriteFile(t, repoDir, "dir/file.go", "package main\n")
	testutil.GitAdd(t, repoDir, "dir/file.go")
	testutil.GitCommit(t, repoDir, "initial")

	t.Chdir(repoDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	ctx := context.Background()
	tests := []struct {
		name        string
		input       string
		wantGitPath string
		wantAbsPath string
		wantErr     bool
	}{
		{
			name:        "relative path",
			input:       "dir/file.go",
			wantGitPath: "dir/file.go",
			wantAbsPath: filepath.Join(wantRepoRoot, "dir", "file.go"),
		},
		{
			name:        "absolute path inside repo",
			input:       filepath.Join(repoDir, "dir", "file.go"),
			wantGitPath: "dir/file.go",
			wantAbsPath: filepath.Join(wantRepoRoot, "dir", "file.go"),
		},
		{
			name:    "outside repo",
			input:   filepath.Join(t.TempDir(), "outside.go"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRepoRoot, gotGitPath, gotAbsPath, err := resolveWhyPath(ctx, tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWhyPath() error = %v", err)
			}
			if gotRepoRoot != wantRepoRoot {
				t.Fatalf("repoRoot = %q, want %q", gotRepoRoot, wantRepoRoot)
			}
			if gotGitPath != tt.wantGitPath {
				t.Fatalf("gitPath = %q, want %q", gotGitPath, tt.wantGitPath)
			}
			if gotAbsPath != tt.wantAbsPath {
				t.Fatalf("absPath = %q, want %q", gotAbsPath, tt.wantAbsPath)
			}
		})
	}
}
