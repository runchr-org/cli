// Package kiro implements the Agent interface for Amazon's Kiro CLI.
package kiro

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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

// --- TranscriptAnalyzer interface implementation ---

// errTranscriptEmpty is returned when the transcript file is missing or empty.
var errTranscriptEmpty = errors.New("transcript is empty or missing")

// readAndParseTranscript reads and parses a Kiro transcript file.
// Returns errTranscriptEmpty if the file doesn't exist or is empty.
func readAndParseTranscript(path string) (*kiroTranscript, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Reading from controlled transcript path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errTranscriptEmpty
		}
		return nil, fmt.Errorf("reading transcript: %w", err)
	}
	if len(data) == 0 {
		return nil, errTranscriptEmpty
	}
	t, err := parseTranscript(data)
	if err != nil {
		return nil, fmt.Errorf("parsing transcript: %w", err)
	}
	return t, nil
}

// GetTranscriptPosition returns the number of history entries in a Kiro transcript.
// Kiro uses JSON format with paired user+assistant history entries, so position
// is the entry count. Returns 0 if the file doesn't exist, is empty, or is a
// placeholder "{}".
func (k *KiroAgent) GetTranscriptPosition(path string) (int, error) {
	if path == "" {
		return 0, nil
	}
	t, err := readAndParseTranscript(path)
	if errors.Is(err, errTranscriptEmpty) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(t.History), nil
}

// ExtractModifiedFilesFromOffset extracts files modified since a given history index.
// For Kiro (JSON format), offset is the starting history entry index.
func (k *KiroAgent) ExtractModifiedFilesFromOffset(path string, startOffset int) (files []string, currentPosition int, err error) {
	if path == "" {
		return nil, 0, nil
	}
	t, parseErr := readAndParseTranscript(path)
	if errors.Is(parseErr, errTranscriptEmpty) {
		return nil, 0, nil
	}
	if parseErr != nil {
		return nil, 0, parseErr
	}

	totalEntries := len(t.History)
	if startOffset >= totalEntries {
		return nil, totalEntries, nil
	}

	modifiedFiles := extractModifiedFilesFromHistory(t.History[startOffset:])
	return modifiedFiles, totalEntries, nil
}

// ExtractPrompts extracts user prompts from the transcript starting at the given offset.
// Only Prompt-type user messages are returned; ToolUseResults entries are skipped.
func (k *KiroAgent) ExtractPrompts(sessionRef string, fromOffset int) ([]string, error) {
	t, err := readAndParseTranscript(sessionRef)
	if err != nil {
		return nil, err
	}

	var prompts []string
	for i := fromOffset; i < len(t.History); i++ {
		if prompt := extractUserPrompt(t.History[i].User.Content); prompt != "" {
			prompts = append(prompts, prompt)
		}
	}
	return prompts, nil
}

// ExtractSummary extracts the last assistant response as a session summary.
func (k *KiroAgent) ExtractSummary(sessionRef string) (string, error) {
	t, err := readAndParseTranscript(sessionRef)
	if err != nil {
		return "", err
	}
	return extractLastAssistantResponse(t.History), nil
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

// --- IDE transcript discovery ---

// ideWorkspaceSessionsDir returns the directory where the Kiro IDE stores
// session files for the given working directory. The directory is derived from
// the base64-encoded CWD.
func ideWorkspaceSessionsDir(cwd string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(cwd))

	var baseDir string
	switch runtime.GOOS {
	case "darwin":
		baseDir = filepath.Join(home, "Library", "Application Support", "Kiro", "User", "globalStorage", "kiro.kiroagent", "workspace-sessions")
	default: // linux
		baseDir = filepath.Join(home, ".config", "Kiro", "User", "globalStorage", "kiro.kiroagent", "workspace-sessions")
	}

	return filepath.Join(baseDir, encoded), nil
}

// ensureIDETranscript looks for the most recent Kiro IDE session transcript
// for the given CWD and copies it to .entire/tmp/<sessionID>.json.
// Returns the cache path on success, or empty string if no transcript is found.
func (k *KiroAgent) ensureIDETranscript(ctx context.Context, cwd, sessionID string) (string, error) {
	sessDir, err := ideWorkspaceSessionsDir(cwd)
	if err != nil {
		return "", err
	}

	// Read the sessions.json index.
	indexPath := filepath.Join(sessDir, "sessions.json")
	indexData, err := os.ReadFile(indexPath) //nolint:gosec // Controlled path from IDE storage
	if err != nil {
		logging.Debug(ctx, "kiro: IDE sessions.json not found", "path", indexPath, "err", err)
		return "", fmt.Errorf("IDE sessions.json not found: %w", err)
	}

	var sessions []kiroIDESessionEntry
	if err := json.Unmarshal(indexData, &sessions); err != nil {
		return "", fmt.Errorf("failed to parse IDE sessions.json: %w", err)
	}

	if len(sessions) == 0 {
		return "", errors.New("no IDE sessions found")
	}

	// Sort by dateCreated descending to find the most recent session.
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].DateCreated > sessions[j].DateCreated
	})

	// Read the most recent session's transcript.
	latestSession := sessions[0]
	transcriptPath := filepath.Join(sessDir, latestSession.SessionID+".json")
	transcriptData, err := os.ReadFile(transcriptPath) //nolint:gosec // Controlled path from IDE storage
	if err != nil {
		return "", fmt.Errorf("failed to read IDE transcript %s: %w", transcriptPath, err)
	}

	// Cache the transcript under our session ID.
	repoRoot, rootErr := paths.WorktreeRoot(ctx)
	if rootErr != nil {
		repoRoot = cwd
	}

	cacheDir := filepath.Join(repoRoot, ".entire", "tmp")
	cachePath := filepath.Join(cacheDir, sessionID+".json")

	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create cache dir: %w", err)
	}

	if err := os.WriteFile(cachePath, transcriptData, 0o600); err != nil {
		return "", fmt.Errorf("failed to write cached IDE transcript: %w", err)
	}

	logging.Info(ctx, "kiro: cached IDE transcript",
		"ide_session", latestSession.SessionID,
		"our_session", sessionID,
		"cache_path", cachePath,
	)

	return cachePath, nil
}
