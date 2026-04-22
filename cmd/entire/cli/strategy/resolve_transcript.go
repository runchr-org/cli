package strategy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// transcriptFileExists reports whether the transcript file at the given path
// actually exists on disk. Returns false for empty paths. This is used by
// PrepareCommitMsg to avoid adding checkpoint trailers when the transcript
// file is not locally available (e.g., cloud agents that set TranscriptPath
// in hook payloads but don't write the file to the runner's filesystem).
func transcriptFileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// resolveTranscriptPath returns the current path to the session's transcript file.
// If the file exists at state.TranscriptPath, that path is returned immediately.
//
// If the file is missing (os.ErrNotExist), the function re-resolves the path
// using the agent's ResolveSessionFile method. This handles agents that relocate
// transcripts mid-session (e.g., Cursor CLI switching from a flat layout
// <dir>/<id>.jsonl to a nested layout <dir>/<id>/<id>.jsonl).
//
// On successful re-resolution, state.TranscriptPath is updated so that
// subsequent reads use the correct path without repeating the resolution.
func resolveTranscriptPath(state *SessionState) (string, error) {
	if state.TranscriptPath == "" {
		return "", errors.New("no transcript path in session state")
	}

	// Fast path: file exists at the stored location.
	if _, err := os.Stat(state.TranscriptPath); err == nil {
		return state.TranscriptPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// Non-ENOENT error (permission denied, etc.) — return as-is.
		return "", fmt.Errorf("failed to access transcript: %w", err)
	}

	// File not found — attempt re-resolution via the agent.
	ag, agErr := agent.GetByAgentType(state.AgentType)
	if agErr != nil {
		return "", fmt.Errorf("transcript not found at %s: %w", state.TranscriptPath, os.ErrNotExist)
	}

	// First try: re-resolve using the agent's current session directory and the
	// session ID. This handles moved session-state directories (e.g., cloud agents
	// where COPILOT_SESSION_STATE_DIR points to a host-mapped path different from
	// the container path stored in TranscriptPath).
	//
	// For Copilot CLI, also check the AWF host-mapped path — the well-known
	// directory where GitHub's Agentic Workflow Firewall maps the container's
	// session-state via --session-state-dir. The hooks fire on the host but the
	// transcript lives inside the container; the AWF mount makes it accessible.
	candidateDirs := []string{}
	if sessionDir, sdErr := ag.GetSessionDir(""); sdErr == nil {
		candidateDirs = append(candidateDirs, sessionDir)
	}
	if state.AgentType == agent.AgentTypeCopilotCLI {
		candidateDirs = append(candidateDirs, "/tmp/gh-aw/sandbox/agent/session-state")
	}
	for _, dir := range candidateDirs {
		resolved := ag.ResolveSessionFile(dir, state.SessionID)
		if resolved != state.TranscriptPath {
			if _, err := os.Stat(resolved); err == nil {
				state.TranscriptPath = resolved
				return resolved, nil
			}
		}
	}

	// Second try: derive agent session ID from the stored path's filename and
	// re-resolve within the same directory. This handles layout changes (e.g.,
	// Cursor switching from flat <dir>/<id>.jsonl to nested <dir>/<id>/<id>.jsonl).
	sessionDir := filepath.Dir(state.TranscriptPath)
	base := filepath.Base(state.TranscriptPath)
	agentSessionID := strings.TrimSuffix(base, filepath.Ext(base))

	resolved := ag.ResolveSessionFile(sessionDir, agentSessionID)
	if resolved == state.TranscriptPath {
		// Agent resolved to the same path — file genuinely doesn't exist.
		return "", fmt.Errorf("transcript not found at %s: %w", state.TranscriptPath, os.ErrNotExist)
	}

	// Check if the re-resolved path exists.
	if _, err := os.Stat(resolved); err != nil {
		return "", fmt.Errorf("transcript not found at %s (also tried %s): %w", state.TranscriptPath, resolved, os.ErrNotExist)
	}

	// Update state so subsequent reads use the correct path.
	state.TranscriptPath = resolved
	return resolved, nil
}
