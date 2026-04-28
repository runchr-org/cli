package cli

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/entireio/cli/redact"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

func TestWhyEnrichCommits_NoCheckpointTrailerUsesGitMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	hash := whyTestCommit(t, repoDir, "plain commit", "package main\n")
	repo := whyTestOpenRepo(t, repoDir)
	lookup := whyTestCheckpointLookup(repo)

	infoByCommit := enrichWhyCommits(ctx, repo, lookup, []whyBlameBlock{{CommitHash: hash.String()}})
	info, ok := infoByCommit[hash]
	if !ok {
		t.Fatalf("missing info for commit %s", hash)
	}
	if info.Subject != "plain commit" {
		t.Fatalf("subject = %q, want plain commit", info.Subject)
	}
	if info.Author != "Test User" {
		t.Fatalf("author = %q, want Test User", info.Author)
	}
	if !info.CheckpointID.IsEmpty() {
		t.Fatalf("checkpoint ID = %q, want empty", info.CheckpointID)
	}
	if info.Checkpoint.Found {
		t.Fatal("checkpoint should not be marked found")
	}
	if info.Summary != "plain commit" {
		t.Fatalf("summary = %q, want commit subject fallback", info.Summary)
	}
}

func TestWhyEnrichCommits_MissingCheckpointDegradesToGitMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	hash := whyTestCommit(t, repoDir, "linked commit\n\nEntire-Checkpoint: "+cpID.String()+"\n", "package main\n")
	repo := whyTestOpenRepo(t, repoDir)
	lookup := whyTestCheckpointLookup(repo)

	infoByCommit := enrichWhyCommits(ctx, repo, lookup, []whyBlameBlock{{CommitHash: hash.String()}})
	info, ok := infoByCommit[hash]
	if !ok {
		t.Fatalf("missing info for commit %s", hash)
	}
	if info.CheckpointID != cpID {
		t.Fatalf("checkpoint ID = %q, want %q", info.CheckpointID, cpID)
	}
	if info.Checkpoint.Found {
		t.Fatal("checkpoint should not be marked found")
	}
	if info.Summary != "linked commit" {
		t.Fatalf("summary = %q, want commit subject fallback", info.Summary)
	}
}

func TestWhyEnrichCommits_LocalCheckpointUsesSummaryMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	cpID := id.MustCheckpointID("b1b2c3d4e5f6")
	hash := whyTestCommit(t, repoDir, "linked commit\n\nEntire-Checkpoint: "+cpID.String()+"\n", "package main\n")
	repo := whyTestOpenRepo(t, repoDir)
	whyTestWriteCommittedCheckpoint(ctx, t, repo, cpID, &checkpoint.Summary{
		Intent:  "Implement why details",
		Outcome: "Rendered enriched metadata",
	}, []string{"Build the why detail view\nwith useful metadata"})
	lookup := whyTestCheckpointLookup(repo)

	infoByCommit := enrichWhyCommits(ctx, repo, lookup, []whyBlameBlock{{CommitHash: hash.String()}})
	info, ok := infoByCommit[hash]
	if !ok {
		t.Fatalf("missing info for commit %s", hash)
	}
	if info.CheckpointID != cpID {
		t.Fatalf("checkpoint ID = %q, want %q", info.CheckpointID, cpID)
	}
	if !info.Checkpoint.Found {
		t.Fatal("checkpoint should be marked found")
	}
	if info.Checkpoint.Agent != types.AgentType("Claude Code") {
		t.Fatalf("agent = %q, want Claude Code", info.Checkpoint.Agent)
	}
	if info.Checkpoint.SessionCount != 1 {
		t.Fatalf("session count = %d, want 1", info.Checkpoint.SessionCount)
	}
	if !slices.Equal(info.Checkpoint.FilesTouched, []string{"file.go", "other.go"}) {
		t.Fatalf("files touched = %#v", info.Checkpoint.FilesTouched)
	}
	if !info.SummaryGenerated || !info.Checkpoint.SummaryGenerated {
		t.Fatal("summary should be marked generated")
	}
	if !strings.Contains(info.Summary, "Implement why details") {
		t.Fatalf("summary = %q, want intent", info.Summary)
	}
	if !strings.Contains(info.Summary, "Rendered enriched metadata") {
		t.Fatalf("summary = %q, want outcome", info.Summary)
	}
}

func TestWhyEnrichCommits_LocalCheckpointFallsBackToPrompt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	cpID := id.MustCheckpointID("c1b2c3d4e5f6")
	hash := whyTestCommit(t, repoDir, "linked commit\n\nEntire-Checkpoint: "+cpID.String()+"\n", "package main\n")
	repo := whyTestOpenRepo(t, repoDir)
	whyTestWriteCommittedCheckpoint(ctx, t, repo, cpID, nil, []string{"Prompt fallback line\nsecond line"})
	lookup := whyTestCheckpointLookup(repo)

	infoByCommit := enrichWhyCommits(ctx, repo, lookup, []whyBlameBlock{{CommitHash: hash.String()}})
	info, ok := infoByCommit[hash]
	if !ok {
		t.Fatalf("missing info for commit %s", hash)
	}
	if info.Summary != "Prompt fallback line" {
		t.Fatalf("summary = %q, want prompt first line", info.Summary)
	}
	if info.SummaryGenerated {
		t.Fatal("prompt fallback should not be marked generated")
	}
}

func TestWhyEnrichCommits_CorruptedCheckpointDegradesThatCommitOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	testutil.InitRepo(t, repoDir)
	corruptID := id.MustCheckpointID("d1b2c3d4e5f6")
	validID := id.MustCheckpointID("e1b2c3d4e5f6")
	corruptHash := whyTestCommit(t, repoDir, "corrupt linked commit\n\nEntire-Checkpoint: "+corruptID.String()+"\n", "package main\n")
	validHash := whyTestCommit(t, repoDir, "valid linked commit\n\nEntire-Checkpoint: "+validID.String()+"\n", "package main\nfunc main() {}\n")
	repo := whyTestOpenRepo(t, repoDir)
	whyTestWriteCorruptCommittedCheckpoint(ctx, t, repo, corruptID)
	whyTestWriteCommittedCheckpoint(ctx, t, repo, validID, &checkpoint.Summary{
		Intent:  "Keep valid checkpoint",
		Outcome: "Still readable",
	}, []string{"valid prompt"})
	lookup := whyTestCheckpointLookup(repo)

	infoByCommit := enrichWhyCommits(ctx, repo, lookup, []whyBlameBlock{
		{CommitHash: corruptHash.String()},
		{CommitHash: validHash.String()},
	})
	corruptInfo, ok := infoByCommit[corruptHash]
	if !ok {
		t.Fatalf("missing info for corrupt commit %s", corruptHash)
	}
	if corruptInfo.Checkpoint.Found {
		t.Fatal("corrupt checkpoint should not be marked found")
	}
	if corruptInfo.Summary != "corrupt linked commit" {
		t.Fatalf("corrupt summary = %q, want commit subject fallback", corruptInfo.Summary)
	}

	validInfo, ok := infoByCommit[validHash]
	if !ok {
		t.Fatalf("missing info for valid commit %s", validHash)
	}
	if !validInfo.Checkpoint.Found {
		t.Fatal("valid checkpoint should still be marked found")
	}
	if !strings.Contains(validInfo.Summary, "Keep valid checkpoint") {
		t.Fatalf("valid summary = %q, want generated summary", validInfo.Summary)
	}
}

func whyTestCommit(t *testing.T, repoDir, message, content string) plumbing.Hash {
	t.Helper()

	testutil.WriteFile(t, repoDir, "file.go", content)
	testutil.GitAdd(t, repoDir, "file.go")
	testutil.GitCommit(t, repoDir, message)
	return plumbing.NewHash(testutil.GetHeadHash(t, repoDir))
}

func whyTestOpenRepo(t *testing.T, repoDir string) *git.Repository {
	t.Helper()

	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		t.Fatalf("failed to open repo: %v", err)
	}
	return repo
}

func whyTestCheckpointLookup(repo *git.Repository) *whyCheckpointLookup {
	return &whyCheckpointLookup{
		repo:                repo,
		v1Store:             checkpoint.NewGitStore(repo),
		v2Store:             checkpoint.NewV2GitStore(repo, ""),
		preferCheckpointsV2: false,
	}
}

func whyTestWriteCommittedCheckpoint(
	ctx context.Context,
	t *testing.T,
	repo *git.Repository,
	cpID id.CheckpointID,
	summary *checkpoint.Summary,
	prompts []string,
) {
	t.Helper()

	err := checkpoint.NewGitStore(repo).WriteCommitted(ctx, checkpoint.WriteCommittedOptions{
		CheckpointID:     cpID,
		SessionID:        "session-1",
		Strategy:         "manual-commit",
		Branch:           "main",
		Transcript:       redact.AlreadyRedacted([]byte("{}\n")),
		Prompts:          prompts,
		FilesTouched:     []string{"file.go", "other.go"},
		CheckpointsCount: 2,
		Agent:            types.AgentType("Claude Code"),
		Model:            "test-model",
		Summary:          summary,
	})
	if err != nil {
		t.Fatalf("failed to write checkpoint: %v", err)
	}
}

func whyTestWriteCorruptCommittedCheckpoint(ctx context.Context, t *testing.T, repo *git.Repository, cpID id.CheckpointID) {
	t.Helper()

	blob, err := checkpoint.CreateBlobFromContent(repo, []byte("{not-json"))
	if err != nil {
		t.Fatalf("failed to create corrupt metadata blob: %v", err)
	}
	treeHash, err := checkpoint.BuildTreeFromEntries(ctx, repo, map[string]object.TreeEntry{
		cpID.Path() + "/metadata.json": {
			Name: cpID.Path() + "/metadata.json",
			Mode: filemode.Regular,
			Hash: blob,
		},
	})
	if err != nil {
		t.Fatalf("failed to build corrupt checkpoint tree: %v", err)
	}
	commitHash, err := checkpoint.CreateCommit(ctx, repo, treeHash, plumbing.ZeroHash, "Checkpoint: "+cpID.String(), "Test User", "test@example.com")
	if err != nil {
		t.Fatalf("failed to create corrupt checkpoint commit: %v", err)
	}
	ref := plumbing.NewHashReference(plumbing.NewBranchReferenceName(paths.MetadataBranchName), commitHash)
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatalf("failed to set metadata branch ref: %v", err)
	}
}

func TestWhySummaryFromPrompt(t *testing.T) {
	t.Parallel()

	got, ok := whySummaryFromPrompt("\n\nFirst useful line\nsecond line")
	if !ok {
		t.Fatal("expected prompt summary")
	}
	if got != "First useful line" {
		t.Fatalf("summary = %q, want first useful line", got)
	}
}

func TestWhyCommitSubject(t *testing.T) {
	t.Parallel()

	got := whyCommitSubject("Subject line\n\nBody")
	if got != "Subject line" {
		t.Fatalf("subject = %q, want Subject line", got)
	}
	got = whyCommitSubject("\n\n")
	if got != whyNotGeneratedSummary {
		t.Fatalf("empty subject = %q, want fallback", got)
	}
}

func TestWhyReadCheckpointInfo_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readWhyCheckpointInfo(ctx, whyTestCheckpointLookup(nil), id.MustCheckpointID("f1b2c3d4e5f6"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
