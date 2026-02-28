// Package kiro implements the Agent interface for Amazon's Kiro CLI.
package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

//nolint:gochecknoinits // Agent self-registration is the intended pattern
func init() {
	agent.Register(agent.AgentNameKiro, NewKiroAgent)
}

// KiroAgent implements the Agent interface for Amazon's Kiro CLI.
//
//nolint:revive // KiroAgent is clearer than Agent in this context
type KiroAgent struct{}

// NewKiroAgent creates a new Kiro agent instance.
func NewKiroAgent() agent.Agent {
	return &KiroAgent{}
}

// --- Identity ---

func (k *KiroAgent) Name() types.AgentName   { return agent.AgentNameKiro }
func (k *KiroAgent) Type() types.AgentType   { return agent.AgentTypeKiro }
func (k *KiroAgent) Description() string     { return "Kiro - Amazon AI coding CLI" }
func (k *KiroAgent) IsPreview() bool         { return true }
func (k *KiroAgent) ProtectedDirs() []string { return []string{".kiro"} }

// DetectPresence checks if Kiro is configured in the repository.
func (k *KiroAgent) DetectPresence(ctx context.Context) (bool, error) {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		worktreeRoot = "."
	}
	if _, err := os.Stat(filepath.Join(worktreeRoot, ".kiro")); err == nil {
		return true, nil
	}
	return false, nil
}

// --- Transcript Storage ---

// ReadTranscript reads the cached transcript JSON for a session.
func (k *KiroAgent) ReadTranscript(sessionRef string) ([]byte, error) {
	data, err := os.ReadFile(sessionRef) //nolint:gosec // Path from agent hook
	if err != nil {
		return nil, fmt.Errorf("failed to read transcript: %w", err)
	}
	return data, nil
}

// ChunkTranscript splits a JSON transcript into chunks.
// Kiro transcripts are single JSON objects; we use the JSONL chunker since the
// cached format is a JSON blob that fits in a single chunk in most cases.
func (k *KiroAgent) ChunkTranscript(_ context.Context, content []byte, maxSize int) ([][]byte, error) {
	if len(content) <= maxSize {
		return [][]byte{content}, nil
	}
	// For large transcripts, split at line boundaries
	chunks, err := agent.ChunkJSONL(content, maxSize)
	if err != nil {
		return nil, fmt.Errorf("failed to chunk transcript: %w", err)
	}
	return chunks, nil
}

// ReassembleTranscript combines transcript chunks back into a single blob.
func (k *KiroAgent) ReassembleTranscript(chunks [][]byte) ([]byte, error) {
	if len(chunks) == 1 {
		return chunks[0], nil
	}
	return agent.ReassembleJSONL(chunks), nil
}

// --- Session Management ---

// GetSessionID extracts the session ID from hook input.
func (k *KiroAgent) GetSessionID(input *agent.HookInput) string {
	return input.SessionID
}

// GetSessionDir returns the directory where Kiro stores session data.
// For Kiro, sessions are in SQLite, but we cache transcripts to .entire/tmp/.
func (k *KiroAgent) GetSessionDir(repoPath string) (string, error) {
	return filepath.Join(repoPath, ".entire", "tmp"), nil
}

// ResolveSessionFile returns the path to the cached transcript file.
func (k *KiroAgent) ResolveSessionFile(sessionDir, agentSessionID string) string {
	return filepath.Join(sessionDir, agentSessionID+".json")
}

// ReadSession reads session data from the cached transcript.
func (k *KiroAgent) ReadSession(input *agent.HookInput) (*agent.AgentSession, error) {
	if input.SessionRef == "" {
		return nil, errors.New("session reference (transcript path) is required")
	}

	data, err := os.ReadFile(input.SessionRef)
	if err != nil {
		return nil, fmt.Errorf("failed to read transcript: %w", err)
	}

	return &agent.AgentSession{
		SessionID:  input.SessionID,
		AgentName:  k.Name(),
		SessionRef: input.SessionRef,
		StartTime:  time.Now(),
		NativeData: data,
	}, nil
}

// WriteSession writes session data for resumption.
func (k *KiroAgent) WriteSession(_ context.Context, session *agent.AgentSession) error {
	if session == nil {
		return errors.New("session is nil")
	}
	if session.AgentName != "" && session.AgentName != k.Name() {
		return fmt.Errorf("session belongs to agent %q, not %q", session.AgentName, k.Name())
	}
	if session.SessionRef == "" {
		return errors.New("session reference (transcript path) is required")
	}
	if len(session.NativeData) == 0 {
		return errors.New("session has no native data to write")
	}

	if err := os.MkdirAll(filepath.Dir(session.SessionRef), 0o750); err != nil {
		return fmt.Errorf("failed to create session dir: %w", err)
	}

	if err := os.WriteFile(session.SessionRef, session.NativeData, 0o600); err != nil {
		return fmt.Errorf("failed to write transcript: %w", err)
	}
	return nil
}

// FormatResumeCommand returns the command to resume a Kiro session.
func (k *KiroAgent) FormatResumeCommand(_ string) string {
	return "kiro-cli chat --resume"
}

// GetHookConfigPath returns the path to the hook config file relative to repo root.
func (k *KiroAgent) GetHookConfigPath() string {
	return filepath.Join(".kiro", hooksDir, HooksFileName)
}

// --- SQLite helpers ---

// escapeSQLString escapes single quotes for use in SQLite string literals.
func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// --- SQLite session resolution ---

// dbPath returns the path to Kiro's SQLite database.
func dbPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "kiro-cli", "data.sqlite3"), nil
	default: // linux
		return filepath.Join(home, ".local", "share", "kiro-cli", "data.sqlite3"), nil
	}
}

// querySessionID queries the SQLite database for the most recent conversation ID
// associated with the given CWD.
func (k *KiroAgent) querySessionID(ctx context.Context, cwd string) (string, error) {
	if os.Getenv("ENTIRE_TEST_KIRO_MOCK_DB") == "1" {
		return "mock-session-id", nil
	}

	db, err := dbPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(db); err != nil {
		return "", fmt.Errorf("kiro database not found at %s: %w", db, err)
	}

	query := fmt.Sprintf(
		"SELECT json_extract(value, '$.conversation_id') FROM conversations_v2 WHERE key = '%s' ORDER BY updated_at DESC LIMIT 1",
		escapeSQLString(cwd),
	)

	cmd := exec.CommandContext(ctx, "sqlite3", "-json", db, query)
	out, err := cmd.Output()
	if err != nil {
		logging.Warn(ctx, "kiro: sqlite3 query failed", "err", err, "cwd", cwd)
		return "", fmt.Errorf("sqlite3 query failed: %w", err)
	}

	result := strings.TrimSpace(string(out))
	if result == "" || result == "[]" {
		return "", nil
	}

	// sqlite3 -json returns an array of objects
	var rows []map[string]string
	if err := json.Unmarshal([]byte(result), &rows); err != nil {
		return "", fmt.Errorf("failed to parse sqlite3 output: %w", err)
	}
	if len(rows) == 0 {
		return "", nil
	}

	// The column name from json_extract
	for _, v := range rows[0] {
		return v, nil
	}
	return "", nil
}

// ensureCachedTranscript fetches the conversation from SQLite and caches it
// to .entire/tmp/<sessionID>.json. Returns the cache file path.
func (k *KiroAgent) ensureCachedTranscript(ctx context.Context, cwd, sessionID string) (string, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		repoRoot = cwd
	}

	cacheDir := filepath.Join(repoRoot, ".entire", "tmp")
	cachePath := filepath.Join(cacheDir, sessionID+".json")

	// Fetch fresh transcript from SQLite on every call (transcript grows during session)
	if os.Getenv("ENTIRE_TEST_KIRO_MOCK_DB") == "1" {
		// In test mode, return the expected cache path without hitting SQLite.
		return cachePath, nil
	}

	db, err := dbPath()
	if err != nil {
		return "", err
	}

	query := fmt.Sprintf(
		"SELECT value FROM conversations_v2 WHERE key = '%s' ORDER BY updated_at DESC LIMIT 1",
		escapeSQLString(cwd),
	)

	cmd := exec.CommandContext(ctx, "sqlite3", db, query)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("sqlite3 transcript query failed: %w", err)
	}

	transcript := strings.TrimSpace(string(out))
	if transcript == "" {
		return "", errors.New("no transcript found")
	}

	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create cache dir: %w", err)
	}

	if err := os.WriteFile(cachePath, []byte(transcript), 0o600); err != nil {
		return "", fmt.Errorf("failed to write cached transcript: %w", err)
	}

	return cachePath, nil
}
