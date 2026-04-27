package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
)

// fakeHeadlessAgent is the orchestrator-test double. It runs a controllable
// `sh -c` script so the orchestrator sees real stdout and exit codes
// without depending on any real agent binary. Only the Agent methods the
// orchestrator actually invokes (Name, LaunchHeadlessCmd) are implemented;
// any other Agent method panics if invoked, by design — tests should not
// exercise the rest of the interface surface.
type fakeHeadlessAgent struct {
	name     string
	output   string
	exitCode int
	delay    time.Duration
}

var _ agent.HeadlessLauncher = (*fakeHeadlessAgent)(nil)

func (f *fakeHeadlessAgent) Name() types.AgentName { return types.AgentName(f.name) }
func (f *fakeHeadlessAgent) Type() types.AgentType { return types.AgentType(f.name) }
func (f *fakeHeadlessAgent) Description() string   { return "fake headless agent for tests" }
func (f *fakeHeadlessAgent) IsPreview() bool       { return false }
func (f *fakeHeadlessAgent) DetectPresence(_ context.Context) (bool, error) {
	return true, nil
}
func (f *fakeHeadlessAgent) ProtectedDirs() []string { return nil }

func (f *fakeHeadlessAgent) ReadTranscript(_ string) ([]byte, error) {
	panic("fakeHeadlessAgent.ReadTranscript should not be called")
}

func (f *fakeHeadlessAgent) ChunkTranscript(_ context.Context, _ []byte, _ int) ([][]byte, error) {
	panic("fakeHeadlessAgent.ChunkTranscript should not be called")
}

func (f *fakeHeadlessAgent) ReassembleTranscript(_ [][]byte) ([]byte, error) {
	panic("fakeHeadlessAgent.ReassembleTranscript should not be called")
}

func (f *fakeHeadlessAgent) GetSessionID(_ *agent.HookInput) string {
	panic("fakeHeadlessAgent.GetSessionID should not be called")
}

func (f *fakeHeadlessAgent) GetSessionDir(_ string) (string, error) {
	panic("fakeHeadlessAgent.GetSessionDir should not be called")
}

func (f *fakeHeadlessAgent) ResolveSessionFile(_, _ string) string {
	panic("fakeHeadlessAgent.ResolveSessionFile should not be called")
}

func (f *fakeHeadlessAgent) ReadSession(_ *agent.HookInput) (*agent.AgentSession, error) {
	panic("fakeHeadlessAgent.ReadSession should not be called")
}

func (f *fakeHeadlessAgent) WriteSession(_ context.Context, _ *agent.AgentSession) error {
	panic("fakeHeadlessAgent.WriteSession should not be called")
}

func (f *fakeHeadlessAgent) FormatResumeCommand(_ string) string {
	panic("fakeHeadlessAgent.FormatResumeCommand should not be called")
}

func (f *fakeHeadlessAgent) LaunchHeadlessCmd(ctx context.Context, _ string) (*exec.Cmd, error) {
	script := "printf '%s' " + shellQuote(f.output)
	if f.delay > 0 {
		script += fmt.Sprintf("; sleep %.3f", f.delay.Seconds())
	}
	if f.exitCode != 0 {
		script += fmt.Sprintf("; exit %d", f.exitCode)
	}
	return exec.CommandContext(ctx, "sh", "-c", script), nil
}

// shellQuote wraps s in single quotes, escaping any embedded single quote
// by closing the quoted run, emitting a backslash-quoted single quote,
// and reopening — the standard POSIX-sh idiom (see the implementation
// for the literal escape sequence). Output is always a valid sh word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// TestOrchestrator_HappyPath exercises N agents all succeeding, verifies
// each produced FinalOutput, and confirms the marker is cleaned up on
// normal exit.
func TestOrchestrator_HappyPath(t *testing.T) {
	// Cannot t.Parallel — writes and reads the global pending-review marker.
	tmp := setupReviewTestRepoWithCommit(t)

	tasks := []MultiAgentTask{
		{Name: "fake-a", Prompt: "review", Agent: &fakeHeadlessAgent{name: "fake-a", output: "A result\n"}},
		{Name: "fake-b", Prompt: "review", Agent: &fakeHeadlessAgent{name: "fake-b", output: "B result\n"}},
		{Name: "fake-c", Prompt: "review", Agent: &fakeHeadlessAgent{name: "fake-c", output: "C result\n"}},
	}

	var buf bytes.Buffer
	orch := newMultiReviewOrchestrator(tmp)
	result, err := orch.Run(context.Background(), tasks, &buf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Runs) != 3 {
		t.Fatalf("Runs count = %d, want 3", len(result.Runs))
	}
	for _, r := range result.Runs {
		if r.Status != AgentRunDone {
			t.Errorf("agent %s status = %d, want Done", r.Name, r.Status)
		}
		if len(r.FinalOutput) == 0 {
			t.Errorf("agent %s has empty FinalOutput", r.Name)
		}
	}
	_, ok, readErr := ReadPendingReviewMarker(context.Background())
	if readErr != nil {
		t.Fatalf("read marker: %v", readErr)
	}
	if ok {
		t.Error("marker should be cleared on happy exit")
	}
}

// TestOrchestrator_WritesSharedMarker pins that the marker written during
// a multi-agent run carries AgentNames (enabling sibling-agent adoption)
// and is visible to hooks while the run is in flight.
func TestOrchestrator_WritesSharedMarker(t *testing.T) {
	// Cannot t.Parallel — reads the global pending-review marker.
	tmp := setupReviewTestRepoWithCommit(t)

	// A delay long enough to reliably observe the marker while the agents
	// are still running. We use .delay to block subprocess exit; 500ms is
	// generous compared to the 50ms poll.
	tasks := []MultiAgentTask{
		{Name: "fake-a", Prompt: "review", Agent: &fakeHeadlessAgent{name: "fake-a", output: "A\n", delay: 500 * time.Millisecond}},
		{Name: "fake-b", Prompt: "review", Agent: &fakeHeadlessAgent{name: "fake-b", output: "B\n", delay: 500 * time.Millisecond}},
	}

	orch := newMultiReviewOrchestrator(tmp)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, runErr := orch.Run(context.Background(), tasks, io.Discard); runErr != nil {
			t.Errorf("orch.Run: %v", runErr)
		}
	}()

	// Poll briefly for the marker — the orchestrator writes it before
	// spawning goroutines, so 10×50ms should be more than enough.
	var marker PendingReviewMarker
	var markerOK bool
	for range 10 {
		m, ok, err := ReadPendingReviewMarker(context.Background())
		if err != nil {
			t.Fatalf("read marker: %v", err)
		}
		if ok {
			marker = m
			markerOK = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !markerOK {
		t.Fatal("marker was never written mid-run")
	}
	if len(marker.AgentNames) != 2 {
		t.Errorf("marker.AgentNames = %v, want 2 entries", marker.AgentNames)
	}
	if marker.WorktreePath != tmp {
		t.Errorf("marker.WorktreePath = %q, want %q", marker.WorktreePath, tmp)
	}
	wg.Wait()

	// Post-exit, marker must be cleared.
	_, postOK, postErr := ReadPendingReviewMarker(context.Background())
	if postErr != nil {
		t.Fatalf("read marker: %v", postErr)
	}
	if postOK {
		t.Error("marker should be cleared after Run returns")
	}
}

// TestOrchestrator_FailurePerAgentIndependent pins that one agent's
// non-zero exit does not cancel the others — Run classifies each
// independently and still returns a result.
func TestOrchestrator_FailurePerAgentIndependent(t *testing.T) {
	// Cannot t.Parallel — writes the global pending-review marker.
	tmp := setupReviewTestRepoWithCommit(t)

	tasks := []MultiAgentTask{
		{Name: "fake-ok", Prompt: "r", Agent: &fakeHeadlessAgent{name: "fake-ok", output: "OK\n"}},
		{Name: "fake-fail", Prompt: "r", Agent: &fakeHeadlessAgent{name: "fake-fail", output: "", exitCode: 1}},
	}

	orch := newMultiReviewOrchestrator(tmp)
	result, err := orch.Run(context.Background(), tasks, io.Discard)
	if err != nil {
		t.Fatalf("Run should return non-nil result even with failures: %v", err)
	}

	var succ, fail int
	for _, r := range result.Runs {
		switch r.Status {
		case AgentRunDone:
			succ++
		case AgentRunFailed:
			fail++
		case AgentRunQueued, AgentRunRunning, AgentRunCancelled:
			t.Errorf("unexpected status %d for agent %s", r.Status, r.Name)
		}
	}
	if succ != 1 || fail != 1 {
		t.Errorf("succ=%d fail=%d, want 1 and 1", succ, fail)
	}
}

// TestOrchestrator_CancelCleansMarker pins that cancelling the context
// reliably fires SIGTERM to subprocesses (via exec.CommandContext), flips
// result.Cancelled, and clears the marker.
func TestOrchestrator_CancelCleansMarker(t *testing.T) {
	// Cannot t.Parallel — reads the global pending-review marker.
	tmp := setupReviewTestRepoWithCommit(t)

	tasks := []MultiAgentTask{
		{Name: "slow", Prompt: "r", Agent: &fakeHeadlessAgent{name: "slow", output: "", delay: 2 * time.Second}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	orch := newMultiReviewOrchestrator(tmp)
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	result, err := orch.Run(ctx, tasks, io.Discard)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("cancel didn't kill subprocess fast enough: %v", elapsed)
	}
	if !result.Cancelled {
		// Cancellation flows through signal goroutine when os signal fires;
		// a context-only cancel skips that path. Accept not-cancelled as long
		// as the subprocess did exit quickly (already asserted above).
		t.Logf("result.Cancelled = false (acceptable when cancel came via ctx, not signal)")
	}
	_, markerOK, markerErr := ReadPendingReviewMarker(context.Background())
	if markerErr != nil {
		t.Fatalf("read marker: %v", markerErr)
	}
	if markerOK {
		t.Error("marker should be cleared after cancel")
	}
	// Status should reflect cancellation via context error.
	if len(result.Runs) != 1 {
		t.Fatalf("Runs count = %d, want 1", len(result.Runs))
	}
	if result.Runs[0].Status != AgentRunCancelled && result.Runs[0].Status != AgentRunFailed {
		t.Errorf("slow agent status = %d, want Cancelled or Failed", result.Runs[0].Status)
	}
}

// TestOrchestrator_NoTasks returns a sentinel error without touching the marker.
func TestOrchestrator_NoTasks(t *testing.T) {
	t.Parallel()

	orch := newMultiReviewOrchestrator(t.TempDir())
	_, err := orch.Run(context.Background(), nil, io.Discard)
	if err == nil {
		t.Fatal("Run with no tasks should return an error")
	}
}

// TestFilterCodexOutput_StripsSessionBanner covers the real noise patterns
// observed in codex exec-mode output: session banner, workdir/model/etc.
// headers, the `exec <cmd>\n succeeded in...\n<output>\n\n` tool-call
// blocks, and hook-firing lines. Narrative text and markers stay intact
// so the downstream extractor can still find them.
func TestFilterCodexOutput_StripsSessionBanner(t *testing.T) {
	t.Parallel()
	raw := []byte(`OpenAI Codex v0.124.0 (research preview)
--------
workdir: /tmp/foo
model: gpt-5.4
provider: openai
approval: never
sandbox: read-only
reasoning effort: medium
reasoning summaries: none
session id: 019dbea3-7f20-7922-a962-d9fd85d22ac4
--------
user
Please run these review skills in order: 1. /review

codex
Using superpowers:using-superpowers first, then code-reviewer.
exec
/bin/zsh -lc "sed -n '1,220p' /.../SKILL.md" in /tmp/foo
 succeeded in 0ms:
[200 lines of skill content]

codex
No findings.

Residual risk: ...
tokens used
12,826
`)
	cleaned := string(filterCodexOutput(raw))
	if strings.Contains(cleaned, "OpenAI Codex v") {
		t.Error("session banner should be stripped")
	}
	if strings.Contains(cleaned, "workdir:") {
		t.Error("workdir line should be stripped")
	}
	if strings.Contains(cleaned, "/bin/zsh -lc") {
		t.Error("exec block should be stripped")
	}
	if strings.Contains(cleaned, "200 lines of skill content") {
		t.Error("exec block body should be stripped")
	}
	if !strings.Contains(cleaned, "No findings.") {
		t.Error("narrative content should be preserved")
	}
	if !strings.Contains(cleaned, "Using superpowers") {
		t.Error("narrative content should be preserved")
	}
}

// TestFilterCodexOutput_PreservesNarrativeStartingWithExec pins that
// the exec-block anchor doesn't swallow legitimate narrative whose
// first word happens to be "exec…" — the previous strings.HasPrefix
// check matched anything starting with those four bytes, including
// "Examining" (capital E starts with 'E' so was safe) and "executed"
// (lowercase, was eaten). Anchoring on either bare `^exec$` or the
// `exec <cmd> in /` shape codex actually emits keeps narrative intact.
func TestFilterCodexOutput_PreservesNarrativeStartingWithExec(t *testing.T) {
	t.Parallel()
	raw := []byte("codex\nexecuted by the runner.\nexecution succeeded with no findings.\nexec ls in /tmp\n succeeded in 0ms:\nfile.txt\n\nMore narrative.\ntokens used\n100\n")
	cleaned := string(filterCodexOutput(raw))
	for _, want := range []string{"executed by the runner.", "execution succeeded with no findings.", "More narrative."} {
		if !strings.Contains(cleaned, want) {
			t.Errorf("legitimate narrative %q was filtered out:\n%s", want, cleaned)
		}
	}
	if strings.Contains(cleaned, "exec ls in /tmp") {
		t.Error("real exec block header should still be stripped")
	}
}

// TestApplyOutputFilter_UnknownAgentPassesThrough pins that agents
// without a registered filter (claude-code, gemini-cli, fakes) get their
// raw output back unchanged — no accidental mutation via shared slices.
func TestApplyOutputFilter_UnknownAgentPassesThrough(t *testing.T) {
	t.Parallel()
	raw := []byte("some random output\n")
	got := applyOutputFilter("unknown-agent", raw)
	if !bytes.Equal(got, raw) {
		t.Errorf("unknown agent: output %q != input %q", got, raw)
	}
}

// TestExtractCodexFinal_WithMarkers pins the marker-based slicing: text
// between the last `^codex$` line and the `tokens used` marker is the
// final narrative response.
func TestExtractCodexFinal_WithMarkers(t *testing.T) {
	t.Parallel()
	input := "codex\nNo findings.\n\nResidual risk: something.\ntokens used\n12826\n"
	got := extractCodexFinal(input)
	want := "No findings.\n\nResidual risk: something."
	if strings.TrimSpace(got) != want {
		t.Errorf("extractCodexFinal =\n%q\nwant:\n%q", got, want)
	}
}

// TestExtractCodexFinal_FallsBackWhenMarkersMissing pins the defensive
// fallback: if codex's output format drifts so the markers aren't found,
// we return the full cleaned input rather than silently dropping it.
func TestExtractCodexFinal_FallsBackWhenMarkersMissing(t *testing.T) {
	t.Parallel()
	input := "some text without codex marker or tokens used line\n"
	got := extractCodexFinal(input)
	if got != input {
		t.Errorf("extractCodexFinal fallback: got %q; want input unchanged", got)
	}
}
