// wingman_lifecycle.go contains wingman integration hooks for the lifecycle dispatcher.
// These functions are called from lifecycle.go event handlers to inject wingman
// review state into session start banners, turn start context, and turn end actions.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
)

// wingmanNotificationLockThreshold is the maximum lock file age for showing
// "Reviewing your changes..." notifications. Much tighter than staleLockThreshold
// (used for lock acquisition) because a real review takes at most
// wingmanInitialDelay (10s) + wingmanReviewTimeout (5m) ≈ 6 minutes.
// A lock older than this is almost certainly stale for notification purposes.
const wingmanNotificationLockThreshold = 10 * time.Minute

// appendWingmanSessionStartStatus appends wingman status to the session start banner.
// Returns the message with wingman status appended.
func appendWingmanSessionStartStatus(message string) string {
	if !settings.IsWingmanEnabled() {
		return message
	}

	repoRoot, rootErr := paths.RepoRoot()
	if rootErr != nil {
		return message + "\n  Wingman is active: your changes will be automatically reviewed."
	}

	reviewPath := filepath.Join(repoRoot, wingmanReviewFile)
	if _, statErr := os.Stat(reviewPath); statErr == nil && reviewHasActionableIssues(reviewPath) {
		return message + "\n  Wingman: a review is pending and will be addressed on your next prompt."
	}
	return message + "\n  Wingman is active: your changes will be automatically reviewed."
}

// handleWingmanTurnStart checks for a pending wingman review and injects it
// as additionalContext so the agent addresses it before the user's request.
// Returns true if a hook response was written to stdout (caller should return early).
func handleWingmanTurnStart(sessionID string) bool {
	if !settings.IsWingmanEnabled() {
		return false
	}

	repoRoot, rootErr := paths.RepoRoot()
	if rootErr != nil {
		return false
	}

	wingmanLogCtx := logging.WithComponent(context.Background(), "wingman")

	// If a review is pending, check if it has actionable issues before injecting
	reviewPath := filepath.Join(repoRoot, wingmanReviewFile)
	if _, statErr := os.Stat(reviewPath); statErr == nil {
		if !reviewHasActionableIssues(reviewPath) {
			// No actionable issues — clean up and notify the user
			fmt.Fprintf(os.Stderr, "[wingman] Review has no actionable issues, cleaning up\n")
			logging.Info(wingmanLogCtx, "wingman review has no actionable issues, skipping injection",
				slog.String("session_id", sessionID),
			)
			_ = os.Remove(reviewPath)
			_ = outputHookMessage("[Wingman] Reviewed your changes — no issues found.") //nolint:errcheck // best-effort notification
		} else {
			fmt.Fprintf(os.Stderr, "[wingman] Review available: .entire/REVIEW.md — injecting into context\n")
			logging.Info(wingmanLogCtx, "wingman injecting review instruction on prompt-submit",
				slog.String("session_id", sessionID),
			)
			if err := outputHookResponseWithContextAndMessage(
				wingmanApplyInstruction,
				"[Wingman] A code review is pending and will be addressed before your request.",
			); err != nil {
				fmt.Fprintf(os.Stderr, "[wingman] Warning: failed to inject review instruction: %v\n", err)
			} else {
				return true // Hook response written to stdout — caller must return
			}
		}
	}

	// Notify if a review is currently in progress (fresh lock file).
	lockPath := filepath.Join(repoRoot, wingmanLockFile)
	if lockInfo, statErr := os.Stat(lockPath); statErr == nil && time.Since(lockInfo.ModTime()) <= wingmanNotificationLockThreshold {
		logging.Info(wingmanLogCtx, "wingman review in progress",
			slog.String("session_id", sessionID),
		)
		if err := outputHookMessage("[Wingman] Review in progress..."); err != nil {
			fmt.Fprintf(os.Stderr, "[wingman] Warning: failed to output review-in-progress message: %v\n", err)
		}
	}

	return false
}

// handleWingmanTurnEnd handles wingman actions at turn end: triggering reviews,
// auto-applying pending reviews, and showing stop notifications.
func handleWingmanTurnEnd(sessionID string, totalChanges int, relModifiedFiles, relNewFiles, relDeletedFiles, allPrompts []string, commitMessage string) {
	repoRoot, err := paths.RepoRoot()
	if err != nil {
		return
	}

	strat := GetStrategy()

	// Trigger wingman review for auto-commit strategy (commit already happened
	// in SaveStep). Manual-commit triggers wingman from the git post-commit hook
	// instead, since the user commits manually.
	if totalChanges > 0 && strat.Name() == strategy.StrategyNameAutoCommit && settings.IsWingmanEnabled() {
		triggerWingmanReview(WingmanPayload{
			SessionID:     sessionID,
			RepoRoot:      repoRoot,
			ModifiedFiles: relModifiedFiles,
			NewFiles:      relNewFiles,
			DeletedFiles:  relDeletedFiles,
			Prompts:       allPrompts,
			CommitMessage: commitMessage,
		})
	}

	// Auto-apply pending wingman review on turn end
	triggerWingmanAutoApplyIfPending(repoRoot)

	outputWingmanStopNotification(repoRoot)
}

// triggerWingmanAutoApplyIfPending checks for a pending REVIEW.md and spawns
// the auto-apply subprocess if conditions are met. Called from the stop hook
// on every turn end (both with-changes and no-changes paths).
//
// When a live session exists, this is a no-op: the prompt-submit injection
// will deliver the review visibly in the user's terminal instead. Background
// auto-apply is only used when no sessions are alive (all ended).
func triggerWingmanAutoApplyIfPending(repoRoot string) {
	logCtx := logging.WithComponent(context.Background(), "wingman")
	if !settings.IsWingmanEnabled() {
		logging.Debug(logCtx, "wingman auto-apply skip: wingman not enabled")
		return
	}
	if os.Getenv("ENTIRE_WINGMAN_APPLY") != "" {
		logging.Debug(logCtx, "wingman auto-apply skip: already in apply subprocess")
		return
	}
	reviewPath := filepath.Join(repoRoot, wingmanReviewFile)
	if _, statErr := os.Stat(reviewPath); statErr != nil {
		logging.Debug(logCtx, "wingman auto-apply skip: no REVIEW.md pending")
		return
	}
	wingmanState := loadWingmanStateDirect(repoRoot)
	if wingmanState != nil && wingmanState.ApplyAttemptedAt != nil {
		logging.Debug(logCtx, "wingman auto-apply already attempted, skipping",
			slog.Time("attempted_at", *wingmanState.ApplyAttemptedAt),
		)
		return
	}
	// Don't spawn background auto-apply if a live session exists.
	// The prompt-submit hook will inject REVIEW.md as additionalContext,
	// which is visible to the user in their terminal.
	if hasAnyLiveSession(repoRoot) {
		logging.Debug(logCtx, "wingman auto-apply deferred: live session will handle via injection")
		fmt.Fprintf(os.Stderr, "[wingman] Review pending — will be injected on next prompt\n")
		return
	}
	fmt.Fprintf(os.Stderr, "[wingman] Pending review found, spawning auto-apply (no live sessions)\n")
	logging.Info(logCtx, "wingman auto-apply spawning (no live sessions)",
		slog.String("review_path", reviewPath),
	)
	spawnDetachedWingmanApply(repoRoot)
}

// reviewHasActionableIssues reads a REVIEW.md and checks if it contains
// actionable issue markers ([CRITICAL], [WARNING], or [SUGGESTION]).
// Returns false if the file can't be read or contains no issues.
func reviewHasActionableIssues(reviewPath string) bool {
	data, err := os.ReadFile(reviewPath) //nolint:gosec // path is from repo-relative constant
	if err != nil {
		return false
	}

	content := string(data)

	// The review format uses "### [SEVERITY] description" for each issue.
	// Check for any of the severity markers in heading context.
	for _, marker := range []string{"[CRITICAL]", "[WARNING]", "[SUGGESTION]"} {
		if strings.Contains(content, marker) {
			return true
		}
	}

	return false
}

// outputWingmanStopNotification outputs a systemMessage notification about
// wingman status at the end of a stop hook. This makes wingman activity visible
// in the agent terminal without injecting context into the agent's conversation.
// Best-effort: status may be stale due to concurrent wingman processes.
func outputWingmanStopNotification(repoRoot string) {
	if !settings.IsWingmanEnabled() {
		return
	}
	if os.Getenv("ENTIRE_WINGMAN_APPLY") != "" {
		return
	}

	lockPath := filepath.Join(repoRoot, wingmanLockFile)
	if info, err := os.Stat(lockPath); err == nil && time.Since(info.ModTime()) <= wingmanNotificationLockThreshold {
		_ = outputHookMessage("[Wingman] Reviewing your changes...") //nolint:errcheck // best-effort notification
		return
	}

	reviewPath := filepath.Join(repoRoot, wingmanReviewFile)
	if _, err := os.Stat(reviewPath); err == nil {
		_ = outputHookMessage("[Wingman] Review pending \u2014 will be addressed on your next prompt") //nolint:errcheck // best-effort notification
		return
	}
}
