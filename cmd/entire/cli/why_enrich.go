package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/trailers"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

const whyNotGeneratedSummary = "(not generated)"

type whyCheckpointLookup struct {
	repo                *git.Repository
	v1Store             *checkpoint.GitStore
	v2Store             *checkpoint.V2GitStore
	preferCheckpointsV2 bool
}

type whyCommitInfo struct {
	Hash             plumbing.Hash
	Subject          string
	Author           string
	AuthorTime       time.Time
	CheckpointID     id.CheckpointID
	Checkpoint       whyCheckpointInfo
	Summary          string
	SummaryGenerated bool
}

type whyCheckpointInfo struct {
	Found            bool
	Agent            types.AgentType
	SessionCount     int
	FilesTouched     []string
	Summary          string
	SummaryGenerated bool
}

func newWhyCheckpointLookup(ctx context.Context, repo *git.Repository) (*whyCheckpointLookup, error) {
	if repo == nil {
		var err error
		repo, err = openRepository(ctx)
		if err != nil {
			return nil, fmt.Errorf("not a git repository: %w", err)
		}
	}

	return &whyCheckpointLookup{
		repo:                repo,
		v1Store:             checkpoint.NewGitStore(repo),
		v2Store:             checkpoint.NewV2GitStore(repo, ""),
		preferCheckpointsV2: settings.IsCheckpointsV2Enabled(ctx),
	}, nil
}

func enrichWhyCommits(ctx context.Context, repo *git.Repository, lookup *whyCheckpointLookup, blocks []whyBlameBlock) map[plumbing.Hash]whyCommitInfo {
	infoByCommit := make(map[plumbing.Hash]whyCommitInfo)
	if repo == nil {
		return infoByCommit
	}
	if lookup == nil {
		var err error
		lookup, err = newWhyCheckpointLookup(ctx, repo)
		if err != nil {
			lookup = nil
		}
	}

	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return infoByCommit
		}

		hash := plumbing.NewHash(block.CommitHash)
		if _, exists := infoByCommit[hash]; exists {
			continue
		}

		commit, err := repo.CommitObject(hash)
		if err != nil {
			continue
		}

		subject := whyCommitSubject(commit.Message)
		info := whyCommitInfo{
			Hash:       hash,
			Subject:    subject,
			Author:     commit.Author.Name,
			AuthorTime: commit.Author.When,
			Summary:    subject,
		}

		if cpID, ok := trailers.ParseCheckpoint(commit.Message); ok {
			info.CheckpointID = cpID
			if checkpointInfo, readErr := readWhyCheckpointInfo(ctx, lookup, cpID); readErr == nil {
				info.Checkpoint = checkpointInfo
				info.Summary = whyCommitFallbackSummary(checkpointInfo.Summary, subject)
				info.SummaryGenerated = checkpointInfo.SummaryGenerated
			}
		}

		infoByCommit[hash] = info
	}

	return infoByCommit
}

func readWhyCheckpointInfo(ctx context.Context, lookup *whyCheckpointLookup, cpID id.CheckpointID) (whyCheckpointInfo, error) {
	if err := ctx.Err(); err != nil {
		return whyCheckpointInfo{}, err //nolint:wrapcheck // Propagating context cancellation
	}
	if lookup == nil {
		return whyCheckpointInfo{}, checkpoint.ErrCheckpointNotFound
	}

	reader, summary, err := checkpoint.ResolveCommittedReaderForCheckpoint(
		ctx,
		cpID,
		lookup.v1Store,
		lookup.v2Store,
		lookup.preferCheckpointsV2,
	)
	if err != nil {
		return whyCheckpointInfo{}, fmt.Errorf("resolve checkpoint reader: %w", err)
	}
	if summary == nil {
		return whyCheckpointInfo{}, checkpoint.ErrCheckpointNotFound
	}

	content, err := readLatestSessionContentForExplain(ctx, reader, cpID, summary)
	if err != nil && !errors.Is(err, checkpoint.ErrNoTranscript) {
		return whyCheckpointInfo{}, err
	}

	info := whyCheckpointInfo{
		Found:        true,
		SessionCount: len(summary.Sessions),
		FilesTouched: append([]string(nil), summary.FilesTouched...),
		Summary:      whyNotGeneratedSummary,
	}
	if content == nil {
		return info, nil
	}

	info.Agent = content.Metadata.Agent
	if content.Metadata.Summary != nil {
		info.Summary = whyGeneratedSummary(content.Metadata.Summary)
		info.SummaryGenerated = true
		return info, nil
	}
	if promptSummary, ok := whySummaryFromPrompt(content.Prompts); ok {
		info.Summary = promptSummary
	}
	return info, nil
}

func whyCommitSubject(message string) string {
	for line := range strings.SplitSeq(message, "\n") {
		subject := strings.TrimSpace(line)
		if subject != "" {
			return subject
		}
	}
	return whyNotGeneratedSummary
}

func whyGeneratedSummary(summary *checkpoint.Summary) string {
	if summary == nil {
		return whyNotGeneratedSummary
	}

	parts := make([]string, 0, 2)
	if intent := strings.TrimSpace(summary.Intent); intent != "" {
		parts = append(parts, intent)
	}
	if outcome := strings.TrimSpace(summary.Outcome); outcome != "" {
		parts = append(parts, outcome)
	}
	if len(parts) == 0 {
		return whyNotGeneratedSummary
	}
	return strategy.TruncateDescription(strings.Join(parts, " - "), maxIntentDisplayLength)
}

func whySummaryFromPrompt(prompt string) (string, bool) {
	for line := range strings.SplitSeq(prompt, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return strategy.TruncateDescription(line, maxIntentDisplayLength), true
		}
	}
	return "", false
}

func whyCommitFallbackSummary(summary, subject string) string {
	if strings.TrimSpace(summary) != "" && summary != whyNotGeneratedSummary {
		return summary
	}
	return subject
}
