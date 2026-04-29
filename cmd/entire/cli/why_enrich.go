package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
	"github.com/entireio/cli/perf"

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
	Agents           []types.AgentType
	SessionCount     int
	FilesTouched     []string
	Summary          string
	SummaryGenerated bool
}

type whySessionMetadataAndPromptsReader interface {
	ReadSessionMetadataAndPrompts(ctx context.Context, checkpointID id.CheckpointID, sessionIndex int) (*checkpoint.SessionContent, error)
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

	loopCtx, enrichCommitLoop := perf.StartLoop(ctx, "why_enrich_commit")
	defer enrichCommitLoop.End()

	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return infoByCommit
		}

		hash := plumbing.NewHash(block.CommitHash)
		if _, exists := infoByCommit[hash]; exists {
			continue
		}

		_, iterSpan := enrichCommitLoop.Iteration(loopCtx)

		_, commitObjectSpan := perf.Start(ctx, "why_commit_object")
		commit, err := repo.CommitObject(hash)
		if err != nil {
			commitObjectSpan.RecordError(err)
			commitObjectSpan.End()
			iterSpan.RecordError(err)
			iterSpan.End()
			continue
		}
		commitObjectSpan.End()

		subject := whyCommitSubject(commit.Message)
		info := whyCommitInfo{
			Hash:       hash,
			Subject:    subject,
			Author:     commit.Author.Name,
			AuthorTime: commit.Author.When,
			Summary:    subject,
		}

		_, checkpointTrailerSpan := perf.Start(ctx, "why_checkpoint_trailer")
		if cpID, ok := trailers.ParseCheckpoint(commit.Message); ok {
			checkpointTrailerSpan.End()
			info.CheckpointID = cpID
			_, checkpointInfoSpan := perf.Start(ctx, "why_checkpoint_info")
			if checkpointInfo, readErr := readWhyCheckpointInfo(ctx, lookup, cpID); readErr == nil {
				info.Checkpoint = checkpointInfo
				info.Summary = whyCommitFallbackSummary(checkpointInfo.Summary, subject)
				info.SummaryGenerated = checkpointInfo.SummaryGenerated
			} else {
				checkpointInfoSpan.RecordError(readErr)
			}
			checkpointInfoSpan.End()
		} else {
			checkpointTrailerSpan.End()
		}

		infoByCommit[hash] = info
		iterSpan.End()
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

	_, resolveReaderSpan := perf.Start(ctx, "why_checkpoint_resolve_reader")
	reader, summary, err := checkpoint.ResolveCommittedReaderForCheckpoint(
		ctx,
		cpID,
		lookup.v1Store,
		lookup.v2Store,
		lookup.preferCheckpointsV2,
	)
	if err != nil {
		resolveReaderSpan.RecordError(err)
		resolveReaderSpan.End()
		return whyCheckpointInfo{}, fmt.Errorf("resolve checkpoint reader: %w", err)
	}
	if summary == nil {
		resolveReaderSpan.RecordError(checkpoint.ErrCheckpointNotFound)
		resolveReaderSpan.End()
		return whyCheckpointInfo{}, checkpoint.ErrCheckpointNotFound
	}
	resolveReaderSpan.End()

	var agents []types.AgentType
	_, sessionContentSpan := perf.Start(ctx, "why_checkpoint_read_metadata")
	content, err := readCheckpointSessionMetadataAndPromptsForWhy(ctx, reader, cpID, summary, func(_ int, content *checkpoint.SessionContent) {
		if content == nil {
			return
		}
		agents = appendUniqueWhyAgent(agents, content.Metadata.Agent)
	})
	if err != nil && !errors.Is(err, checkpoint.ErrNoTranscript) {
		sessionContentSpan.RecordError(err)
		sessionContentSpan.End()
		return whyCheckpointInfo{}, err
	}
	sessionContentSpan.End()

	_, buildInfoSpan := perf.Start(ctx, "why_checkpoint_build_info")
	info := whyCheckpointInfo{
		Found:        true,
		Agents:       agents,
		SessionCount: len(summary.Sessions),
		FilesTouched: append([]string(nil), summary.FilesTouched...),
		Summary:      whyNotGeneratedSummary,
	}
	if content == nil {
		buildInfoSpan.End()
		return info, nil
	}

	if content.Metadata.Summary != nil {
		info.Summary = whyGeneratedSummary(content.Metadata.Summary)
		info.SummaryGenerated = true
		buildInfoSpan.End()
		return info, nil
	}
	if promptSummary, ok := whySummaryFromPrompt(content.Prompts); ok {
		info.Summary = promptSummary
	}
	buildInfoSpan.End()
	return info, nil
}

func readCheckpointSessionMetadataAndPromptsForWhy(
	ctx context.Context,
	reader checkpoint.CommittedReader,
	checkpointID id.CheckpointID,
	summary *checkpoint.CheckpointSummary,
	onSession func(sessionIndex int, content *checkpoint.SessionContent),
) (*checkpoint.SessionContent, error) {
	if summary == nil || len(summary.Sessions) == 0 {
		return nil, checkpoint.ErrCheckpointNotFound
	}

	latestIndex := len(summary.Sessions) - 1
	var latestContent *checkpoint.SessionContent
	for sessionIndex := range summary.Sessions {
		content, err := readSessionMetadataAndPromptsForWhy(ctx, reader, checkpointID, sessionIndex)
		if err != nil {
			if sessionIndex == latestIndex {
				return nil, err
			}
			continue
		}
		if onSession != nil {
			onSession(sessionIndex, content)
		}
		if sessionIndex == latestIndex {
			latestContent = content
		}
	}
	return latestContent, nil
}

func readSessionMetadataAndPromptsForWhy(ctx context.Context, reader checkpoint.CommittedReader, checkpointID id.CheckpointID, sessionIndex int) (*checkpoint.SessionContent, error) {
	if metadataReader, ok := reader.(whySessionMetadataAndPromptsReader); ok {
		content, err := metadataReader.ReadSessionMetadataAndPrompts(ctx, checkpointID, sessionIndex)
		if err != nil {
			return nil, fmt.Errorf("reading session %d metadata and prompts: %w", sessionIndex, err)
		}
		return content, nil
	}
	content, err := reader.ReadSessionContent(ctx, checkpointID, sessionIndex)
	if err != nil {
		return nil, fmt.Errorf("reading session %d content: %w", sessionIndex, err)
	}
	return content, nil
}

func appendUniqueWhyAgent(agents []types.AgentType, agent types.AgentType) []types.AgentType {
	if agent == "" {
		return agents
	}
	if slices.Contains(agents, agent) {
		return agents
	}
	return append(agents, agent)
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
