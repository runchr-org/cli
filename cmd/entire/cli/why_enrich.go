package cli

import (
	"context"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
	"github.com/entireio/cli/perf"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

type whyCommitInfo struct {
	Hash         plumbing.Hash
	CheckpointID id.CheckpointID
}

func enrichWhyCommits(ctx context.Context, repo *git.Repository, blocks []whyBlameBlock) map[plumbing.Hash]whyCommitInfo {
	infoByCommit := make(map[plumbing.Hash]whyCommitInfo)
	if repo == nil {
		return infoByCommit
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

		info := whyCommitInfo{Hash: hash}
		_, checkpointTrailerSpan := perf.Start(ctx, "why_checkpoint_trailer")
		if cpID, ok := trailers.ParseCheckpoint(commit.Message); ok {
			info.CheckpointID = cpID
		}
		checkpointTrailerSpan.End()

		infoByCommit[hash] = info
		iterSpan.End()
	}

	return infoByCommit
}
