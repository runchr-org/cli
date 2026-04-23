package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/logging"
)

// previewRateWindow caps the rate at which preview tees forward stdout
// lines to the TUI — roughly 10/sec per agent. Keeps the bubbletea
// program's inbound message queue manageable even when a subprocess
// floods stdout.
const previewRateWindow = 100 * time.Millisecond

// multiReviewOrchestrator owns the lifecycle of an N-agent parallel review:
// writes the shared pending marker, spawns one goroutine per agent, drives
// cancellation, cleans up on exit. When stdout is a terminal, it drives a
// bubbletea reviewTUIModel with per-agent live status rows; otherwise it
// falls back to an io.Writer dump of all per-agent outputs. Either way the
// final dump runs after the TUI exits so transcripts follow the table.
type multiReviewOrchestrator struct {
	worktreePath string
}

func newMultiReviewOrchestrator(worktreePath string) *multiReviewOrchestrator {
	return &multiReviewOrchestrator{worktreePath: worktreePath}
}

// Run spawns all tasks, waits for completion or cancellation, cleans up the
// marker, and dumps final per-agent responses to out.
//
// Run returns a non-nil MultiRunResult even when individual agents fail —
// callers use it to distinguish "N of M succeeded" from "could not run at
// all". An error return means the orchestrator itself could not start
// (missing tasks, marker write failure).
func (o *multiReviewOrchestrator) Run(ctx context.Context, tasks []MultiAgentTask, out io.Writer) (MultiRunResult, error) {
	if len(tasks) == 0 {
		return MultiRunResult{}, errNoMultiAgentTasks
	}

	// 1. Write shared marker so UserPromptSubmit hooks in each spawned
	// agent can adopt and tag their session. HEAD lookup is best-effort —
	// adoption tolerates an empty StartingSHA.
	headSHA, headErr := currentHeadSHA(ctx)
	if headErr != nil {
		logging.Debug(ctx, "orchestrator: HEAD resolve failed",
			slog.String("error", headErr.Error()))
	}
	agentNames := make([]string, len(tasks))
	for i, t := range tasks {
		agentNames[i] = t.Name
	}
	if err := WritePendingReviewMarker(ctx, PendingReviewMarker{
		AgentNames:   agentNames,
		StartingSHA:  headSHA,
		StartedAt:    time.Now().UTC(),
		WorktreePath: o.worktreePath,
	}); err != nil {
		return MultiRunResult{}, fmt.Errorf("write marker: %w", err)
	}

	// 2. Defer marker cleanup regardless of exit path. A fresh context is
	// used because ctx may already be cancelled by the time we run.
	defer func() {
		if err := ClearPendingReviewMarker(context.Background()); err != nil {
			logging.Debug(ctx, "orchestrator: marker cleanup failed",
				slog.String("error", err.Error()))
		}
	}()

	// 3. Cancel-aware child context. Subprocesses launched via
	// exec.CommandContext inherit this, so cancelling it fires SIGTERM.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	// 4. tasksCmds stores the live *exec.Cmd for each task. Declared
	// before the signal-handler goroutine because that goroutine may snapshot
	// it from a different goroutine — the mutex guards the shared state.
	tasksCmds := make([]*exec.Cmd, len(tasks))
	var tasksCmdsMu sync.Mutex
	setCmd := func(i int, cmd *exec.Cmd) {
		tasksCmdsMu.Lock()
		defer tasksCmdsMu.Unlock()
		tasksCmds[i] = cmd
	}
	snapshotCmds := func() []*exec.Cmd {
		tasksCmdsMu.Lock()
		defer tasksCmdsMu.Unlock()
		cp := make([]*exec.Cmd, len(tasksCmds))
		copy(cp, tasksCmds)
		return cp
	}

	// 5. Signal handler: first Ctrl+C → cancel runCtx (SIGTERM to
	// subprocesses via exec.CommandContext); 5s watchdog or second Ctrl+C
	// escalates to SIGKILL.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	cancelled := &atomicBool{}
	allDone := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			cancelled.store(true)
			cancelRun()
			watchdog := time.NewTimer(5 * time.Second)
			defer watchdog.Stop()
			select {
			case <-sigCh:
				forceKillAll(snapshotCmds())
			case <-watchdog.C:
				forceKillAll(snapshotCmds())
			case <-allDone:
			}
		case <-allDone:
		}
	}()

	// 6. Optional bubbletea program. Only spin up the TUI when out is a
	// real terminal — piping to a file or CI log pipe should get the
	// plain io.Writer dump instead of escape-code garbage.
	var program *tea.Program
	if isTerminalWriter(out) {
		program = tea.NewProgram(newReviewTUIModel(tasks), tea.WithOutput(out))
	}
	send := sendFunc(program)

	// 7. Spawn one goroutine per task. Each goroutine also posts state +
	// preview messages to the TUI (no-op when program is nil).
	var wg sync.WaitGroup
	results := make([]AgentRunResult, len(tasks))
	for i, task := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = runSingleAgentTask(runCtx, task, setCmd, i, send)
		}()
	}

	// 8. If we have a TUI, run program in the foreground while a small
	// goroutine waits for all agents to finish and then signals quit.
	// Without a TUI, just wait for the goroutines to finish synchronously.
	if program != nil {
		go func() {
			wg.Wait()
			close(allDone)
			// Last chance for the TUI to repaint with final state before
			// allRowsTerminal triggers its own tea.Quit. Sending Quit here
			// is a safety net in case some row never reached a terminal
			// status (shouldn't happen, but a silent hang would be worse).
			program.Quit()
		}()
		if _, runErr := program.Run(); runErr != nil {
			logging.Debug(ctx, "orchestrator: tea.Program exited with error",
				slog.String("error", runErr.Error()))
		}
		wg.Wait()
	} else {
		wg.Wait()
		close(allDone)
	}

	// 9. Dump per-agent outputs + summary. Runs after the TUI exits so
	// the final table paint stays above the full responses.
	dumpMultiAgentResults(out, tasks, results, cancelled.load())

	return MultiRunResult{
		Runs:      results,
		Duration:  computeTotalDuration(results),
		Cancelled: cancelled.load(),
	}, nil
}

// sendFunc returns a tea-message sender bound to program. If program is
// nil (non-TTY output), returns a no-op so call sites don't have to
// branch on nil at every post.
func sendFunc(program *tea.Program) func(msg tea.Msg) {
	if program == nil {
		return func(_ tea.Msg) {}
	}
	return program.Send
}

// errNoMultiAgentTasks is the sentinel returned when Run is invoked with
// an empty task list — a programmer error the caller should surface.
var errNoMultiAgentTasks = errors.New("orchestrator: no tasks")

// runSingleAgentTask launches one headless agent subprocess, buffers its
// stdout/stderr, and classifies the exit. Called once per task by Run.
// send posts lifecycle + preview messages to the TUI; a no-op sender is
// used when the caller output isn't a terminal.
func runSingleAgentTask(ctx context.Context, task MultiAgentTask, setCmd func(int, *exec.Cmd), idx int, send func(tea.Msg)) AgentRunResult {
	startTime := time.Now()

	cmd, err := task.Agent.LaunchHeadlessCmd(ctx, task.Prompt)
	if err != nil {
		send(agentStateMsg{Name: task.Name, Status: AgentRunFailed, Duration: time.Since(startTime)})
		return AgentRunResult{
			Name:     task.Name,
			Status:   AgentRunFailed,
			Duration: time.Since(startTime),
			Err:      fmt.Errorf("launch %s: %w", task.Name, err),
		}
	}
	setCmd(idx, cmd)

	var buf bytes.Buffer
	tee := newPreviewTeeWriter(&buf, send, task.Name)
	cmd.Stdout = tee
	cmd.Stderr = tee
	// WaitDelay protects against subprocesses whose children hold stdio
	// pipes open after the root pid has been killed (e.g. `sh -c "sleep
	// N"`). Without it, cmd.Wait() blocks on pipe close even though the
	// context is already cancelled. 500ms is generous for normal child
	// cleanup while keeping Ctrl+C responsive.
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 500 * time.Millisecond
	}

	// Notify the TUI that this agent is now running. Posted after
	// LaunchHeadlessCmd so failures above never advertise a "running"
	// state the orchestrator never actually observed.
	send(agentStateMsg{Name: task.Name, Status: AgentRunRunning})

	runErr := cmd.Run()
	duration := time.Since(startTime)

	status := AgentRunDone
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(runErr, &exitErr):
			exitCode = exitErr.ExitCode()
			status = AgentRunFailed
		case errors.Is(ctx.Err(), context.Canceled):
			status = AgentRunCancelled
		default:
			status = AgentRunFailed
		}
	}

	// Post-exit: resolve transcript path + token usage for successful runs.
	// Best-effort — transcripts may legitimately be unresolvable for some
	// fake/minimal agents in tests.
	var transcriptPath string
	var tokenUsage *agent.TokenUsage
	if status == AgentRunDone {
		if sbp, ok := task.Agent.(agent.SessionBaseDirProvider); ok {
			if dir, dirErr := sbp.GetSessionBaseDir(); dirErr == nil {
				transcriptPath = resolveLatestTranscript(dir)
				if transcriptPath != "" {
					data, readErr := os.ReadFile(transcriptPath) //nolint:gosec // path derived from agent's own session dir
					if readErr == nil {
						if tc, tok := task.Agent.(agent.TokenCalculator); tok {
							usage, calcErr := tc.CalculateTokenUsage(data, 0)
							if calcErr == nil {
								tokenUsage = usage
							}
						}
					}
				}
			}
		}
	}

	totalTokens := 0
	if tokenUsage != nil {
		totalTokens = tokenUsage.InputTokens +
			tokenUsage.CacheCreationTokens +
			tokenUsage.CacheReadTokens +
			tokenUsage.OutputTokens
	}
	send(agentStateMsg{
		Name:     task.Name,
		Status:   status,
		Duration: duration,
		ExitCode: exitCode,
		Tokens:   totalTokens,
	})

	return AgentRunResult{
		Name:           task.Name,
		Status:         status,
		ExitCode:       exitCode,
		Duration:       duration,
		FinalOutput:    buf.Bytes(),
		Err:            runErr,
		TokenUsage:     tokenUsage,
		TranscriptPath: transcriptPath,
	}
}

// previewTeeWriter tees subprocess stdout/stderr bytes into a buffer
// (for the final per-agent dump) and — rate-limited — emits preview
// lines to the TUI as agentPreviewMsg. The rate limit is per-agent:
// at most one message per previewRateWindow.
type previewTeeWriter struct {
	buf        io.Writer
	send       func(tea.Msg)
	agentName  string
	mu         sync.Mutex
	partial    []byte
	lastSentAt time.Time
}

func newPreviewTeeWriter(buf io.Writer, send func(tea.Msg), agentName string) *previewTeeWriter {
	return &previewTeeWriter{buf: buf, send: send, agentName: agentName}
}

// Write implements io.Writer: captures into the buffer for the final
// response dump, then scans for newline-terminated lines and forwards
// the last-complete one as a preview message, subject to the rate
// window. A partial line without a trailing newline is carried forward.
func (p *previewTeeWriter) Write(data []byte) (int, error) {
	n, err := p.buf.Write(data)
	if err != nil {
		return n, fmt.Errorf("tee buf write: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.partial = append(p.partial, data[:n]...)
	// Only scan on newline; short writes with no newline just accumulate.
	lastNL := bytes.LastIndexByte(p.partial, '\n')
	if lastNL < 0 {
		return n, nil
	}
	// The last complete line is the segment right before lastNL.
	completed := p.partial[:lastNL]
	p.partial = p.partial[lastNL+1:]
	prevNL := bytes.LastIndexByte(completed, '\n')
	var last []byte
	if prevNL < 0 {
		last = completed
	} else {
		last = completed[prevNL+1:]
	}
	if len(last) == 0 {
		return n, nil
	}
	now := time.Now()
	if now.Sub(p.lastSentAt) < previewRateWindow {
		return n, nil
	}
	p.lastSentAt = now
	// Copy into a new buffer because p.partial's backing array is reused.
	line := string(bytes.TrimRight(last, "\r"))
	p.send(agentPreviewMsg{Name: p.agentName, Line: line})
	return n, nil
}

// dumpMultiAgentResults prints per-agent headers + FinalOutput, then a
// summary line, then transcript paths for successful agents.
func dumpMultiAgentResults(out io.Writer, tasks []MultiAgentTask, results []AgentRunResult, cancelled bool) {
	for i, r := range results {
		fmt.Fprintf(out, "\n─────── %s review ───────\n", tasks[i].Name)
		switch r.Status {
		case AgentRunFailed:
			fmt.Fprintf(out, "(failed — exit %d)\n", r.ExitCode)
			if r.Err != nil {
				fmt.Fprintf(out, "%v\n\n", r.Err)
			}
		case AgentRunCancelled:
			fmt.Fprintln(out, "(cancelled)")
			fmt.Fprintln(out)
		case AgentRunQueued, AgentRunRunning, AgentRunDone:
			// Done: no header; buffered output follows.
			// Queued/Running would indicate a logic bug; fall through.
		}
		if _, err := out.Write(r.FinalOutput); err != nil {
			// Writer failed mid-dump; further writes will also fail, so stop.
			return
		}
	}

	var nSucc, nFail, nCancel int
	for _, r := range results {
		switch r.Status {
		case AgentRunDone:
			nSucc++
		case AgentRunFailed:
			nFail++
		case AgentRunCancelled:
			nCancel++
		case AgentRunQueued, AgentRunRunning:
			// Should not occur after Wait; ignore for summary stats.
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%d agents done in %s (%d succeeded, %d failed, %d cancelled)\n",
		len(results), computeTotalDuration(results), nSucc, nFail, nCancel)
	if !cancelled && nSucc > 0 {
		fmt.Fprintln(out, "  Run `git commit` to attach the review to the next checkpoint.")
	}

	anyPath := false
	for _, r := range results {
		if r.TranscriptPath == "" {
			continue
		}
		if !anyPath {
			fmt.Fprintln(out, "\n  Full transcripts (including tool calls + reasoning):")
			anyPath = true
		}
		fmt.Fprintf(out, "    %-12s → %s\n", r.Name, r.TranscriptPath)
	}
}

// computeTotalDuration returns the maximum Duration across results — the
// wall-clock span of the whole parallel run, not the sum of per-agent
// runtimes.
func computeTotalDuration(results []AgentRunResult) time.Duration {
	var longest time.Duration
	for _, r := range results {
		if r.Duration > longest {
			longest = r.Duration
		}
	}
	return longest
}

// forceKillAll sends SIGKILL to every live subprocess. Null cmds (agents
// whose LaunchHeadlessCmd call had not returned yet when cancellation
// arrived) are skipped.
func forceKillAll(cmds []*exec.Cmd) {
	for _, cmd := range cmds {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		_ = cmd.Process.Kill() //nolint:errcheck // best-effort cleanup after cancel
	}
}

// resolveLatestTranscript returns the newest .jsonl (or .json) file under
// baseDir, searched recursively. Returns "" if baseDir is empty, unreadable,
// or contains no transcript files. Uses a best-effort newest-file
// heuristic; a future refinement could switch to a session-ID-specific
// resolver.
func resolveLatestTranscript(baseDir string) string {
	if baseDir == "" {
		return ""
	}
	var latestPath string
	var latestMod time.Time
	walkErr := walkTranscriptFiles(baseDir, func(path string, info os.FileInfo) {
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestPath = path
		}
	})
	if walkErr != nil {
		return ""
	}
	return latestPath
}

// walkTranscriptFiles walks baseDir recursively, invoking visit for every
// file whose extension is .jsonl or .json. Non-existent or unreadable
// directories return an error; individual read errors inside are silently
// skipped (best-effort resolution).
func walkTranscriptFiles(baseDir string, visit func(path string, info os.FileInfo)) error {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return fmt.Errorf("read transcript dir: %w", err)
	}
	for _, e := range entries {
		full := baseDir + string(os.PathSeparator) + e.Name()
		if e.IsDir() {
			_ = walkTranscriptFiles(full, visit) //nolint:errcheck // best-effort
			continue
		}
		name := e.Name()
		if !hasTranscriptExt(name) {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		visit(full, info)
	}
	return nil
}

func hasTranscriptExt(name string) bool {
	for _, ext := range []string{".jsonl", ".json"} {
		if len(name) >= len(ext) && name[len(name)-len(ext):] == ext {
			return true
		}
	}
	return false
}

// atomicBool is a tiny mutex-guarded boolean used to flip the "cancelled"
// flag from the signal goroutine and read it from the completion path.
// Using sync/atomic.Bool would work too, but a mutex keeps the call sites
// explicit and matches the plan's spec.
type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (b *atomicBool) store(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.v = v
}

func (b *atomicBool) load() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.v
}
