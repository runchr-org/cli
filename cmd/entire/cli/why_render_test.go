package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"

	"github.com/go-git/go-git/v6/plumbing"
)

const whyTestAuthor = "Example Author"

func TestRenderWhyStatic_IncludesCheckpointColumn(t *testing.T) {
	t.Parallel()

	hash := plumbing.NewHash("abcdef1234567890abcdef1234567890abcdef12")
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	data := whyViewData{
		GitPath: "file.go",
		Rows: []whyBlameRow{
			{
				whyBlameLine: whyBlameLine{
					CommitHash: hash.String(),
					FinalLine:  12,
					Source:     "func main() {",
				},
			},
		},
		Commits: map[plumbing.Hash]whyCommitInfo{
			hash: {
				Hash:         hash,
				CheckpointID: cpID,
			},
		},
	}

	output := renderWhyStatic(data)
	assertWhyOutputContains(t, output,
		"LINE",
		"CHECKPOINT",
		"CODE",
		"  12",
		cpID.String(),
		"func main() {",
	)
}

func TestRenderWhyStatic_GutterColumnsFollowRequestedOrder(t *testing.T) {
	t.Parallel()

	hash := plumbing.NewHash("c56b7ac719000000000000000000000000000000")
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	data := whyViewData{
		GitPath: "file.go",
		Rows: []whyBlameRow{
			{
				whyBlameLine: whyBlameLine{
					CommitHash: hash.String(),
					FinalLine:  42,
					Author:     whyTestAuthor,
					AuthorTime: time.Now().Add(-6 * 24 * time.Hour),
					Source:     "func main() {}",
				},
			},
		},
		Commits: map[plumbing.Hash]whyCommitInfo{
			hash: {
				Hash:         hash,
				CheckpointID: cpID,
			},
		},
	}

	output := renderWhyStatic(data)
	assertWhyOutputContains(t, output,
		"TIME",
		"AUTHOR",
		"COMMIT",
		"CHECKPOINT",
		"LINE",
		"CODE",
		"6d ago",
		whyTestAuthor,
		"c56b7ac719",
		cpID.String(),
		"42 func main() {}",
	)
	if strings.Index(output, "6d ago") > strings.Index(output, whyTestAuthor) ||
		strings.Index(output, whyTestAuthor) > strings.Index(output, "c56b7ac719") ||
		strings.Index(output, "c56b7ac719") > strings.Index(output, cpID.String()) ||
		strings.Index(output, cpID.String()) > strings.Index(output, "42") {
		t.Fatalf("gutter columns rendered out of order:\n%s", output)
	}
}

func TestRenderWhyStatic_GutterColumnsHaveFixedWidths(t *testing.T) {
	t.Parallel()

	hashA := plumbing.NewHash("c56b7ac719000000000000000000000000000000")
	hashB := plumbing.NewHash("d56b7ac719000000000000000000000000000000")
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	now := time.Now()
	data := whyViewData{
		GitPath: "file.go",
		Rows: []whyBlameRow{
			{
				whyBlameLine: whyBlameLine{
					CommitHash: hashA.String(),
					FinalLine:  7,
					Author:     whyTestAuthor,
					AuthorTime: now.Add(-6 * 24 * time.Hour),
					Source:     "short := true",
				},
			},
			{
				whyBlameLine: whyBlameLine{
					CommitHash: hashB.String(),
					FinalLine:  100,
					Author:     "A",
					Source:     "longer := false",
				},
			},
		},
		Commits: map[plumbing.Hash]whyCommitInfo{
			hashA: {
				Hash:         hashA,
				CheckpointID: cpID,
			},
			hashB: {
				Hash: hashB,
			},
		},
	}

	lines := strings.Split(strings.TrimSpace(renderWhyStatic(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered lines = %d, want header plus two rows:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	firstCodeColumn := strings.Index(lines[1], "short := true")
	secondCodeColumn := strings.Index(lines[2], "longer := false")
	if firstCodeColumn == -1 || secondCodeColumn == -1 {
		t.Fatalf("missing source code in output:\n%s", strings.Join(lines, "\n"))
	}
	if firstCodeColumn != secondCodeColumn {
		t.Fatalf("code columns differ: first=%d second=%d\n%s", firstCodeColumn, secondCodeColumn, strings.Join(lines, "\n"))
	}
}

func TestRenderWhyStatic_FallbackValuesForNonEntireCommit(t *testing.T) {
	t.Parallel()

	hash := plumbing.NewHash("1111111111111111111111111111111111111111")
	data := whyViewData{
		GitPath: "file.go",
		Rows: []whyBlameRow{
			{
				whyBlameLine: whyBlameLine{
					CommitHash: hash.String(),
					FinalLine:  1,
					Source:     "package main",
				},
			},
		},
		Commits: map[plumbing.Hash]whyCommitInfo{},
	}

	output := renderWhyStatic(data)
	assertWhyOutputContains(t, output,
		"1 -",
		"package main",
	)
}

func TestWhyStaticMode_RendersFileForNonInteractiveOutput(t *testing.T) {
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	testutil.WriteFile(t, repoDir, "file.go", "package main\n\nfunc main() {}\n")
	testutil.GitAdd(t, repoDir, "file.go")
	testutil.GitCommit(t, repoDir, "initial")

	t.Chdir(repoDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	var out bytes.Buffer
	err := runWhy(context.Background(), &out, "file.go")
	if err != nil {
		t.Fatalf("runWhy() error = %v", err)
	}

	assertWhyOutputContains(t, out.String(),
		"LINE",
		"CHECKPOINT",
		"CODE",
		"package main",
		"func main() {}",
	)
}

func TestWhyStaticMode_RendersCheckpointTrailer(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	cpID := id.MustCheckpointID("b1b2c3d4e5f6")
	whyTestCommit(t, repoDir, "linked commit\n\nEntire-Checkpoint: "+cpID.String()+"\n")

	t.Chdir(repoDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	var out bytes.Buffer
	err := runWhy(ctx, &out, "file.go")
	if err != nil {
		t.Fatalf("runWhy() error = %v", err)
	}

	assertWhyOutputContains(t, out.String(),
		cpID.String(),
		"package main",
	)
}

func assertWhyOutputContains(t *testing.T, output string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
