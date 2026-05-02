package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestWhyCmd_ModeFlagsAreNotRegistered(t *testing.T) {
	t.Parallel()

	cmd := newWhyCmd()

	if flag := cmd.Flags().Lookup("interactive"); flag != nil {
		t.Fatal("did not expect --interactive flag to be registered")
	}
	if flag := cmd.Flags().ShorthandLookup("i"); flag != nil {
		t.Fatal("did not expect -i shorthand to be registered")
	}
	if flag := cmd.Flags().Lookup("no-pager"); flag != nil {
		t.Fatal("did not expect --no-pager flag to be registered")
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

func TestWhyCmd_InteractiveOverviewLoadsFileAndStartsTUI(t *testing.T) {
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	testutil.WriteFile(t, repoDir, "file.go", "package main\n")
	testutil.GitAdd(t, repoDir, "file.go")
	testutil.GitCommit(t, repoDir, "initial")

	t.Chdir(repoDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	originalCanRunWhyTUI := canRunWhyTUI
	canRunWhyTUI = func(io.Writer) bool { return true }
	t.Cleanup(func() { canRunWhyTUI = originalCanRunWhyTUI })

	originalRunWhyTUI := runWhyTUI
	var gotData whyViewData
	runWhyTUI = func(_ context.Context, _ io.Writer, data whyViewData) error {
		gotData = data
		return nil
	}
	t.Cleanup(func() { runWhyTUI = originalRunWhyTUI })

	err := runWhy(context.Background(), &bytes.Buffer{}, "file.go")
	if err != nil {
		t.Fatalf("runWhy() error = %v", err)
	}
	if gotData.GitPath != "file.go" {
		t.Fatalf("TUI GitPath = %q, want file.go", gotData.GitPath)
	}
	if len(gotData.Rows) != 1 {
		t.Fatalf("TUI rows = %d, want 1", len(gotData.Rows))
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
		wantErr     bool
	}{
		{
			name:        "relative path",
			input:       "dir/file.go",
			wantGitPath: "dir/file.go",
		},
		{
			name:        "absolute path inside repo",
			input:       filepath.Join(repoDir, "dir", "file.go"),
			wantGitPath: "dir/file.go",
		},
		{
			name:    "outside repo",
			input:   filepath.Join(t.TempDir(), "outside.go"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRepoRoot, gotGitPath, err := resolveWhyPath(ctx, tt.input)
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
		})
	}
}
