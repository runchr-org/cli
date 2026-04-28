//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/benchutil"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// BenchmarkHookOpenCodeTurnEnd measures the end-to-end wall-clock latency
// of `entire hooks opencode turn-end` as a function of the number of
// modified files in the working tree.
//
// This benchmark validates the diagnosis for issue #952 (Opencode - Buggy
// conversation steps): the OpenCode plugin invokes turn-end via
// Bun.spawnSync, so the OpenCode JS event loop is parked for the full
// duration of this hook. The reporter's logs showed turn-end taking
// 3-4 minutes against working trees with 100+ modified files; that wall
// time is exactly what this benchmark captures.
//
// Run:
//
//	go test -tags=integration -bench=BenchmarkHookOpenCodeTurnEnd \
//	  -benchtime=1x -run='^$' -timeout=15m \
//	  ./cmd/entire/cli/integration_test/...
//
// `-benchtime=1x` is appropriate because each iteration creates a
// shadow-branch commit with potentially hundreds of files; multiple
// iterations would compound state and skew the second-and-later samples.
//
// Reading the output: the "ms/op" metric is the wall time of one
// turn-end invocation. Anything north of ~500ms is already a perceptible
// hitch in the OpenCode TUI; the multi-second numbers at higher file
// counts are what the reporter saw as "step end hanging."
func BenchmarkHookOpenCodeTurnEnd(b *testing.B) {
	b.Run("Files", benchOpenCodeTurnEndFiles)
}

// benchOpenCodeTurnEndFiles scales the count of modified files in the
// working tree at the moment turn-end fires. Each scenario builds a
// fresh repo so iterations don't compound shadow-branch state.
func benchOpenCodeTurnEndFiles(b *testing.B) {
	for _, n := range []int{1, 10, 50, 100, 200} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				repo, sessionID := setupOpenCodeTurnEndScenario(b, n)
				b.StartTimer()

				start := time.Now()
				runOpenCodeTurnEndHook(b, repo.Dir, sessionID)
				b.ReportMetric(float64(time.Since(start).Milliseconds()), "ms/op")
			}
		})
	}
}

// setupOpenCodeTurnEndScenario builds a fresh repo with `dirtyFiles`
// modified-but-uncommitted files, an active OpenCode session state,
// and a pre-written mock OpenCode export transcript. This mirrors the
// state of the working tree at the instant OpenCode's session.status
// transitions to "idle" (i.e., right before the plugin fires turn-end).
func setupOpenCodeTurnEndScenario(b *testing.B, dirtyFiles int) (*benchutil.BenchRepo, string) {
	b.Helper()

	// Repo with enough committed files to cover the dirty-file count.
	// Each subsequent dirty modification rewrites a tracked file so
	// `git status` sees a clean "modified" entry rather than untracked.
	repo := benchutil.NewBenchRepo(b, benchutil.RepoOpts{
		FileCount:     dirtyFiles + 1,
		FeatureBranch: "feature/bench-opencode",
	})

	// Active OpenCode session state — what session-start would have
	// written. The lifecycle handler reads this to know which session
	// the turn-end belongs to.
	sessionID := repo.CreateSessionState(b, benchutil.SessionOpts{
		Phase:     session.PhaseActive,
		AgentType: agent.AgentTypeOpenCode,
	})

	// Mock the `opencode export` output at the path
	// fetchAndCacheExport reads when ENTIRE_TEST_OPENCODE_MOCK_EXPORT=1
	// is set: .entire/tmp/<session-id>.json. Minimal valid OpenCode
	// export shape — the realistic transcript-size dimension is left
	// for a future sub-benchmark.
	tmpDir := filepath.Join(repo.Dir, ".entire", "tmp")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		b.Fatalf("mkdir .entire/tmp: %v", err)
	}
	transcript := map[string]any{
		"info": map[string]any{"id": sessionID},
		"messages": []map[string]any{
			{
				"info": map[string]any{
					"id":   "msg-1",
					"role": "user",
					"time": map[string]any{"created": 1708300000},
				},
				"parts": []map[string]any{{"type": "text", "text": "bench"}},
			},
		},
	}
	transcriptBytes, err := json.Marshal(transcript)
	if err != nil {
		b.Fatalf("marshal transcript: %v", err)
	}
	transcriptPath := filepath.Join(tmpDir, sessionID+".json")
	if err := os.WriteFile(transcriptPath, transcriptBytes, 0o600); err != nil {
		b.Fatalf("write transcript: %v", err)
	}

	// Dirty the working tree: rewrite N tracked files. These show up
	// as "modified" in git status, which is what SaveStep walks during
	// the timed window.
	for i := range dirtyFiles {
		path := fmt.Sprintf("src/file_%03d.go", i)
		repo.WriteFile(b, path, benchutil.GenerateGoFile(i+1_000_000, 100))
	}

	return repo, sessionID
}

// runOpenCodeTurnEndHook invokes the turn-end hook as a subprocess.
// Failure here is a benchmark failure: a hook that errored out wouldn't
// exercise the slow code path we want to measure.
func runOpenCodeTurnEndHook(b *testing.B, repoDir, sessionID string) {
	b.Helper()

	stdinPayload, err := json.Marshal(map[string]string{
		"session_id": sessionID,
	})
	if err != nil {
		b.Fatalf("marshal stdin: %v", err)
	}

	cmd := exec.Command(getTestBinary(), "hooks", "opencode", "turn-end")
	cmd.Dir = repoDir
	cmd.Stdin = bytes.NewReader(stdinPayload)
	cmd.Env = append(testutil.GitIsolatedEnv(),
		"ENTIRE_TEST_OPENCODE_MOCK_EXPORT=1",
		// Keep `opencode export` from being shelled out even if the
		// test env happens to have it installed.
		"ENTIRE_TEST_OPENCODE_PROJECT_DIR="+b.TempDir(),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		b.Fatalf("turn-end hook failed: %v\nOutput: %s", err, output)
	}
}
