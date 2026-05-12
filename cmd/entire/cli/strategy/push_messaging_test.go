package strategy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withShortPushTimeout replaces pushAttemptTimeout for the test duration and
// restores it via t.Cleanup. Lets us trigger errPushTimedOut without waiting
// the production 2-minute deadline.
func withShortPushTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	original := pushAttemptTimeout
	pushAttemptTimeout = d
	t.Cleanup(func() { pushAttemptTimeout = original })
}

// Regression: a colleague's terminal once showed "Warning: couldn't sync …:
// fetch failed:" with nothing after the colon, because the wrapping discarded
// git's underlying error whenever git produced no stdout. The wrapped error
// must now appear in the message even when output is empty.
//
// Not parallel: t.Chdir() for OpenRepository.
func TestFetchAndRebase_FetchFailure_IncludesUnderlyingError(t *testing.T) {
	tmpDir := setupRepoWithCheckpointBranch(t)
	t.Chdir(tmpDir)

	nonExistent := filepath.Join(t.TempDir(), "does-not-exist")
	err := fetchAndRebaseSessionsCommon(context.Background(), nonExistent, paths.MetadataBranchName)

	require.Error(t, err)
	msg := err.Error()
	require.True(t, strings.HasPrefix(msg, "fetch failed: "), "expected fetch failed prefix, got %q", msg)
	detail := strings.TrimPrefix(msg, "fetch failed: ")
	require.NotEmpty(t, strings.TrimSpace(detail), "fetch failed must carry a non-empty cause; got %q", msg)
}

// On inner-push timeout, doPushBranch must end the dot line with " timed out",
// skip the sync→retry cascade (same network condition would also trip the
// fetch deadline), and surface the DEBUG-logging hint.
//
// Not parallel: t.Chdir() and stderr capture.
func TestDoPushBranch_TimedOut_PrintsTimedOutAndSkipsSync(t *testing.T) {
	workDir, bareDir := setupBareRemoteWithCheckpointBranch(t)
	t.Chdir(workDir)

	// Force every push attempt to time out immediately. KillOnCancel +
	// WaitDelay ensures the call returns within milliseconds.
	withShortPushTimeout(t, 1*time.Millisecond)

	restore := captureStderr(t)
	err := doPushBranch(context.Background(), bareDir, paths.MetadataBranchName)
	output := restore()

	require.NoError(t, err, "doPushBranch should swallow the error (graceful degradation)")
	assert.Contains(t, output, "timed out", "dot line must end with 'timed out'")
	assert.NotContains(t, output, "Syncing", "must not cascade into sync on inner-push timeout")
	assert.Contains(t, output, "log_level", "should hint at enabling DEBUG logging")
}

// When the outer context is already cancelled (user Ctrl-C, hook deadline),
// doPushBranch must bail without a misleading sync cascade or warning.
//
// Not parallel: t.Chdir() and stderr capture.
func TestDoPushBranch_OuterContextCancelled_BailsQuietly(t *testing.T) {
	workDir, bareDir := setupBareRemoteWithCheckpointBranch(t)
	t.Chdir(workDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	restore := captureStderr(t)
	err := doPushBranch(ctx, bareDir, paths.MetadataBranchName)
	output := restore()

	require.NoError(t, err)
	assert.NotContains(t, output, "Syncing", "must not run sync after outer cancel")
	assert.NotContains(t, output, "Warning:", "must not print sync/retry warnings after outer cancel")
}

// Push attempts must reach INFO so doctor bundles capture push history at the
// default log level. Without this, a bundle from a stuck push reveals nothing
// about what was attempted.
//
// Not parallel: t.Chdir() and global logger state.
func TestDoPushBranch_LogsAttemptsAtInfo(t *testing.T) {
	workDir, bareDir := setupBareRemoteWithCheckpointBranch(t)
	t.Chdir(workDir)

	require.NoError(t, logging.Init(context.Background(), "2026-05-12-push-log-test"))
	t.Cleanup(logging.Close)

	restore := captureStderr(t)
	err := doPushBranch(context.Background(), bareDir, paths.MetadataBranchName)
	_ = restore()
	require.NoError(t, err)

	logging.Close() // flush

	logBytes, err := os.ReadFile(filepath.Join(workDir, ".entire", "logs", "entire.log"))
	require.NoError(t, err)
	logText := string(logBytes)

	assert.Contains(t, logText, "push attempt start", "log must record push start")
	assert.Contains(t, logText, "push attempt completed", "log must record push completion")
	assert.Contains(t, logText, `"branch":"`+paths.MetadataBranchName+`"`, "log must include branch")
}

// Pins the errPushTimedOut sentinel so future refactors of doPushBranch's
// cascade logic can keep relying on errors.Is(err, errPushTimedOut).
//
// Not parallel: t.Chdir().
func TestTryPushSessionsCommon_InnerTimeout_ReturnsErrPushTimedOut(t *testing.T) {
	workDir, bareDir := setupBareRemoteWithCheckpointBranch(t)
	t.Chdir(workDir)

	withShortPushTimeout(t, 1*time.Millisecond)

	_, err := tryPushSessionsCommon(context.Background(), bareDir, paths.MetadataBranchName)
	require.Error(t, err)
	assert.ErrorIs(t, err, errPushTimedOut,
		"err must wrap errPushTimedOut; got %v (class=%q)", err, classifyForLog(err))
}

// classifyForLog must keep logs free of raw push output (which can contain
// URLs and tokens). Anything we don't recognize collapses to "other".
func TestClassifyForLog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"push timeout", errPushTimedOut, pushClassTimedOut},
		{"fetch timeout", errFetchTimedOut, pushClassTimedOut},
		{"non-fast-forward", errNonFastForward, pushClassNonFastForward},
		{"protected", &protectedRefError{output: "GH013"}, pushClassProtectedRef},
		{"other", errors.New("some random error"), pushClassOther},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, classifyForLog(tc.err))
		})
	}
}
