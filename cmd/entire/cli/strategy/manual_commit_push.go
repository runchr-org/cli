package strategy

import (
	"context"
	"log/slog"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/perf"

	"github.com/go-git/go-git/v6/plumbing"
)

// PrePush is called by the git pre-push hook before pushing to a remote.
// It pushes:
//   - the legacy refs/heads/entire/checkpoints/v1 branch (unless v1 writes are
//     disabled by checkpoints_version: 2);
//   - refs/entire/checkpoints/v1/main and refs/entire/checkpoints/v1/full
//     together, in a single batched push, when push_sessions is on;
//   - v2 /full/* refs via pushV2Refs when IsPushV2RefsEnabled is true.
//
// If a checkpoint_remote is configured in settings, checkpoint branches/refs
// are pushed to the derived URL instead of the user's push remote.
//
// Configuration options (stored in .entire/settings.json under strategy_options):
//   - push_sessions: false to disable automatic pushing of checkpoints
//   - checkpoint_remote: {"provider": "github", "repo": "org/repo"} to push to a separate repo
//   - push_v2_refs: true to enable pushing v2 /full/* refs (requires checkpoints_v2)
//   - checkpoints_version: 2 to skip the legacy v1 branch entirely and force v2 ref pushes on
func (s *ManualCommitStrategy) PrePush(ctx context.Context, remote string) error {
	// Load settings once for remote resolution and push_sessions check
	ps := resolvePushSettings(ctx, remote)

	if ps.pushDisabled {
		return nil
	}

	var err error
	if settings.CheckpointsVersion(ctx) != 2 {
		_, pushCheckpointsSpan := perf.Start(ctx, "push_checkpoints_branch")
		err = pushBranchIfNeeded(ctx, ps.pushTarget(), paths.MetadataBranchName)
		if err != nil {
			pushCheckpointsSpan.RecordError(err)
		}
		pushCheckpointsSpan.End()
	}

	// Push both v1.1 custom refs in one batched git invocation. Skips any
	// refs that don't exist locally (e.g. v1/full in v2 mode where the
	// legacy branch isn't written).
	_, pushV11Span := perf.Start(ctx, "push_v11_custom_refs")
	pushV11CustomRefs(ctx, ps.pushTarget())
	pushV11Span.End()

	// Push v2 /full/* refs when enabled.
	if settings.IsPushV2RefsEnabled(ctx) {
		_, pushV2Span := perf.Start(ctx, "push_v2_refs")
		pushV2Refs(ctx, ps.pushTarget())
		pushV2Span.End()
	}

	return err
}

// pushV11CustomRefs pushes the v1.1 compact and full refs together in
// one batched call (one git subprocess, one pack negotiation). Missing
// local refs are filtered out silently. Per-ref non-fast-forward
// recovery is delegated to the shared pushV2RefsWithRecovery primitive.
func pushV11CustomRefs(ctx context.Context, target string) {
	repo, err := OpenRepository(ctx)
	if err != nil {
		return // Hook must be silent on failure to open repo.
	}

	candidates := []plumbing.ReferenceName{
		plumbing.ReferenceName(paths.MetadataCompactRefName),
		plumbing.ReferenceName(paths.MetadataFullRefName),
	}
	var refs []plumbing.ReferenceName
	for _, r := range candidates {
		if _, refErr := repo.Reference(r, true); refErr == nil {
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 {
		return
	}

	for _, result := range pushV2RefsWithRecovery(ctx, target, refs) {
		if result.err != nil {
			logging.Warn(ctx, "v1.1 custom ref push failed",
				slog.String("ref", string(result.refName)),
				slog.String("error", result.err.Error()),
			)
		}
	}
}
