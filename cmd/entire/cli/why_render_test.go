package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"

	"github.com/go-git/go-git/v6/plumbing"
)

func TestRenderWhyStatic_IncludesEnrichedColumns(t *testing.T) {
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
					Author:     "Fallback Author",
					Source:     "func main() {",
				},
			},
		},
		Commits: map[plumbing.Hash]whyCommitInfo{
			hash: {
				Hash:         hash,
				Author:       "Pat Example",
				CheckpointID: cpID,
				Checkpoint: whyCheckpointInfo{
					Found:  true,
					Agents: []types.AgentType{types.AgentType("Claude Code")},
				},
			},
		},
	}

	output := renderWhyStatic(data)
	assertWhyOutputContains(t, output,
		"LINE",
		"COMMIT",
		"AUTHOR",
		"CHECKPOINT",
		"AGENT",
		"  12 abcdef12 Pat Example",
		cpID.String(),
		"Claude Code",
		"func main() {",
	)
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
					Author:     "Git Author",
					Source:     "package main",
				},
			},
		},
		Commits: map[plumbing.Hash]whyCommitInfo{},
	}

	output := renderWhyStatic(data)
	assertWhyOutputContains(t, output,
		"1 11111111 Git Author",
		"-            -",
		"package main",
	)
}

func TestWhyStaticAgent_RendersAllCheckpointAgents(t *testing.T) {
	t.Parallel()

	info := whyCommitInfo{
		Checkpoint: whyCheckpointInfo{
			Agents: []types.AgentType{types.AgentType("claude"), types.AgentType("Codex")},
		},
	}

	if got := whyStaticAgent(info); got != "Claude Code, Codex" {
		t.Fatalf("whyStaticAgent() = %q, want Claude Code, Codex", got)
	}
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
	err := runWhy(context.Background(), &out, &bytes.Buffer{}, whyOptions{Path: "file.go"})
	if err != nil {
		t.Fatalf("runWhy() error = %v", err)
	}

	assertWhyOutputContains(t, out.String(),
		"LINE",
		"COMMIT",
		"AUTHOR",
		"CHECKPOINT",
		"AGENT",
		"package main",
		"func main() {}",
	)
}

func TestWhyStaticMode_RendersEntireCheckpointData(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	cpID := id.MustCheckpointID("b1b2c3d4e5f6")
	whyTestCommit(t, repoDir, "linked commit\n\nEntire-Checkpoint: "+cpID.String()+"\n", "package main\n")
	repo := whyTestOpenRepo(t, repoDir)
	whyTestWriteCommittedCheckpoint(ctx, t, repo, cpID, &checkpoint.Summary{
		Intent:  "Explain static why",
		Outcome: "Rendered checkpoint columns",
	}, []string{"prompt"})

	t.Chdir(repoDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	var out bytes.Buffer
	err := runWhy(ctx, &out, &bytes.Buffer{}, whyOptions{Path: "file.go"})
	if err != nil {
		t.Fatalf("runWhy() error = %v", err)
	}

	assertWhyOutputContains(t, out.String(),
		cpID.String(),
		"Claude Code",
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
