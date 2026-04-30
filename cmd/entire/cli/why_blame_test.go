package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestParseBlamePorcelain(t *testing.T) {
	t.Parallel()

	const blame = `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 1 1 2
author Alice Example
author-time 1714560000
filename dir/file with spaces.go
	package main
aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 2 2
		fmt.Println("hi")
bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb 1 3 1
author Bob Example
author-time 1714560600
filename dir/file with spaces.go
	func done() {}
`

	lines, err := parseBlamePorcelain([]byte(blame))
	if err != nil {
		t.Fatalf("parseBlamePorcelain() error = %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("parsed %d lines, want 3", len(lines))
	}

	first := lines[0]
	if first.CommitHash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("first commit = %q", first.CommitHash)
	}
	if first.OriginalLine != 1 {
		t.Fatalf("first original line = %d, want 1", first.OriginalLine)
	}
	if first.FinalLine != 1 {
		t.Fatalf("first final line = %d, want 1", first.FinalLine)
	}
	if first.Author != "Alice Example" {
		t.Fatalf("first author = %q", first.Author)
	}
	if first.AuthorTime.Unix() != 1714560000 {
		t.Fatalf("first author time = %d, want 1714560000", first.AuthorTime.Unix())
	}
	if first.Filename != "dir/file with spaces.go" {
		t.Fatalf("first filename = %q", first.Filename)
	}
	if first.Source != "package main" {
		t.Fatalf("first source = %q", first.Source)
	}

	second := lines[1]
	if second.Author != "Alice Example" {
		t.Fatalf("second author = %q, want repeated metadata", second.Author)
	}
	if second.AuthorTime.Unix() != 1714560000 {
		t.Fatalf("second author time = %d, want repeated metadata", second.AuthorTime.Unix())
	}
	if second.Filename != "dir/file with spaces.go" {
		t.Fatalf("second filename = %q, want repeated metadata", second.Filename)
	}
	if second.Source != "\tfmt.Println(\"hi\")" {
		t.Fatalf("second source = %q, want leading tab preserved", second.Source)
	}

	third := lines[2]
	if third.CommitHash != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("third commit = %q", third.CommitHash)
	}
	if third.OriginalLine != 1 {
		t.Fatalf("third original line = %d, want 1", third.OriginalLine)
	}
	if third.FinalLine != 3 {
		t.Fatalf("third final line = %d, want 3", third.FinalLine)
	}
	if third.Author != "Bob Example" {
		t.Fatalf("third author = %q", third.Author)
	}
}

func TestParseBlamePorcelain_InvalidRecord(t *testing.T) {
	t.Parallel()

	const blame = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 1 1 1\nauthor Alice\n"

	_, err := parseBlamePorcelain([]byte(blame))
	if err == nil {
		t.Fatal("expected missing source line to fail")
	}
	if !strings.Contains(err.Error(), "missing source line") {
		t.Fatalf("expected missing source line error, got: %v", err)
	}
}

func TestCollapseWhyBlameBlocks(t *testing.T) {
	t.Parallel()

	lines := []whyBlameLine{
		{CommitHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FinalLine: 1},
		{CommitHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FinalLine: 2},
		{CommitHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", FinalLine: 3},
		{CommitHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FinalLine: 4},
	}

	blocks := collapseWhyBlameBlocks(lines)
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(blocks))
	}
	if blocks[0].CommitHash != lines[0].CommitHash || blocks[0].StartLine != 1 || blocks[0].EndLine != 2 {
		t.Fatalf("first block = %+v", blocks[0])
	}
	if blocks[1].CommitHash != lines[2].CommitHash || blocks[1].StartLine != 3 || blocks[1].EndLine != 3 {
		t.Fatalf("second block = %+v", blocks[1])
	}
	if blocks[2].CommitHash != lines[3].CommitHash || blocks[2].StartLine != 4 || blocks[2].EndLine != 4 {
		t.Fatalf("third block = %+v", blocks[2])
	}

	rows := buildWhyBlameRows(lines)
	if len(rows) != len(lines) {
		t.Fatalf("rows = %d, want %d", len(rows), len(lines))
	}
	for i, row := range rows {
		if row.CommitHash != lines[i].CommitHash || row.FinalLine != lines[i].FinalLine {
			t.Fatalf("row[%d] = %+v, want line %+v", i, row, lines[i])
		}
	}
}

func TestRunGitBlame(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	testutil.WriteFile(t, repoDir, "file.go", "package main\n\nfunc main() {}\n")
	testutil.GitAdd(t, repoDir, "file.go")
	testutil.GitCommit(t, repoDir, "initial")

	output, err := runGitBlame(context.Background(), repoDir, "file.go")
	if err != nil {
		t.Fatalf("runGitBlame() error = %v", err)
	}
	lines, err := parseBlamePorcelain(output)
	if err != nil {
		t.Fatalf("parseBlamePorcelain(runGitBlame()) error = %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("parsed %d blamed lines, want 3", len(lines))
	}
	if lines[0].Source != "package main" {
		t.Fatalf("first source = %q", lines[0].Source)
	}
}

func TestRunGitBlame_MissingPathIncludesStderr(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	testutil.WriteFile(t, repoDir, "file.go", "package main\n")
	testutil.GitAdd(t, repoDir, "file.go")
	testutil.GitCommit(t, repoDir, "initial")

	_, err := runGitBlame(context.Background(), repoDir, "missing.go")
	if err == nil {
		t.Fatal("expected missing path to fail")
	}
	if !strings.Contains(err.Error(), "git blame failed for missing.go") {
		t.Fatalf("expected path in error, got: %v", err)
	}
}
