package strategy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// (1) When the underlying git fetch reports an error, the user-visible
// "fetch failed: …" message must include some non-empty detail. Previously,
// when git's stdout was empty (e.g. SIGKILLed helper), the message ended
// with a bare colon and the user had no idea what failed.
//
// Not parallel: t.Chdir() for OpenRepository.
func TestFetchAndRebase_FetchFailure_IncludesUnderlyingError(t *testing.T) {
	tmpDir := setupRepoWithCheckpointBranch(t)
	t.Chdir(tmpDir)

	// Point at a path that does not exist. git fetch will fail; output is
	// typically non-empty but the test passes either way because we now
	// always include some detail.
	nonExistent := filepath.Join(t.TempDir(), "does-not-exist")
	err := fetchAndRebaseSessionsCommon(context.Background(), nonExistent, paths.MetadataBranchName)

	require.Error(t, err)
	msg := err.Error()
	require.Greater(t, len(msg), len("fetch failed: "), "error message must include detail beyond 'fetch failed: '; got %q", msg)
}

// (2) When tryPushSessionsCommon hits its own 2-minute deadline, doPushBranch
// must end the dot line with " timed out" (not silently). And (4): it must
// also skip the sync→retry cascade, since the same network condition would
// trip the fetch timeout too.
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

// (4b) If the OUTER context is already cancelled when doPushBranch runs (the
// user hit Ctrl-C, or the hook deadline fired), we must bail without printing
// a misleading sync cascade or warnings.
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

// (3) Push attempts must be logged at INFO so doctor bundles capture
// push history even when log_level=INFO (the default). Without this, a
// bundle from a stuck push reveals nothing about what was attempted.
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

// (2/4) Companion test for the lower-level helper: tryPushSessionsCommon
// returns errPushTimedOut when its inner deadline fires before the outer
// context. Pinning this so future refactors of the cascade logic can rely
// on the sentinel.
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
		{"push timeout", errPushTimedOut, "timed_out"},
		{"fetch timeout", errFetchTimedOut, "timed_out"},
		{"non-fast-forward", errNonFastForward, "non_fast_forward"},
		{"protected", &protectedRefError{output: "GH013"}, "protected_ref"},
		{"other", errors.New("some random error"), "other"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, classifyForLog(tc.err))
		})
	}
}
