package strategy

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/perf"
	"github.com/entireio/cli/redact"
)

// errOPFAbortedByUser is returned when the user chose Abort (or pressed
// Ctrl-C) at the OPF prompt. PrePush returns it verbatim; the hook
// command propagates the non-zero exit code so git push aborts.
var errOPFAbortedByUser = errors.New("OPF prompt aborted by user; push cancelled")

// PrePush is called by the git pre-push hook before pushing to a remote.
// It pushes the entire/checkpoints/v1 branch alongside the user's push (unless
// v1 writes are disabled by checkpoints_version: 2), and pushes v2 refs whenever
// IsPushV2RefsEnabled is true — i.e. either checkpoints_v2 + push_v2_refs, or
// checkpoints_version: 2.
//
// If a checkpoint_remote is configured in settings, checkpoint branches/refs
// are pushed to the derived URL instead of the user's push remote.
//
// Configuration options (stored in .entire/settings.json under strategy_options):
//   - push_sessions: false to disable automatic pushing of checkpoints
//   - checkpoint_remote: {"provider": "github", "repo": "org/repo"} to push to a separate repo
//   - push_v2_refs: true to enable pushing v2 refs (requires checkpoints_v2)
//   - checkpoints_version: 2 to skip the v1 metadata branch entirely and force v2 ref pushes on
func (s *ManualCommitStrategy) PrePush(ctx context.Context, remote string) error {
	// Load settings once for remote resolution and push_sessions check
	ps := resolvePushSettings(ctx, remote)

	if ps.pushDisabled {
		return nil
	}

	if settings.CheckpointsVersion(ctx) != 2 {
		// OPF pre-push rewrite: if OPF is configured, resolve the
		// user's decision (env > settings > prompt > non-TTY auto-run),
		// then re-redact unpushed v1 commits with the 8-layer pipeline
		// before pushing. Skipped entirely when OPF is off, so the
		// common-case fast path is unchanged.
		if redact.OPFEnabled() {
			s, _ := settings.Load(ctx) //nolint:errcheck // Load already failed at hook init; fall back to nil
			var opfCfg *settings.OPFSettings
			if s != nil && s.Redaction != nil {
				opfCfg = s.Redaction.OpenAIPrivacyFilter
			}
			decision, decisionErr := resolveOPFDecisionForPrePush(ctx, opfCfg, os.Stderr)
			if decisionErr != nil {
				logging.Warn(ctx, "OPF pre-push decision failed; aborting push",
					slog.String("error", decisionErr.Error()),
				)
				return decisionErr
			}
			switch decision {
			case OPFAbort:
				return errOPFAbortedByUser
			case OPFSkip:
				// User opted out for this push (or settings/env say
				// "never"). Push 7-layer content as-is.
				logging.Info(ctx, "OPF skipped for this push (user choice or settings)")
			case OPFRun:
				_, opfSpan := perf.Start(ctx, "opf_pre_push_rewrite")
				repo, repoErr := OpenRepository(ctx)
				if repoErr != nil {
					opfSpan.RecordError(repoErr)
					opfSpan.End()
					logging.Warn(ctx, "OPF pre-push: failed to open repo; aborting push",
						slog.String("error", repoErr.Error()),
					)
					return repoErr
				}
				if _, rewriteErr := RewriteUnpushedV1WithOPF(ctx, repo, ps.pushTarget()); rewriteErr != nil {
					opfSpan.RecordError(rewriteErr)
					opfSpan.End()
					logging.Warn(ctx, "OPF pre-push rewrite failed; aborting push",
						slog.String("error", rewriteErr.Error()),
					)
					return rewriteErr
				}
				opfSpan.End()
			}
		}

		// Push the checkpoint branch. This is best-effort — failures here
		// are logged but NOT propagated, so a transient checkpoint-push
		// problem doesn't break the user's git push of their actual work.
		// (OPF failures above are the exception — they're privacy-critical.)
		_, pushCheckpointsSpan := perf.Start(ctx, "push_checkpoints_branch")
		if pushErr := pushBranchIfNeeded(ctx, ps.pushTarget(), paths.MetadataBranchName); pushErr != nil {
			pushCheckpointsSpan.RecordError(pushErr)
			logging.Warn(ctx, "checkpoint branch push failed; user push continues",
				slog.String("error", pushErr.Error()),
			)
		}
		pushCheckpointsSpan.End()
	}

	// Push v2 refs when enabled.
	if settings.IsPushV2RefsEnabled(ctx) {
		_, pushV2Span := perf.Start(ctx, "push_v2_refs")
		pushV2Refs(ctx, ps.pushTarget())
		pushV2Span.End()
	}

	return nil
}
