package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/validation"
)

// Compile-time interface assertions for new interfaces.
var (
	_ agent.TranscriptAnalyzer     = (*ClaudeCodeAgent)(nil)
	_ agent.TranscriptPreparer     = (*ClaudeCodeAgent)(nil)
	_ agent.TokenCalculator        = (*ClaudeCodeAgent)(nil)
	_ agent.SubagentAwareExtractor = (*ClaudeCodeAgent)(nil)
	_ agent.HookResponseWriter     = (*ClaudeCodeAgent)(nil)
)

// WriteHookResponse outputs a JSON hook response to stdout.
// Claude Code reads this JSON and displays the systemMessage to the user.
func (c *ClaudeCodeAgent) WriteHookResponse(message string) error {
	resp := struct {
		SystemMessage string `json:"systemMessage,omitempty"`
	}{SystemMessage: message}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		return fmt.Errorf("failed to encode hook response: %w", err)
	}
	return nil
}

// HookNames returns the hook verbs Claude Code supports.
// These become subcommands: entire hooks claude-code <verb>
func (c *ClaudeCodeAgent) HookNames() []string {
	return []string{
		HookNameSessionStart,
		HookNameSessionEnd,
		HookNameStop,
		HookNameUserPromptSubmit,
		HookNamePreTask,
		HookNamePostTask,
		HookNamePostTodo,
	}
}

// ParseHookEvent translates a Claude Code hook into a normalized lifecycle Event.
// Returns nil if the hook has no lifecycle significance.
func (c *ClaudeCodeAgent) ParseHookEvent(_ context.Context, hookName string, stdin io.Reader) (*agent.Event, error) {
	switch hookName {
	case HookNameSessionStart:
		return c.parseSessionStart(stdin)
	case HookNameUserPromptSubmit:
		return c.parseTurnStart(stdin)
	case HookNameStop:
		return c.parseTurnEnd(stdin)
	case HookNameSessionEnd:
		return c.parseSessionEnd(stdin)
	case HookNamePreTask:
		return c.parseSubagentStart(stdin)
	case HookNamePostTask:
		return c.parseSubagentEnd(stdin)
	case HookNamePostTodo:
		// PostTodo is Claude-specific; handled outside the generic dispatcher.
		return nil, nil //nolint:nilnil // nil event = no lifecycle action
	default:
		return nil, nil //nolint:nilnil // Unknown hooks have no lifecycle action
	}
}

// ReadTranscript reads the raw JSONL transcript bytes for a session.
func (c *ClaudeCodeAgent) ReadTranscript(sessionRef string) ([]byte, error) {
	data, err := os.ReadFile(sessionRef) //nolint:gosec // Path comes from agent hook input
	if err != nil {
		return nil, fmt.Errorf("failed to read transcript: %w", err)
	}
	return data, nil
}

// PrepareTranscript waits for Claude Code's async transcript flush to complete.
// Claude writes a hook_progress sentinel entry after flushing all pending writes.
func (c *ClaudeCodeAgent) PrepareTranscript(ctx context.Context, sessionRef string) error {
	waitForTranscriptFlush(ctx, sessionRef, time.Now())
	return nil
}

// CalculateTokenUsage computes token usage from the transcript starting at the given line offset.
func (c *ClaudeCodeAgent) CalculateTokenUsage(transcriptData []byte, fromOffset int) (*agent.TokenUsage, error) {
	return c.CalculateTotalTokenUsage(transcriptData, fromOffset, "")
}

// --- Internal hook parsing functions ---

func (c *ClaudeCodeAgent) parseSessionStart(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[sessionInfoRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.SessionStart,
		SessionID:  raw.SessionID,
		SessionRef: c.resolveTranscriptPath(raw.TranscriptPath, raw.SessionID),
		Model:      raw.Model,
		Timestamp:  time.Now(),
	}, nil
}

func (c *ClaudeCodeAgent) parseTurnStart(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[userPromptSubmitRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.TurnStart,
		SessionID:  raw.SessionID,
		SessionRef: c.resolveTranscriptPath(raw.TranscriptPath, raw.SessionID),
		Prompt:     raw.Prompt,
		Timestamp:  time.Now(),
	}, nil
}

func (c *ClaudeCodeAgent) parseTurnEnd(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[sessionInfoRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.TurnEnd,
		SessionID:  raw.SessionID,
		SessionRef: c.resolveTranscriptPath(raw.TranscriptPath, raw.SessionID),
		Model:      raw.Model,
		Timestamp:  time.Now(),
	}, nil
}

func (c *ClaudeCodeAgent) parseSessionEnd(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[sessionInfoRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.SessionEnd,
		SessionID:  raw.SessionID,
		SessionRef: c.resolveTranscriptPath(raw.TranscriptPath, raw.SessionID),
		Model:      raw.Model,
		Timestamp:  time.Now(),
	}, nil
}

func (c *ClaudeCodeAgent) parseSubagentStart(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[taskHookInputRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.SubagentStart,
		SessionID:  raw.SessionID,
		SessionRef: c.resolveTranscriptPath(raw.TranscriptPath, raw.SessionID),
		ToolUseID:  raw.ToolUseID,
		ToolInput:  raw.ToolInput,
		Timestamp:  time.Now(),
	}, nil
}

func (c *ClaudeCodeAgent) parseSubagentEnd(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[postToolHookInputRaw](stdin)
	if err != nil {
		return nil, err
	}
	event := &agent.Event{
		Type:       agent.SubagentEnd,
		SessionID:  raw.SessionID,
		SessionRef: c.resolveTranscriptPath(raw.TranscriptPath, raw.SessionID),
		ToolUseID:  raw.ToolUseID,
		ToolInput:  raw.ToolInput,
		Timestamp:  time.Now(),
	}
	if raw.ToolResponse.AgentID != "" {
		event.SubagentID = raw.ToolResponse.AgentID
	}
	return event, nil
}

// claudeWorktreeMarker is the substring that appears in an encoded project-dir
// segment when Claude Code is invoked from inside its worktree feature.
// SanitizePathForClaude replaces every non-alphanumeric character with '-',
// so a CWD ending in "/.claude/worktrees/<branch>" produces a project segment
// containing "--claude-worktrees-<branch-encoded>".
const claudeWorktreeMarker = "--claude-worktrees-"

// resolveTranscriptPath returns the canonical path to a Claude transcript file,
// recovering from the Claude Code worktree-feature mismatch where
// transcript_path encodes the worktree CWD (e.g. .claude/worktrees/<branch>) but
// the file is stored under the parent repo's project dir.
//
// When the reported path doesn't exist, the resolver strips the worktree marker
// from the project segment and checks the parent-repo candidate. The lookup is
// fully deterministic — no directory scanning, no chance of crossing into an
// unrelated project that happens to share a session ID.
//
// The fallback is gated to keep risk minimal:
//   - Only triggers on os.IsNotExist (permission/IO errors are returned as-is
//     so real problems aren't masked).
//   - Only triggers when the reported path is under the Claude projects base
//     dir.
//   - Only triggers when the project segment contains claudeWorktreeMarker.
//   - The session ID is validated with validation.ValidateAgentSessionID
//     before being used in filepath.Join, blocking traversal via hostile hook
//     input.
//   - The candidate path must itself exist before being returned.
func (c *ClaudeCodeAgent) resolveTranscriptPath(sessionRef, sessionID string) string {
	if sessionRef == "" || sessionID == "" {
		return sessionRef
	}
	if _, err := os.Stat(sessionRef); err == nil {
		return sessionRef
	} else if !os.IsNotExist(err) {
		return sessionRef
	}
	if err := validation.ValidateAgentSessionID(sessionID); err != nil {
		return sessionRef
	}
	base, err := c.GetSessionBaseDir()
	if err != nil {
		return sessionRef
	}
	candidate := worktreeParentCandidate(filepath.Clean(base), filepath.Clean(sessionRef), sessionID)
	if candidate == "" {
		return sessionRef
	}
	if _, err := os.Stat(candidate); err != nil {
		return sessionRef
	}
	logging.Info(logging.WithComponent(context.Background(), "agent.claudecode"),
		"resolved transcript via worktree fallback",
		slog.String("reported", sessionRef),
		slog.String("found", candidate),
		slog.String("session_id", sessionID),
	)
	return candidate
}

// worktreeParentCandidate returns the parent-repo equivalent of a reported
// transcript path when the project segment carries the Claude Code worktree
// marker. Returns "" if reported is not under base, has no project segment,
// or has no worktree marker. Callers must validate sessionID before calling.
//
// strings.LastIndex (not Index) is used because the *synthetic* worktree
// marker is always the trailing occurrence in the project segment: Claude
// appends "/.claude/worktrees/<branch>" to the cwd, and SanitizePathForClaude
// preserves left-to-right order, so the suffix carrying the bug is the last
// one. Cutting at the first match would mis-strip repos whose sanitized root
// already contains the token (e.g. repos checked out under a directory
// literally named "...--claude-worktrees-...").
func worktreeParentCandidate(base, reported, sessionID string) string {
	sep := string(os.PathSeparator)
	prefix := base + sep
	if !strings.HasPrefix(reported, prefix) {
		return ""
	}
	projectSeg, _, ok := strings.Cut(reported[len(prefix):], sep)
	if !ok || projectSeg == "" {
		return ""
	}
	idx := strings.LastIndex(projectSeg, claudeWorktreeMarker)
	if idx <= 0 {
		return ""
	}
	return filepath.Join(base, projectSeg[:idx], sessionID+".jsonl")
}

// --- Transcript flush sentinel ---

// stopHookSentinel is the string that appears in Claude Code's hook_progress
// entry when the stop hook has been invoked, indicating the transcript is fully flushed.
const stopHookSentinel = "hooks claude-code stop"

// waitForTranscriptFlush polls the transcript file for the stop hook sentinel.
// Falls back silently after a timeout.
func waitForTranscriptFlush(ctx context.Context, transcriptPath string, hookStartTime time.Time) {
	const (
		maxWait      = 3 * time.Second
		pollInterval = 50 * time.Millisecond
		tailBytes    = 4096
		maxSkew      = 2 * time.Second
	)

	logCtx := logging.WithComponent(ctx, "agent.claudecode")

	// Fast path: skip the poll loop when the sentinel can't possibly appear.
	// - File doesn't exist: nothing to poll.
	// - File is stale (unmodified for 2+ min): agent isn't running anymore.
	//   This avoids 3s timeouts per stale "active" session (e.g., agent crashed
	//   without firing stop hook).
	const staleThreshold = 2 * time.Minute
	info, err := os.Stat(transcriptPath)
	if err != nil {
		// Most likely the file doesn't exist; other errors (permission, etc.)
		// would also prevent polling, so skip the wait either way.
		return
	}
	fileAge := time.Since(info.ModTime())
	if fileAge > staleThreshold {
		logging.Debug(logCtx, "transcript file is stale, skipping sentinel wait",
			slog.Duration("file_age", fileAge),
		)
		return
	}

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if checkStopSentinel(transcriptPath, tailBytes, hookStartTime, maxSkew) {
			logging.Debug(logCtx, "transcript flush sentinel found",
				slog.Duration("wait", time.Since(hookStartTime)),
			)
			return
		}
		time.Sleep(pollInterval)
	}
	logging.Warn(logCtx, "transcript flush sentinel not found within timeout, proceeding",
		slog.Duration("timeout", maxWait),
	)
}

// checkStopSentinel reads the tail of the transcript file and looks for the sentinel.
func checkStopSentinel(path string, tailBytes int64, hookStartTime time.Time, maxSkew time.Duration) bool {
	f, err := os.Open(path) //nolint:gosec // path comes from agent hook input
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false
	}
	offset := info.Size() - tailBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return false
	}

	lines := strings.Split(string(buf), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, stopHookSentinel) {
			continue
		}

		var entry struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Timestamp == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			ts, err = time.Parse(time.RFC3339, entry.Timestamp)
			if err != nil {
				continue
			}
		}
		// Validate timestamp is within acceptable range:
		// - Not too far in the past (before hook started minus skew)
		// - Not too far in the future (after hook started plus skew)
		lowerBound := hookStartTime.Add(-maxSkew)
		upperBound := hookStartTime.Add(maxSkew)
		if ts.After(lowerBound) && ts.Before(upperBound) {
			return true
		}
	}
	return false
}
