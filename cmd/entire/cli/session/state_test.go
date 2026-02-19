package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_NormalizeAfterLoad(t *testing.T) {
	t.Parallel()

	t.Run("migrates_CondensedTranscriptLines", func(t *testing.T) {
		t.Parallel()
		state := &State{
			CondensedTranscriptLines: 150,
		}
		state.NormalizeAfterLoad()
		assert.Equal(t, 150, state.CheckpointTranscriptStart)
		assert.Equal(t, 0, state.CondensedTranscriptLines)
		assert.Equal(t, 0, state.TranscriptLinesAtStart)
	})

	t.Run("no_migration_when_CheckpointTranscriptStart_set", func(t *testing.T) {
		t.Parallel()
		state := &State{
			CheckpointTranscriptStart: 200,
			CondensedTranscriptLines:  150, // old value should be cleared but not override new
		}
		state.NormalizeAfterLoad()
		assert.Equal(t, 200, state.CheckpointTranscriptStart)
		assert.Equal(t, 0, state.CondensedTranscriptLines)
	})

	t.Run("no_migration_when_all_zero", func(t *testing.T) {
		t.Parallel()
		state := &State{}
		state.NormalizeAfterLoad()
		assert.Equal(t, 0, state.CheckpointTranscriptStart)
	})

	t.Run("migrates_TranscriptLinesAtStart", func(t *testing.T) {
		t.Parallel()
		state := &State{
			TranscriptLinesAtStart: 42,
		}
		state.NormalizeAfterLoad()
		assert.Equal(t, 42, state.CheckpointTranscriptStart)
		assert.Equal(t, 0, state.TranscriptLinesAtStart)
	})

	t.Run("CondensedTranscriptLines_takes_precedence_over_TranscriptLinesAtStart", func(t *testing.T) {
		t.Parallel()
		state := &State{
			CondensedTranscriptLines: 150,
			TranscriptLinesAtStart:   42,
		}
		state.NormalizeAfterLoad()
		assert.Equal(t, 150, state.CheckpointTranscriptStart)
		assert.Equal(t, 0, state.CondensedTranscriptLines)
		assert.Equal(t, 0, state.TranscriptLinesAtStart)
	})

	t.Run("CheckpointTranscriptStart_not_overridden_by_TranscriptLinesAtStart", func(t *testing.T) {
		t.Parallel()
		state := &State{
			CheckpointTranscriptStart: 200,
			TranscriptLinesAtStart:    42,
		}
		state.NormalizeAfterLoad()
		assert.Equal(t, 200, state.CheckpointTranscriptStart)
		assert.Equal(t, 0, state.TranscriptLinesAtStart)
	})
}

func TestState_NormalizeAfterLoad_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantCTS  int // CheckpointTranscriptStart
		wantStep int // StepCount
	}{
		{
			name:     "migrates old condensed_transcript_lines",
			json:     `{"session_id":"s1","condensed_transcript_lines":42,"checkpoint_count":5}`,
			wantCTS:  42,
			wantStep: 5,
		},
		{
			name:    "migrates old transcript_lines_at_start",
			json:    `{"session_id":"s1","transcript_lines_at_start":75}`,
			wantCTS: 75,
		},
		{
			name:    "preserves new field over old",
			json:    `{"session_id":"s1","condensed_transcript_lines":10,"checkpoint_transcript_start":50}`,
			wantCTS: 50,
		},
		{
			name:     "handles clean new format",
			json:     `{"session_id":"s1","checkpoint_transcript_start":25,"checkpoint_count":3}`,
			wantCTS:  25,
			wantStep: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state State
			require.NoError(t, json.Unmarshal([]byte(tt.json), &state))
			state.NormalizeAfterLoad()

			assert.Equal(t, tt.wantCTS, state.CheckpointTranscriptStart)
			assert.Equal(t, tt.wantStep, state.StepCount)
			assert.Equal(t, 0, state.CondensedTranscriptLines, "deprecated field should be cleared")
			assert.Equal(t, 0, state.TranscriptLinesAtStart, "deprecated field should be cleared")
		})
	}
}

// initTestRepo creates a temp dir with a git repo and chdirs into it.
// Cannot use t.Parallel() because of t.Chdir.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks (macOS /var -> /private/var)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	_, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	t.Chdir(dir)
	ClearGitCommonDirCache()
	return dir
}

func TestGetGitCommonDir_ReturnsValidPath(t *testing.T) {
	dir := initTestRepo(t)

	commonDir, err := getGitCommonDir()
	require.NoError(t, err)

	// getGitCommonDir returns a relative path from cwd; resolve it to absolute for comparison
	absCommonDir, err := filepath.Abs(commonDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, ".git"), absCommonDir)

	// The path should actually exist
	info, err := os.Stat(commonDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestGetGitCommonDir_CachesResult(t *testing.T) {
	initTestRepo(t)

	// First call populates cache
	first, err := getGitCommonDir()
	require.NoError(t, err)

	// Second call should return the same result (from cache)
	second, err := getGitCommonDir()
	require.NoError(t, err)

	assert.Equal(t, first, second)
}

func TestGetGitCommonDir_ClearCache(t *testing.T) {
	initTestRepo(t)

	// Populate cache
	_, err := getGitCommonDir()
	require.NoError(t, err)

	// Verify cache is populated
	gitCommonDirMu.RLock()
	assert.NotEmpty(t, gitCommonDirCache)
	gitCommonDirMu.RUnlock()

	// Clear and verify
	ClearGitCommonDirCache()

	gitCommonDirMu.RLock()
	assert.Empty(t, gitCommonDirCache)
	assert.Empty(t, gitCommonDirCacheDir)
	gitCommonDirMu.RUnlock()
}

func TestGetGitCommonDir_InvalidatesOnCwdChange(t *testing.T) {
	// Create two separate repos
	dir1 := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir1); err == nil {
		dir1 = resolved
	}
	_, err := git.PlainInit(dir1, false)
	require.NoError(t, err)

	dir2 := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir2); err == nil {
		dir2 = resolved
	}
	_, err = git.PlainInit(dir2, false)
	require.NoError(t, err)

	ClearGitCommonDirCache()

	// Populate cache from dir1
	t.Chdir(dir1)
	first, err := getGitCommonDir()
	require.NoError(t, err)
	absFirst, err := filepath.Abs(first)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir1, ".git"), absFirst)

	// Change to dir2 — cache should miss and resolve to dir2's .git
	t.Chdir(dir2)
	second, err := getGitCommonDir()
	require.NoError(t, err)
	absSecond, err := filepath.Abs(second)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir2, ".git"), absSecond)

	assert.NotEqual(t, absFirst, absSecond)
}

func TestGetGitCommonDir_ErrorOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	ClearGitCommonDirCache()

	_, err := getGitCommonDir()
	assert.Error(t, err)
}
