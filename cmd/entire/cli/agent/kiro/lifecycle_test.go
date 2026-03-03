package kiro

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// --- ParseHookEvent: agent-spawn ---

func TestParseHookEvent_AgentSpawn(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	// Create a minimal git repo so paths.WorktreeRoot works, or rely on fallback to ".".
	// Since we're parallel, we can't chdir. The cache will write to "./.entire/tmp/" relative
	// to the test process's cwd. That's fine: we just verify event fields, not the cache file.

	ag := &KiroAgent{}
	input := `{"hook_event_name":"agentSpawn","cwd":"` + tempDir + `"}`

	event, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(input))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
	}
	if event.Type != agent.SessionStart {
		t.Errorf("expected event type %v, got %v", agent.SessionStart, event.Type)
	}
	if event.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if event.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

// --- ParseHookEvent: user-prompt-submit ---

func TestParseHookEvent_UserPromptSubmit(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}
	input := `{"hook_event_name":"userPromptSubmit","cwd":"/tmp","prompt":"Hello world"}`

	event, err := ag.ParseHookEvent(context.Background(), HookNameUserPromptSubmit, strings.NewReader(input))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
	}
	if event.Type != agent.TurnStart {
		t.Errorf("expected event type %v, got %v", agent.TurnStart, event.Type)
	}
	if event.Prompt != "Hello world" {
		t.Errorf("expected prompt %q, got %q", "Hello world", event.Prompt)
	}
	if event.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if event.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestParseHookEvent_UserPromptSubmit_EmptyPrompt(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}
	input := `{"hook_event_name":"userPromptSubmit","cwd":"/tmp"}`

	event, err := ag.ParseHookEvent(context.Background(), HookNameUserPromptSubmit, strings.NewReader(input))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
	}
	if event.Type != agent.TurnStart {
		t.Errorf("expected event type %v, got %v", agent.TurnStart, event.Type)
	}
	if event.Prompt != "" {
		t.Errorf("expected empty prompt, got %q", event.Prompt)
	}
}

// --- ParseHookEvent: stop ---

func TestParseHookEvent_Stop(t *testing.T) {
	// Set mock DB env to avoid real SQLite access
	t.Setenv("ENTIRE_TEST_KIRO_MOCK_DB", "1")

	ag := &KiroAgent{}

	input := `{"hook_event_name":"stop","cwd":"/tmp"}`

	event, err := ag.ParseHookEvent(context.Background(), HookNameStop, strings.NewReader(input))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
	}
	if event.Type != agent.TurnEnd {
		t.Errorf("expected event type %v, got %v", agent.TurnEnd, event.Type)
	}
	if event.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if event.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestParseHookEvent_Stop_TranscriptRef(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv("ENTIRE_TEST_KIRO_MOCK_DB", "1")

	ag := &KiroAgent{}

	// First, agent-spawn to generate and cache a session ID.
	spawnInput := `{"hook_event_name":"agentSpawn","cwd":"` + tempDir + `"}`
	spawnEvent, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(spawnInput))
	if err != nil {
		t.Fatalf("agent-spawn error: %v", err)
	}

	// Then stop — should set SessionRef to cached transcript path.
	stopInput := `{"hook_event_name":"stop","cwd":"` + tempDir + `"}`
	stopEvent, err := ag.ParseHookEvent(context.Background(), HookNameStop, strings.NewReader(stopInput))
	if err != nil {
		t.Fatalf("stop error: %v", err)
	}

	if stopEvent.SessionRef == "" {
		t.Fatal("stop event SessionRef should not be empty when mock DB is enabled")
	}

	// Verify the path contains the session ID and ends with .json.
	if !strings.Contains(stopEvent.SessionRef, spawnEvent.SessionID) {
		t.Errorf("SessionRef %q does not contain session ID %q", stopEvent.SessionRef, spawnEvent.SessionID)
	}
	if !strings.HasSuffix(stopEvent.SessionRef, ".json") {
		t.Errorf("SessionRef %q does not end with .json", stopEvent.SessionRef)
	}
}

// --- ParseHookEvent: pass-through hooks ---

func TestParseHookEvent_PreToolUse_ReturnsNil(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}
	input := `{"hook_event_name":"preToolUse","cwd":"/tmp","tool_name":"fs_write","tool_input":"{}"}`

	event, err := ag.ParseHookEvent(context.Background(), HookNamePreToolUse, strings.NewReader(input))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Errorf("expected nil event for pre-tool-use, got %+v", event)
	}
}

func TestParseHookEvent_PostToolUse_ReturnsNil(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}
	input := `{"hook_event_name":"postToolUse","cwd":"/tmp","tool_name":"fs_write","tool_input":"{}","tool_response":"ok"}`

	event, err := ag.ParseHookEvent(context.Background(), HookNamePostToolUse, strings.NewReader(input))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Errorf("expected nil event for post-tool-use, got %+v", event)
	}
}

// --- ParseHookEvent: unknown hooks ---

func TestParseHookEvent_UnknownHook_ReturnsNil(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}
	input := `{"hook_event_name":"unknown","cwd":"/tmp"}`

	event, err := ag.ParseHookEvent(context.Background(), "unknown-hook", strings.NewReader(input))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != nil {
		t.Errorf("expected nil event for unknown hook, got %+v", event)
	}
}

// --- ParseHookEvent: error cases ---

func TestParseHookEvent_EmptyInput(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}

	// Empty stdin is valid in IDE mode — returns SessionStart with generated session ID.
	event, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
	}
	if event.Type != agent.SessionStart {
		t.Errorf("expected event type %v, got %v", agent.SessionStart, event.Type)
	}
	if event.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestParseHookEvent_MalformedJSON(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}
	input := `{"hook_event_name": INVALID}`

	_, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(input))

	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse hook input") {
		t.Errorf("expected 'failed to parse hook input' error, got: %v", err)
	}
}

func TestParseHookEvent_EmptyInput_UserPromptSubmit(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}

	// Empty stdin is valid in IDE mode — returns TurnStart with generated session ID.
	event, err := ag.ParseHookEvent(context.Background(), HookNameUserPromptSubmit, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
	}
	if event.Type != agent.TurnStart {
		t.Errorf("expected event type %v, got %v", agent.TurnStart, event.Type)
	}
	if event.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestParseHookEvent_EmptyInput_Stop(t *testing.T) {
	// t.Setenv prevents t.Parallel()
	t.Setenv("ENTIRE_TEST_KIRO_MOCK_DB", "1")

	ag := &KiroAgent{}

	// Empty stdin is valid in IDE mode — returns TurnEnd.
	event, err := ag.ParseHookEvent(context.Background(), HookNameStop, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
	}
	if event.Type != agent.TurnEnd {
		t.Errorf("expected event type %v, got %v", agent.TurnEnd, event.Type)
	}
}

func TestParseHookEvent_MalformedJSON_UserPromptSubmit(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}

	_, err := ag.ParseHookEvent(context.Background(), HookNameUserPromptSubmit, strings.NewReader("{bad json"))

	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestParseHookEvent_MalformedJSON_Stop(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}

	_, err := ag.ParseHookEvent(context.Background(), HookNameStop, strings.NewReader("{bad json"))

	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// --- IDE mode: env var fallback ---

func TestParseHookEvent_UserPromptSubmit_EmptyStdin_ReadsEnvVar(t *testing.T) {
	// t.Setenv prevents t.Parallel()
	t.Setenv("USER_PROMPT", "Hello from the IDE")

	ag := &KiroAgent{}

	event, err := ag.ParseHookEvent(context.Background(), HookNameUserPromptSubmit, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected event, got nil")
	}
	if event.Type != agent.TurnStart {
		t.Errorf("expected event type %v, got %v", agent.TurnStart, event.Type)
	}
	if event.Prompt != "Hello from the IDE" {
		t.Errorf("expected prompt %q, got %q", "Hello from the IDE", event.Prompt)
	}
}

func TestParseHookEvent_UserPromptSubmit_StdinPromptTakesPrecedence(t *testing.T) {
	// t.Setenv prevents t.Parallel()
	t.Setenv("USER_PROMPT", "from env")

	ag := &KiroAgent{}

	// When stdin has a prompt, it takes precedence over the env var
	input := `{"hook_event_name":"userPromptSubmit","cwd":"/tmp","prompt":"from stdin"}`
	event, err := ag.ParseHookEvent(context.Background(), HookNameUserPromptSubmit, strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Prompt != "from stdin" {
		t.Errorf("expected prompt %q (stdin takes precedence), got %q", "from stdin", event.Prompt)
	}
}

func TestParseHookEvent_Stop_EmptyStdin_Succeeds(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv("ENTIRE_TEST_KIRO_MOCK_DB", "1")

	ag := &KiroAgent{}

	// First, agent-spawn to generate and cache a session ID.
	spawnInput := `{"hook_event_name":"agentSpawn","cwd":"` + tempDir + `"}`
	spawnEvent, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(spawnInput))
	if err != nil {
		t.Fatalf("agent-spawn error: %v", err)
	}

	// Then stop with empty stdin (IDE mode) — should still succeed
	stopEvent, err := ag.ParseHookEvent(context.Background(), HookNameStop, strings.NewReader(""))
	if err != nil {
		t.Fatalf("stop error: %v", err)
	}
	if stopEvent == nil {
		t.Fatal("expected event, got nil")
	}
	if stopEvent.Type != agent.TurnEnd {
		t.Errorf("expected event type %v, got %v", agent.TurnEnd, stopEvent.Type)
	}
	if stopEvent.SessionID != spawnEvent.SessionID {
		t.Errorf("stop session ID %q does not match agent-spawn session ID %q",
			stopEvent.SessionID, spawnEvent.SessionID)
	}
}

// --- Table-driven test across all hook types ---

//nolint:tparallel // t.Setenv prevents t.Parallel(); subtests are parallelized
func TestParseHookEvent_AllHookTypes(t *testing.T) {
	t.Setenv("ENTIRE_TEST_KIRO_MOCK_DB", "1")

	testCases := []struct {
		hookName     string
		expectedType agent.EventType
		expectNil    bool
		input        string
	}{
		{
			hookName:     HookNameAgentSpawn,
			expectedType: agent.SessionStart,
			input:        `{"hook_event_name":"agentSpawn","cwd":"/tmp"}`,
		},
		{
			hookName:     HookNameUserPromptSubmit,
			expectedType: agent.TurnStart,
			input:        `{"hook_event_name":"userPromptSubmit","cwd":"/tmp","prompt":"test"}`,
		},
		{
			hookName:     HookNameStop,
			expectedType: agent.TurnEnd,
			input:        `{"hook_event_name":"stop","cwd":"/tmp"}`,
		},
		{
			hookName:  HookNamePreToolUse,
			expectNil: true,
			input:     `{"hook_event_name":"preToolUse","cwd":"/tmp","tool_name":"fs_write"}`,
		},
		{
			hookName:  HookNamePostToolUse,
			expectNil: true,
			input:     `{"hook_event_name":"postToolUse","cwd":"/tmp","tool_name":"fs_write"}`,
		},
		{
			hookName:  "completely-unknown",
			expectNil: true,
			input:     `{"hook_event_name":"unknown","cwd":"/tmp"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.hookName, func(t *testing.T) {
			t.Parallel()

			ag := &KiroAgent{}
			event, err := ag.ParseHookEvent(context.Background(), tc.hookName, strings.NewReader(tc.input))

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.expectNil {
				if event != nil {
					t.Errorf("expected nil event, got %+v", event)
				}
				return
			}

			if event == nil {
				t.Fatal("expected event, got nil")
			}
			if event.Type != tc.expectedType {
				t.Errorf("expected event type %v, got %v", tc.expectedType, event.Type)
			}
			if event.Timestamp.IsZero() {
				t.Error("expected non-zero timestamp")
			}
		})
	}
}

// --- Session ID caching mechanism ---
// These tests use t.Chdir to control where the cache file is written.
// t.Chdir prevents t.Parallel().

func TestSessionIDCaching_AgentSpawnCachesID(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	input := `{"hook_event_name":"agentSpawn","cwd":"` + tempDir + `"}`

	event, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the session ID was cached to disk
	cachePath := filepath.Join(tempDir, ".entire", "tmp", sessionIDFile)
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("failed to read cached session ID: %v", err)
	}

	cachedID := strings.TrimSpace(string(data))
	if cachedID != event.SessionID {
		t.Errorf("cached session ID %q does not match event session ID %q", cachedID, event.SessionID)
	}
}

func TestSessionIDCaching_UserPromptSubmitReadsCache(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// First call agent-spawn to generate and cache a session ID.
	spawnInput := `{"hook_event_name":"agentSpawn","cwd":"` + tempDir + `"}`
	spawnEvent, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(spawnInput))
	if err != nil {
		t.Fatalf("agent-spawn error: %v", err)
	}

	// Then call user-prompt-submit which should read the cached session ID.
	promptInput := `{"hook_event_name":"userPromptSubmit","cwd":"` + tempDir + `","prompt":"test"}`
	promptEvent, err := ag.ParseHookEvent(context.Background(), HookNameUserPromptSubmit, strings.NewReader(promptInput))
	if err != nil {
		t.Fatalf("user-prompt-submit error: %v", err)
	}

	if promptEvent.SessionID != spawnEvent.SessionID {
		t.Errorf("user-prompt-submit session ID %q does not match agent-spawn session ID %q",
			promptEvent.SessionID, spawnEvent.SessionID)
	}
}

func TestSessionIDCaching_StopReadsCache(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv("ENTIRE_TEST_KIRO_MOCK_DB", "1")

	ag := &KiroAgent{}

	// First call agent-spawn to generate and cache a session ID.
	spawnInput := `{"hook_event_name":"agentSpawn","cwd":"` + tempDir + `"}`
	spawnEvent, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(spawnInput))
	if err != nil {
		t.Fatalf("agent-spawn error: %v", err)
	}

	// Then call stop which should read the cached session ID.
	stopInput := `{"hook_event_name":"stop","cwd":"` + tempDir + `"}`
	stopEvent, err := ag.ParseHookEvent(context.Background(), HookNameStop, strings.NewReader(stopInput))
	if err != nil {
		t.Fatalf("stop error: %v", err)
	}

	if stopEvent.SessionID != spawnEvent.SessionID {
		t.Errorf("stop session ID %q does not match agent-spawn session ID %q",
			stopEvent.SessionID, spawnEvent.SessionID)
	}
}

func TestSessionIDCaching_UserPromptSubmitGeneratesNewIDWhenCacheMissing(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Call user-prompt-submit WITHOUT a prior agent-spawn.
	// Should generate a new session ID (fallback behavior).
	input := `{"hook_event_name":"userPromptSubmit","cwd":"` + tempDir + `","prompt":"test"}`
	event, err := ag.ParseHookEvent(context.Background(), HookNameUserPromptSubmit, strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.SessionID == "" {
		t.Error("expected non-empty session ID even without prior agent-spawn")
	}

	// Verify a new cache file was created as fallback.
	cachePath := filepath.Join(tempDir, ".entire", "tmp", sessionIDFile)
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("expected cache file to be created as fallback: %v", err)
	}

	cachedID := strings.TrimSpace(string(data))
	if cachedID != event.SessionID {
		t.Errorf("cached session ID %q does not match event session ID %q", cachedID, event.SessionID)
	}
}

func TestParseStop_PlaceholderTranscript_WhenSQLiteUnavailable(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	// Do NOT set ENTIRE_TEST_KIRO_MOCK_DB — SQLite will fail, triggering placeholder path.

	ag := &KiroAgent{}

	// First, agent-spawn to cache a session ID.
	spawnInput := `{"hook_event_name":"agentSpawn","cwd":"` + tempDir + `"}`
	spawnEvent, err := ag.ParseHookEvent(context.Background(), HookNameAgentSpawn, strings.NewReader(spawnInput))
	if err != nil {
		t.Fatalf("agent-spawn error: %v", err)
	}

	// Stop hook — SQLite unavailable, should create placeholder transcript.
	stopInput := `{"hook_event_name":"stop","cwd":"` + tempDir + `"}`
	stopEvent, err := ag.ParseHookEvent(context.Background(), HookNameStop, strings.NewReader(stopInput))
	if err != nil {
		t.Fatalf("stop error: %v", err)
	}

	// SessionRef must be non-empty (placeholder was created).
	if stopEvent.SessionRef == "" {
		t.Fatal("expected non-empty SessionRef from placeholder transcript")
	}

	// The placeholder file should exist and contain valid JSON.
	data, err := os.ReadFile(stopEvent.SessionRef)
	if err != nil {
		t.Fatalf("failed to read placeholder transcript: %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("expected placeholder content %q, got %q", "{}", string(data))
	}

	// Verify session ID consistency.
	if stopEvent.SessionID != spawnEvent.SessionID {
		t.Errorf("stop session ID %q does not match spawn session ID %q",
			stopEvent.SessionID, spawnEvent.SessionID)
	}
}

func TestSessionIDCaching_StopFallsBackToUnknownWhenNoCacheAndNoSQLite(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	// Do NOT set ENTIRE_TEST_KIRO_MOCK_DB so the SQLite query will fail
	// (sqlite3 won't find the db file).

	ag := &KiroAgent{}

	// Call stop WITHOUT a prior agent-spawn and without mock DB.
	input := `{"hook_event_name":"stop","cwd":"` + tempDir + `"}`
	event, err := ag.ParseHookEvent(context.Background(), HookNameStop, strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.SessionID != "unknown" {
		t.Errorf("expected session ID %q, got %q", "unknown", event.SessionID)
	}
}

// --- generateSessionID ---

func TestGenerateSessionID_Format(t *testing.T) {
	t.Parallel()

	id := generateSessionID()
	// Should be a 32-character hex string (16 bytes * 2 hex chars each).
	if len(id) != 32 {
		t.Errorf("generateSessionID() length = %d, want 32", len(id))
	}
	// Validate all characters are lowercase hex.
	for _, c := range id {
		isDigit := c >= '0' && c <= '9'
		isHexLetter := c >= 'a' && c <= 'f'
		if !isDigit && !isHexLetter {
			t.Errorf("generateSessionID() contains non-hex character %q in %q", string(c), id)
			break
		}
	}
}

func TestGenerateSessionID_Unique(t *testing.T) {
	t.Parallel()

	ids := make(map[string]bool)
	for range 100 {
		id := generateSessionID()
		if ids[id] {
			t.Fatalf("generateSessionID() produced duplicate ID: %s", id)
		}
		ids[id] = true
	}
}
