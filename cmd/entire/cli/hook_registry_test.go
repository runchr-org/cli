package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/claudecode"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/logging/loggingtest"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/stretchr/testify/require"

	"github.com/spf13/cobra"
)

const testAgentName = "claude-code"

func TestNewAgentHookVerbCmd_LogsInvocation(t *testing.T) {
	// Setup: Create a temp directory with git repo structure
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Initialize git repo (required for paths.WorktreeRoot to work)
	gitInit := exec.CommandContext(context.Background(), "git", "init")
	if err := gitInit.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Create initial commit so repository is not empty
	gitConfig := exec.CommandContext(context.Background(), "git", "config", "user.email", "test@test.com")
	if err := gitConfig.Run(); err != nil {
		t.Fatalf("failed to configure git user.email: %v", err)
	}
	gitConfigName := exec.CommandContext(context.Background(), "git", "config", "user.name", "Test User")
	if err := gitConfigName.Run(); err != nil {
		t.Fatalf("failed to configure git user.name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatalf("failed to create README: %v", err)
	}
	gitAdd := exec.CommandContext(context.Background(), "git", "add", "README.md")
	if err := gitAdd.Run(); err != nil {
		t.Fatalf("failed to git add: %v", err)
	}
	gitCommit := exec.CommandContext(context.Background(), "git", "commit", "-m", "Initial commit")
	gitCommit.Env = testutil.GitIsolatedEnv()
	if err := gitCommit.Run(); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}

	// Create .entire directory
	entireDir := filepath.Join(tmpDir, paths.EntireDir)
	if err := os.MkdirAll(entireDir, 0o755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}

	// Create settings.json to indicate Entire is set up in this repo
	settingsFile := filepath.Join(entireDir, "settings.json")
	if err := os.WriteFile(settingsFile, []byte(`{"enabled":true,"strategy":"manual-commit"}`), 0o644); err != nil {
		t.Fatalf("failed to create settings file: %v", err)
	}

	// Create logs directory
	logsDir := filepath.Join(entireDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("failed to create logs directory: %v", err)
	}

	// Create session state file in .git/entire-sessions/
	sessionID := "test-claudecode-hook-session"
	writeTestSessionState(t, tmpDir, sessionID)

	// Enable debug logging
	t.Setenv(logging.LogLevelEnvVar, "DEBUG")

	// Inject a buffer-backed logger via loggingtest to capture the hook's
	// log output. In production main.go installs the logger via WithLogger.
	ctx, buf := loggingtest.New(t)

	// Create a transcript file for the hook input
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"user","message":{"content":"test"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("failed to create transcript file: %v", err)
	}

	// Create stdin with session-start hook input
	hookInput := map[string]any{
		"session_id":      sessionID,
		"transcript_path": transcriptPath,
	}
	inputJSON, _ := json.Marshal(hookInput) //nolint:errcheck,errchkjson // Test code; JSON marshal of simple map never fails

	// Create the command with logging - use session-start hook which is a pass-through
	cmd := newAgentHookVerbCmdWithLogging(agent.AgentNameClaudeCode, claudecode.HookNameSessionStart)

	// Set stdin and the test ctx
	cmd.SetContext(ctx)
	cmd.SetIn(bytes.NewReader(inputJSON))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	// Execute the command
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	records := loggingtest.Records(t, buf)
	if len(records) == 0 {
		t.Fatal("expected at least one log record")
	}

	foundInvocation := false
	foundPerfSpan := false
	for _, rec := range records {
		// Hook invocation log: msg contains "invoked", hook="session-start"
		if rec.Attrs["hook"] == claudecode.HookNameSessionStart && strings.Contains(rec.Msg, "invoked") {
			foundInvocation = true
			if rec.Attrs["component"] != "hooks" {
				t.Errorf("expected component='hooks', got %v", rec.Attrs["component"])
			}
		}
		// Perf span log: msg="perf", op="session-start", duration_ms present
		if rec.Msg == "perf" && rec.Attrs["op"] == claudecode.HookNameSessionStart {
			foundPerfSpan = true
			if _, ok := rec.Attrs["duration_ms"]; !ok {
				t.Error("expected duration_ms in perf span log")
			}
		}
	}

	if !foundInvocation {
		t.Error("expected to find hook invocation log")
	}
	if !foundPerfSpan {
		t.Error("expected to find perf span log")
	}
}

func TestClaudeCodeHooksCmd_HasLoggingHooks(t *testing.T) {
	// This test verifies that the claude-code hooks command has PersistentPreRunE
	// and PersistentPostRunE for logging initialization and cleanup

	// Get the actual hooks command which contains the claude-code subcommand
	hooksCmd := newHooksCmd()

	// Find the claude-code subcommand
	var claudeCodeCmd *cobra.Command
	for _, sub := range hooksCmd.Commands() {
		if sub.Use == testAgentName {
			claudeCodeCmd = sub
			break
		}
	}

	require.NotNil(t, claudeCodeCmd, "expected to find claude-code subcommand under hooks")

	// PersistentPreRunE enriches the cobra context with the discovered
	// session ID. The logger and log file are owned by main.go, so no
	// PersistentPostRunE cleanup is needed.
	if claudeCodeCmd.PersistentPreRunE == nil {
		t.Error("expected PersistentPreRunE to be set for ctx enrichment")
	}
}

func TestGeminiCLIHooksCmd_HasLoggingHooks(t *testing.T) {
	// This test verifies that the gemini hooks command has PersistentPreRunE
	// and PersistentPostRunE for logging initialization and cleanup

	// Get the actual hooks command which contains the gemini subcommand
	hooksCmd := newHooksCmd()

	// Find the gemini subcommand
	var geminiCmd *cobra.Command
	for _, sub := range hooksCmd.Commands() {
		if sub.Use == "gemini" {
			geminiCmd = sub
			break
		}
	}

	require.NotNil(t, geminiCmd, "expected to find gemini subcommand under hooks")

	// PersistentPreRunE enriches the cobra context with the discovered
	// session ID. The logger and log file are owned by main.go, so no
	// PersistentPostRunE cleanup is needed.
	if geminiCmd.PersistentPreRunE == nil {
		t.Error("expected PersistentPreRunE to be set for ctx enrichment")
	}
}

func TestHookCommand_SetsCurrentHookAgentName(t *testing.T) {
	// Verify that newAgentHookVerbCmdWithLogging sets currentHookAgentName
	// correctly for the handler, and clears it after

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Initialize git repo (required for paths.WorktreeRoot to work)
	gitInit := exec.CommandContext(context.Background(), "git", "init")
	if err := gitInit.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Create initial commit so repository is not empty
	gitConfig := exec.CommandContext(context.Background(), "git", "config", "user.email", "test@test.com")
	if err := gitConfig.Run(); err != nil {
		t.Fatalf("failed to configure git user.email: %v", err)
	}
	gitConfigName := exec.CommandContext(context.Background(), "git", "config", "user.name", "Test User")
	if err := gitConfigName.Run(); err != nil {
		t.Fatalf("failed to configure git user.name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatalf("failed to create README: %v", err)
	}
	gitAdd := exec.CommandContext(context.Background(), "git", "add", "README.md")
	if err := gitAdd.Run(); err != nil {
		t.Fatalf("failed to git add: %v", err)
	}
	gitCommit := exec.CommandContext(context.Background(), "git", "commit", "-m", "Initial commit")
	gitCommit.Env = testutil.GitIsolatedEnv()
	if err := gitCommit.Run(); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}

	// Create .entire directory to enable Entire
	entireDir := filepath.Join(tmpDir, paths.EntireDir)
	if err := os.MkdirAll(entireDir, 0o755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}

	// Create session state file
	sessionID := "test-agent-name-session"
	writeTestSessionState(t, tmpDir, sessionID)

	// Create transcript file
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"user","message":{"content":"test"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("failed to create transcript file: %v", err)
	}

	// Create stdin input
	hookInput := map[string]interface{}{
		"session_id":      sessionID,
		"transcript_path": transcriptPath,
	}
	inputJSON, _ := json.Marshal(hookInput) //nolint:errcheck,errchkjson // Test code; JSON marshal of simple map never fails

	// Test with Claude Code using session-start hook (pass-through but sets agent name)
	cmd := newAgentHookVerbCmdWithLogging(agent.AgentNameClaudeCode, claudecode.HookNameSessionStart)
	cmd.SetIn(bytes.NewReader(inputJSON))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	// After handler completes, currentHookAgentName should be cleared
	if currentHookAgentName != "" {
		t.Errorf("after handler: currentHookAgentName = %q, want empty", currentHookAgentName)
	}
}

// writeTestSessionState creates a session state file in .git/entire-sessions/ for testing.
func writeTestSessionState(t *testing.T, repoDir, sessionID string) {
	t.Helper()
	stateDir := filepath.Join(repoDir, ".git", session.SessionStateDirName)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("failed to create session state directory: %v", err)
	}

	now := time.Now()
	state := session.State{
		SessionID:           sessionID,
		StartedAt:           now,
		LastInteractionTime: &now,
		Phase:               session.PhaseActive,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	stateFile := filepath.Join(stateDir, sessionID+".json")
	if err := os.WriteFile(stateFile, data, 0o600); err != nil {
		t.Fatalf("failed to write session state file: %v", err)
	}
	t.Cleanup(func() { os.Remove(stateFile) })
}
