// Package pi implements the Agent interface for the pi coding agent
// (https://github.com/earendil-works/pi-mono).
// The npm package the embedded extension imports a type from is
// `@earendil-works/pi-coding-agent`.
//
// This is an in-tree port of the previously-external entire-agent-pi plugin
// (github.com/entireio/external-agents/agents/entire-agent-pi). The behaviour
// matches the external version — most notably the active-branch resolution
// for Pi's tree-shaped sessions — but the integration is plumbed directly
// through the in-tree Agent / HookSupport / TokenCalculator / TranscriptAnalyzer
// interfaces rather than the external JSON-over-stdio protocol.
package pi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

//nolint:gochecknoinits // Agent self-registration is the intended pattern
func init() {
	agent.Register(agent.AgentNamePi, NewPiAgent)
}

// PiAgent implements agent.Agent for the pi coding agent.
//
//nolint:revive // PiAgent is clearer than Agent in this context
type PiAgent struct{}

// NewPiAgent returns a new Pi agent instance.
func NewPiAgent() agent.Agent {
	return &PiAgent{}
}

// --- Identity ---

func (a *PiAgent) Name() types.AgentName    { return agent.AgentNamePi }
func (a *PiAgent) Type() types.AgentType    { return agent.AgentTypePi }
func (a *PiAgent) Description() string      { return "Pi coding agent integration for Entire" }
func (a *PiAgent) IsPreview() bool          { return true }
func (a *PiAgent) ProtectedDirs() []string  { return []string{".pi"} }
func (a *PiAgent) ProtectedFiles() []string { return nil }

// DetectPresence reports whether pi is configured for *this repo*. We only
// check repo-local config (.pi/) and intentionally ignore $PATH — in-tree
// agents follow the convention used by Claude/Gemini/OpenCode where
// detection means "this repo is set up for this agent", not "this agent is
// installed somewhere on this machine". The external plugin uses the broader
// $PATH check because it can't see repo state; we don't have that limitation.
func (a *PiAgent) DetectPresence(ctx context.Context) (bool, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		repoRoot = "."
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".pi")); err == nil {
		return true, nil
	}
	return false, nil
}

// --- Transcript Storage (chunking) ---

// ReadTranscript reads a captured Pi JSONL session transcript from disk.
// SessionRef is the absolute path returned by captureTranscript().
func (a *PiAgent) ReadTranscript(sessionRef string) ([]byte, error) {
	if sessionRef == "" {
		return nil, errors.New("empty session ref")
	}
	//nolint:gosec // SessionRef from validated lifecycle hook input
	data, err := os.ReadFile(sessionRef)
	if err != nil {
		return nil, fmt.Errorf("read pi transcript %s: %w", sessionRef, err)
	}
	return data, nil
}

// ChunkTranscript splits a Pi JSONL transcript at line boundaries.
func (a *PiAgent) ChunkTranscript(_ context.Context, content []byte, maxSize int) ([][]byte, error) {
	chunks, err := agent.ChunkJSONL(content, maxSize)
	if err != nil {
		return nil, fmt.Errorf("chunk pi transcript: %w", err)
	}
	return chunks, nil
}

// ReassembleTranscript concatenates JSONL chunks with newlines.
func (a *PiAgent) ReassembleTranscript(chunks [][]byte) ([]byte, error) {
	return agent.ReassembleJSONL(chunks), nil
}

// --- Legacy methods ---

// GetSessionID extracts the session ID from a hook input.
func (a *PiAgent) GetSessionID(input *agent.HookInput) string {
	if input == nil {
		return ""
	}
	return input.SessionID
}

// piSessionSubdir is the per-agent subdirectory under .entire/tmp/ where
// Pi captures session transcripts. This MUST be agent-specific (not just
// .entire/tmp) — the framework's AgentForTranscriptPath iterates every
// agent's session dir to identify the owner of a transcript path, and a
// broader claim would shadow the .entire/tmp/ paths used by other agents'
// integration tests and tooling.
const piSessionSubdir = "pi"

// GetSessionDir returns the directory where Entire stages Pi session
// transcripts. The Pi extension forwards Pi's native JSONL path on every
// hook event; on agent_end we copy the file into
// <repo>/.entire/tmp/pi/<id>.json so condensation can find it
// deterministically and survive Pi sessions being deleted by the user.
//
// When repoPath is empty we resolve the worktree root (so callers running
// from a subdirectory still get the repo-local staging path) and only fall
// back to os.Getwd() if no git repo is reachable.
func (a *PiAgent) GetSessionDir(repoPath string) (string, error) {
	if repoPath == "" {
		root, err := paths.WorktreeRoot(context.Background())
		if err == nil {
			repoPath = root
		} else {
			logging.Debug(context.Background(), "pi: GetSessionDir falling back to cwd",
				slog.String("err", err.Error()))
			//nolint:forbidigo // last-resort fallback when no git repo (tests outside repos)
			wd, wdErr := os.Getwd()
			if wdErr != nil {
				return "", fmt.Errorf("resolve repo root or cwd for pi session dir: %w", wdErr)
			}
			repoPath = wd
		}
	}
	return filepath.Join(repoPath, paths.EntireTmpDir, piSessionSubdir), nil
}

// ResolveSessionFile returns the full path to a captured Pi session JSONL
// file for the given session ID inside sessionDir.
func (a *PiAgent) ResolveSessionFile(sessionDir, agentSessionID string) string {
	return filepath.Join(sessionDir, agentSessionID+".json")
}

// ReadSession loads a captured Pi transcript and returns it as an AgentSession.
func (a *PiAgent) ReadSession(input *agent.HookInput) (*agent.AgentSession, error) {
	if input == nil || input.SessionRef == "" {
		return nil, errors.New("no session ref provided")
	}

	data, err := os.ReadFile(input.SessionRef)
	if err != nil {
		return nil, fmt.Errorf("read pi session: %w", err)
	}
	return &agent.AgentSession{
		AgentName:  a.Name(),
		SessionID:  input.SessionID,
		SessionRef: input.SessionRef,
		NativeData: data,
	}, nil
}

// WriteSession writes a captured Pi transcript back to disk so Pi can resume
// from it. Pi loads sessions from arbitrary paths via `pi --session <path>`,
// so a plain write is sufficient.
func (a *PiAgent) WriteSession(_ context.Context, session *agent.AgentSession) error {
	if session == nil {
		return errors.New("nil session")
	}
	if session.SessionRef == "" {
		return errors.New("session has empty SessionRef")
	}
	if len(session.NativeData) == 0 {
		return errors.New("session has empty NativeData")
	}
	if err := os.MkdirAll(filepath.Dir(session.SessionRef), 0o750); err != nil {
		return fmt.Errorf("create pi session dir: %w", err)
	}

	if err := os.WriteFile(session.SessionRef, session.NativeData, 0o600); err != nil {
		return fmt.Errorf("write pi session file: %w", err)
	}
	return nil
}

// FormatResumeCommand returns the shell command to resume a specific Pi
// session by ID. Pi accepts a partial UUID via `pi --session <id>`. When no
// session is specified, fall back to `pi --continue` which reopens the most
// recent session.
func (a *PiAgent) FormatResumeCommand(sessionID string) string {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return "pi --continue"
	}
	return "pi --session " + id
}
