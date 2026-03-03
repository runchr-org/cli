package kiro

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

// Compile-time interface assertion for TranscriptAnalyzer.
var _ agent.TranscriptAnalyzer = (*KiroAgent)(nil)

// Kiro hook names — these become CLI subcommands under `entire hooks kiro`.
// Kiro uses camelCase hook names natively, but CLI subcommands use kebab-case.
const (
	HookNameAgentSpawn       = "agent-spawn"
	HookNameUserPromptSubmit = "user-prompt-submit"
	HookNamePreToolUse       = "pre-tool-use"
	HookNamePostToolUse      = "post-tool-use"
	HookNameStop             = "stop"
)

// sessionIDFile is the filename for caching the generated session ID.
// Stored in .entire/tmp/ to ensure a stable ID across all hooks in one session.
const sessionIDFile = "kiro-active-session"

// HookNames returns the hook verbs Kiro supports.
// These become subcommands: entire hooks kiro <verb>
func (k *KiroAgent) HookNames() []string {
	return []string{
		HookNameAgentSpawn,
		HookNameUserPromptSubmit,
		HookNamePreToolUse,
		HookNamePostToolUse,
		HookNameStop,
	}
}

// ParseHookEvent translates a Kiro hook into a normalized lifecycle Event.
// Returns nil for hooks with no lifecycle significance (preToolUse, postToolUse).
func (k *KiroAgent) ParseHookEvent(ctx context.Context, hookName string, stdin io.Reader) (*agent.Event, error) {
	switch hookName {
	case HookNameAgentSpawn:
		return k.parseAgentSpawn(ctx, stdin)
	case HookNameUserPromptSubmit:
		return k.parseUserPromptSubmit(ctx, stdin)
	case HookNameStop:
		return k.parseStop(ctx, stdin)
	case HookNamePreToolUse, HookNamePostToolUse:
		// Pass-through hooks with no lifecycle significance
		return nil, nil //nolint:nilnil // nil event = no lifecycle action
	default:
		return nil, nil //nolint:nilnil // Unknown hooks have no lifecycle action
	}
}

// readHookInputOrEmpty wraps ReadAndParseHookInput, returning a zero-value
// hookInputRaw on empty stdin instead of erroring. This supports the Kiro IDE
// which delivers data via environment variables rather than JSON on stdin.
// Malformed JSON still returns an error.
func readHookInputOrEmpty(stdin io.Reader) (*hookInputRaw, error) {
	raw, err := agent.ReadAndParseHookInput[hookInputRaw](stdin)
	if err != nil {
		if errors.Is(err, agent.ErrEmptyHookInput) {
			return &hookInputRaw{}, nil
		}
		return nil, err
	}
	return raw, nil
}

func (k *KiroAgent) parseAgentSpawn(ctx context.Context, stdin io.Reader) (*agent.Event, error) {
	_, err := readHookInputOrEmpty(stdin)
	if err != nil {
		return nil, err
	}

	// Generate a new stable session ID and cache it for subsequent hooks.
	// Kiro's SQLite transcript isn't populated until the turn ends, so we can't
	// use the native conversation_id as our session ID — it would be "unknown"
	// at agentSpawn/userPromptSubmit and change to the real ID at stop.
	sessionID := k.generateAndCacheSessionID(ctx)

	return &agent.Event{
		Type:      agent.SessionStart,
		SessionID: sessionID,
		Timestamp: time.Now(),
	}, nil
}

func (k *KiroAgent) parseUserPromptSubmit(ctx context.Context, stdin io.Reader) (*agent.Event, error) {
	raw, err := readHookInputOrEmpty(stdin)
	if err != nil {
		return nil, err
	}

	// IDE mode: prompt comes from USER_PROMPT env var when stdin is empty.
	prompt := raw.Prompt
	if prompt == "" {
		prompt = os.Getenv("USER_PROMPT")
	}

	// Read the stable session ID generated at agentSpawn.
	sessionID := k.readCachedSessionID(ctx)
	if sessionID == "" {
		// Fallback: generate new ID if cache file is missing (e.g., agentSpawn was skipped).
		sessionID = k.generateAndCacheSessionID(ctx)
	}

	return &agent.Event{
		Type:      agent.TurnStart,
		SessionID: sessionID,
		Prompt:    prompt,
		Timestamp: time.Now(),
	}, nil
}

func (k *KiroAgent) parseStop(ctx context.Context, stdin io.Reader) (*agent.Event, error) {
	raw, err := readHookInputOrEmpty(stdin)
	if err != nil {
		return nil, err
	}

	// IDE mode: CWD may not be in stdin. Fall back to repo root.
	cwd := raw.CWD
	if cwd == "" {
		repoRoot, rootErr := paths.WorktreeRoot(ctx)
		if rootErr == nil {
			cwd = repoRoot
		}
	}

	// Read the stable session ID generated at agentSpawn.
	sessionID := k.readCachedSessionID(ctx)
	if sessionID == "" {
		// Fallback: try SQLite for the session ID.
		sid, queryErr := k.querySessionID(ctx, cwd)
		if queryErr != nil || sid == "" {
			sessionID = "unknown"
		} else {
			sessionID = sid
		}
	}

	// At stop, Kiro's SQLite transcript is available. Fetch and cache it
	// under our stable session ID so lifecycle.go can read it.
	sessionRef, _ := k.ensureCachedTranscript(ctx, cwd, sessionID) //nolint:errcheck // best-effort: fall back to placeholder

	// IDE mode: SQLite may not exist or be at a different path. Create a
	// minimal placeholder transcript so the lifecycle handler can proceed
	// (file-diff checkpoints still work without a real transcript).
	if sessionRef == "" {
		sessionRef = k.createPlaceholderTranscript(ctx, cwd, sessionID)
	}

	return &agent.Event{
		Type:       agent.TurnEnd,
		SessionID:  sessionID,
		SessionRef: sessionRef,
		Timestamp:  time.Now(),
	}, nil
}

// createPlaceholderTranscript writes a minimal JSON transcript to .entire/tmp/
// so the lifecycle handler can proceed when the real transcript is unavailable
// (e.g., Kiro IDE mode where SQLite is at a different path).
func (k *KiroAgent) createPlaceholderTranscript(ctx context.Context, cwd, sessionID string) string {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		repoRoot = cwd
	}
	if repoRoot == "" {
		return ""
	}
	cacheDir := filepath.Join(repoRoot, ".entire", "tmp")
	cachePath := filepath.Join(cacheDir, sessionID+".json")
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		logging.Warn(ctx, "kiro: failed to create transcript cache dir", "err", err)
		return ""
	}
	if err := os.WriteFile(cachePath, []byte("{}"), 0o600); err != nil {
		logging.Warn(ctx, "kiro: failed to write placeholder transcript", "err", err)
		return ""
	}
	return cachePath
}

// generateAndCacheSessionID creates a new random session ID and writes it
// to .entire/tmp/kiro-active-session for subsequent hooks to read.
func (k *KiroAgent) generateAndCacheSessionID(ctx context.Context) string {
	sid := generateSessionID()
	cachePath := k.sessionIDCachePath(ctx)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o750); err != nil {
		logging.Warn(ctx, "kiro: failed to create session ID cache dir", "err", err)
		return sid
	}
	if err := os.WriteFile(cachePath, []byte(sid), 0o600); err != nil {
		logging.Warn(ctx, "kiro: failed to write session ID cache", "err", err)
	}
	return sid
}

// readCachedSessionID reads the stable session ID from .entire/tmp/kiro-active-session.
// Returns empty string if the cache file doesn't exist.
func (k *KiroAgent) readCachedSessionID(ctx context.Context) string {
	cachePath := k.sessionIDCachePath(ctx)
	data, err := os.ReadFile(cachePath) //nolint:gosec // cachePath is constructed from WorktreeRoot + constant suffix
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// sessionIDCachePath returns the path to the session ID cache file.
func (k *KiroAgent) sessionIDCachePath(ctx context.Context) string {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		repoRoot = "."
	}
	return filepath.Join(repoRoot, ".entire", "tmp", sessionIDFile)
}

// generateSessionID creates a random 32-character hex string for use as a session ID.
func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on supported platforms, but use timestamp fallback.
		return "kiro-" + time.Now().Format("20060102-150405")
	}
	return hex.EncodeToString(b)
}
