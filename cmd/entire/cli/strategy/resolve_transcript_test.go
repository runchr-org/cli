package strategy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/session"

	// Register agents so GetByAgentType works in tests.
	_ "github.com/entireio/cli/cmd/entire/cli/agent/claudecode"
	_ "github.com/entireio/cli/cmd/entire/cli/agent/copilotcli"
	_ "github.com/entireio/cli/cmd/entire/cli/agent/cursor"
)

func TestTranscriptFileExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	regularFile := filepath.Join(tmpDir, "events.jsonl")
	if err := os.WriteFile(regularFile, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	dirPath := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(dirPath, 0o750); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	symlinkPath := filepath.Join(tmpDir, "link.jsonl")
	if err := os.Symlink(regularFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"empty path", "", false},
		{"regular file", regularFile, true},
		{"nonexistent", filepath.Join(tmpDir, "missing.jsonl"), false},
		{"directory", dirPath, false},
		{"symlink", symlinkPath, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := transcriptFileExists(tt.path); got != tt.want {
				t.Errorf("transcriptFileExists(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsRegularFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	regularFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(regularFile, []byte("data"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	dirPath := filepath.Join(tmpDir, "dir")
	if err := os.MkdirAll(dirPath, 0o750); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	symlinkToFile := filepath.Join(tmpDir, "sym-file")
	if err := os.Symlink(regularFile, symlinkToFile); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	symlinkToDir := filepath.Join(tmpDir, "sym-dir")
	if err := os.Symlink(dirPath, symlinkToDir); err != nil {
		t.Fatalf("failed to create symlink to dir: %v", err)
	}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"regular file", regularFile, true},
		{"nonexistent", filepath.Join(tmpDir, "nope"), false},
		{"directory", dirPath, false},
		{"symlink to file", symlinkToFile, false},
		{"symlink to dir", symlinkToDir, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isRegularFile(tt.path); got != tt.want {
				t.Errorf("isRegularFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestResolveTranscriptPath_FileExists(t *testing.T) {
	t.Parallel()

	// When the transcript file exists at the stored path, resolveTranscriptPath
	// should return that path unchanged.
	tmpDir := t.TempDir()
	transcriptFile := filepath.Join(tmpDir, "session-123.jsonl")
	if err := os.WriteFile(transcriptFile, []byte(`{"test":true}`), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	state := &session.State{
		TranscriptPath: transcriptFile,
		AgentType:      agent.AgentTypeCursor,
	}

	resolved, err := resolveTranscriptPath(state)
	if err != nil {
		t.Fatalf("resolveTranscriptPath() error = %v", err)
	}
	if resolved != transcriptFile {
		t.Errorf("resolveTranscriptPath() = %q, want %q", resolved, transcriptFile)
	}
	if state.TranscriptPath != transcriptFile {
		t.Errorf("state.TranscriptPath changed to %q, should remain %q", state.TranscriptPath, transcriptFile)
	}
}

func TestResolveTranscriptPath_ReResolvesToNestedLayout(t *testing.T) {
	t.Parallel()

	// Simulates Cursor CLI relocating a transcript mid-session:
	// Stored path (flat):  <dir>/<uuid>.jsonl  (does not exist)
	// Actual path (nested): <dir>/<uuid>/<uuid>.jsonl  (exists)
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "agent-transcripts")
	if err := os.MkdirAll(sessionDir, 0o750); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	agentSessionID := "87874108-eff2-47a0-b260-183961dd6cb0"
	flatPath := filepath.Join(sessionDir, agentSessionID+".jsonl")

	// Create the file at the nested path only
	nestedDir := filepath.Join(sessionDir, agentSessionID)
	if err := os.MkdirAll(nestedDir, 0o750); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}
	nestedPath := filepath.Join(nestedDir, agentSessionID+".jsonl")
	if err := os.WriteFile(nestedPath, []byte(`{"role":"user"}`), 0o600); err != nil {
		t.Fatalf("failed to write nested transcript: %v", err)
	}

	state := &session.State{
		TranscriptPath: flatPath,
		AgentType:      agent.AgentTypeCursor,
	}

	resolved, err := resolveTranscriptPath(state)
	if err != nil {
		t.Fatalf("resolveTranscriptPath() error = %v", err)
	}
	if resolved != nestedPath {
		t.Errorf("resolveTranscriptPath() = %q, want %q", resolved, nestedPath)
	}
	// State should be updated to the resolved path
	if state.TranscriptPath != nestedPath {
		t.Errorf("state.TranscriptPath = %q, want %q", state.TranscriptPath, nestedPath)
	}
}

func TestResolveTranscriptPath_FileNotFoundAndCannotResolve(t *testing.T) {
	t.Parallel()

	// When the file doesn't exist and re-resolution also fails, the original
	// not-found error should be returned.
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "agent-transcripts")
	if err := os.MkdirAll(sessionDir, 0o750); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	agentSessionID := "nonexistent-uuid"
	flatPath := filepath.Join(sessionDir, agentSessionID+".jsonl")

	state := &session.State{
		TranscriptPath: flatPath,
		AgentType:      agent.AgentTypeCursor,
	}

	_, err := resolveTranscriptPath(state)
	if err == nil {
		t.Fatal("resolveTranscriptPath() expected error, got nil")
	}
	// State should not change
	if state.TranscriptPath != flatPath {
		t.Errorf("state.TranscriptPath changed to %q, should remain %q", state.TranscriptPath, flatPath)
	}
}

func TestResolveTranscriptPath_UnknownAgentFallsThrough(t *testing.T) {
	t.Parallel()

	// When the agent type is unknown, re-resolution should not be attempted,
	// and the original not-found error should be returned.
	tmpDir := t.TempDir()
	missingPath := filepath.Join(tmpDir, "nonexistent.jsonl")

	state := &session.State{
		TranscriptPath: missingPath,
		AgentType:      "Unknown Agent",
	}

	_, err := resolveTranscriptPath(state)
	if err == nil {
		t.Fatal("resolveTranscriptPath() expected error, got nil")
	}
}

func TestResolveTranscriptPath_EmptyPath(t *testing.T) {
	t.Parallel()

	state := &session.State{
		TranscriptPath: "",
		AgentType:      agent.AgentTypeCursor,
	}

	_, err := resolveTranscriptPath(state)
	if err == nil {
		t.Fatal("resolveTranscriptPath() expected error for empty path, got nil")
	}
}

func TestResolveTranscriptPath_DirectoryPathTriggersReResolution(t *testing.T) {
	t.Parallel()

	// When the stored path is a directory (not a regular file), the fast path
	// should not match. Re-resolution should be attempted and, when it also
	// fails, an error is returned.
	tmpDir := t.TempDir()
	dirPath := filepath.Join(tmpDir, "adir")
	if err := os.MkdirAll(dirPath, 0o750); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	state := &session.State{
		TranscriptPath: dirPath,
		AgentType:      agent.AgentTypeCursor,
	}

	_, err := resolveTranscriptPath(state)
	if err == nil {
		t.Fatal("resolveTranscriptPath() expected error for directory path, got nil")
	}
}

func TestResolveTranscriptPath_SymlinkPathTriggersReResolution(t *testing.T) {
	t.Parallel()

	// A symlink at the stored path should not satisfy the fast path (os.Lstat
	// reports the symlink itself, not the target). Re-resolution kicks in.
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "real.jsonl")
	if err := os.WriteFile(realFile, []byte(`{"test":true}`), 0o600); err != nil {
		t.Fatalf("failed to write real file: %v", err)
	}
	symlinkPath := filepath.Join(tmpDir, "link.jsonl")
	if err := os.Symlink(realFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	state := &session.State{
		TranscriptPath: symlinkPath,
		AgentType:      agent.AgentTypeCursor,
	}

	_, err := resolveTranscriptPath(state)
	if err == nil {
		t.Fatal("resolveTranscriptPath() expected error for symlink path, got nil")
	}
}

func TestResolveTranscriptPath_CopilotSessionDirFallback(t *testing.T) {
	// Simulates cloud Copilot: stored path points to a container location,
	// but COPILOT_SESSION_STATE_DIR points to the host-mapped directory.
	hostDir := t.TempDir()
	sessionID := "cloud-session-uuid"

	nestedDir := filepath.Join(hostDir, sessionID)
	if err := os.MkdirAll(nestedDir, 0o750); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	hostPath := filepath.Join(nestedDir, "events.jsonl")
	if err := os.WriteFile(hostPath, []byte(`{"test":true}`), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	t.Setenv("ENTIRE_TEST_COPILOT_SESSION_DIR", "")
	t.Setenv("COPILOT_SESSION_STATE_DIR", hostDir)

	containerPath := "/home/runner/.copilot/session-state/" + sessionID + "/events.jsonl"
	state := &session.State{
		SessionID:      sessionID,
		TranscriptPath: containerPath,
		AgentType:      agent.AgentTypeCopilotCLI,
	}

	resolved, err := resolveTranscriptPath(state)
	if err != nil {
		t.Fatalf("resolveTranscriptPath() error = %v", err)
	}
	if resolved != hostPath {
		t.Errorf("resolveTranscriptPath() = %q, want %q", resolved, hostPath)
	}
	if state.TranscriptPath != hostPath {
		t.Errorf("state.TranscriptPath = %q, want %q", state.TranscriptPath, hostPath)
	}
}

func TestResolveTranscriptPath_CopilotNoSessionDirFallsThrough(t *testing.T) {
	t.Parallel()

	// When COPILOT_SESSION_STATE_DIR is not set and the container path doesn't
	// exist, resolveTranscriptPath should return an error.
	state := &session.State{
		SessionID:      "missing-session",
		TranscriptPath: "/container/.copilot/session-state/missing-session/events.jsonl",
		AgentType:      agent.AgentTypeCopilotCLI,
	}

	_, err := resolveTranscriptPath(state)
	if err == nil {
		t.Fatal("resolveTranscriptPath() expected error, got nil")
	}
}

func TestResolveTranscriptPath_ClaudeCodeNoReResolution(t *testing.T) {
	t.Parallel()

	// Claude Code transcripts don't relocate mid-session. resolveTranscriptPath
	// should still attempt re-resolution (the agent's ResolveSessionFile handles it).
	// This test verifies the function works generically for any agent.
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "projects", "sessions")
	if err := os.MkdirAll(sessionDir, 0o750); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	missingPath := filepath.Join(sessionDir, "session-abc.jsonl")

	state := &session.State{
		TranscriptPath: missingPath,
		AgentType:      agent.AgentTypeClaudeCode,
	}

	_, err := resolveTranscriptPath(state)
	if err == nil {
		t.Fatal("resolveTranscriptPath() expected error, got nil")
	}
	// Path should remain unchanged since re-resolution also fails
	if state.TranscriptPath != missingPath {
		t.Errorf("state.TranscriptPath = %q, want %q", state.TranscriptPath, missingPath)
	}
}
