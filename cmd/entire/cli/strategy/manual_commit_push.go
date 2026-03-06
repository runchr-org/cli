package strategy

import (
	"context"
	"log/slog"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/perf"
)

// PrePush is called by the git pre-push hook before pushing to a remote.
// It pushes the entire/checkpoints/v1 branch alongside the user's push.
// Configuration options (stored in .entire/settings.json under strategy_options.push_sessions):
//   - "auto": always push automatically
//   - "prompt" (default): ask user with option to enable auto
//   - "false"/"off"/"no": never push
func (s *ManualCommitStrategy) PrePush(ctx context.Context, remote string) error {
	_, pushCheckpointsSpan := perf.Start(ctx, "push_checkpoints_branch")
	if err := pushSessionsBranchCommon(ctx, remote, paths.MetadataBranchName); err != nil {
		pushCheckpointsSpan.RecordError(err)
		pushCheckpointsSpan.End()
		return err
	}
	pushCheckpointsSpan.End()

	// Only mark sessions if pushing was actually enabled and attempted.
	// pushSessionsBranchCommon returns nil without pushing when disabled.
	if !isPushSessionsDisabled(ctx) {
		markActiveSessionsPushed(ctx, remote)
	}

	_, pushTrailsSpan := perf.Start(ctx, "push_trails_branch")
	err := PushTrailsBranch(ctx, remote)
	pushTrailsSpan.RecordError(err)
	pushTrailsSpan.End()
	return err
}

// markActiveSessionsPushed sets PushedDuringTurnRemote on all ACTIVE sessions
// that have TurnCheckpointIDs. This records that provisional checkpoints were
// pushed to remote, so HandleTurnEnd knows to push finalized transcripts.
//
// Best-effort: errors are logged but don't fail the push.
func markActiveSessionsPushed(ctx context.Context, remote string) {
	logCtx := logging.WithComponent(ctx, "push")

	states, err := ListSessionStates(ctx)
	if err != nil {
		logging.Warn(logCtx, "failed to list session states for push marking",
			slog.Any("error", err),
		)
		return
	}

	for _, state := range states {
		if !state.Phase.IsActive() || len(state.TurnCheckpointIDs) == 0 {
			continue
		}

		state.PushedDuringTurnRemote = remote
		if err := SaveSessionState(ctx, state); err != nil {
			logging.Warn(logCtx, "failed to save pushed-during-turn flag",
				slog.String("session_id", state.SessionID),
				slog.Any("error", err),
			)
		}
	}
}
