package strategy

import (
	"context"
	"errors"
	"fmt"
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
//   - refs/entire/checkpoints/v1/main (the v1.1 compact ref), always when
//     push_sessions is on;
//   - refs/entire/checkpoints/v1/full (the v1.1 full-tree alias of the legacy
//     branch), always when push_sessions is on;
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

	// Always push the v1.1 compact ref (refs/entire/checkpoints/v1/main).
	_, pushCompactSpan := perf.Start(ctx, "push_compact_ref")
	if pushErr := pushCustomRefWithRecovery(ctx, ps.pushTarget(),
		plumbing.ReferenceName(paths.MetadataCompactRefName)); pushErr != nil {
		pushCompactSpan.RecordError(pushErr)
		logging.Warn(ctx, "compact-ref push failed",
			slog.String("error", pushErr.Error()),
		)
	}
	pushCompactSpan.End()

	// Always push the v1.1 full ref (refs/entire/checkpoints/v1/full) when
	// it exists. Skipped silently when the ref is missing — that's the
	// expected state in v2 mode where the legacy branch isn't written.
	_, pushFullSpan := perf.Start(ctx, "push_full_ref")
	if pushErr := pushCustomRefWithRecovery(ctx, ps.pushTarget(),
		plumbing.ReferenceName(paths.MetadataFullRefName)); pushErr != nil {
		pushFullSpan.RecordError(pushErr)
		logging.Warn(ctx, "full-ref push failed",
			slog.String("error", pushErr.Error()),
		)
	}
	pushFullSpan.End()

	// Push v2 /full/* refs when enabled.
	if settings.IsPushV2RefsEnabled(ctx) {
		_, pushV2Span := perf.Start(ctx, "push_v2_refs")
		pushV2Refs(ctx, ps.pushTarget())
		pushV2Span.End()
	}

	return err
}

// pushCustomRefWithRecovery attempts a single-ref push and, on
// non-fast-forward, fetches the remote ref + merges it locally before
// retrying. Used for refs/entire/checkpoints/v1/main and
// refs/entire/checkpoints/v1/full.
//
// Skips silently when the ref does not exist locally — callers do not
// need to gate on existence.
func pushCustomRefWithRecovery(ctx context.Context, target string, refName plumbing.ReferenceName) error {
	repo, openErr := OpenRepository(ctx)
	if openErr != nil {
		return nil //nolint:nilerr // Hook must be silent on failure to open repo
	}
	if _, refErr := repo.Reference(refName, true); refErr != nil {
		// Local ref doesn't exist — nothing to push.
		return nil //nolint:nilerr // Expected when v1.1 ref hasn't been written yet
	}

	if pushErr := tryPushRef(ctx, target, refName); pushErr == nil {
		return nil
	} else if !errors.Is(pushErr, errNonFastForward) {
		return pushErr
	}
	if err := fetchAndMergeRef(ctx, target, refName); err != nil {
		return fmt.Errorf("couldn't sync %s: %w", refName, err)
	}
	if err := tryPushRef(ctx, target, refName); err != nil {
		return fmt.Errorf("failed to push %s after sync: %w", refName, err)
	}
	return nil
}
