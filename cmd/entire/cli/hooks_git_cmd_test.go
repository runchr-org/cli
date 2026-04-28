package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
)

func TestEnrichHookContext_AttachesSessionID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	gitInit := exec.CommandContext(context.Background(), "git", "init")
	gitInit.Dir = tmpDir
	if err := gitInit.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	entireDir := filepath.Join(tmpDir, paths.EntireDir)
	if err := os.MkdirAll(entireDir, 0o755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}
	settingsFile := filepath.Join(entireDir, "settings.json")
	if err := os.WriteFile(settingsFile, []byte(`{"enabled":true,"strategy":"manual-commit"}`), 0o644); err != nil {
		t.Fatalf("failed to create settings file: %v", err)
	}

	sessionID := "test-session-12345"
	stateDir := filepath.Join(tmpDir, ".git", session.SessionStateDirName)
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
	defer os.Remove(stateFile)

	enriched := enrichHookContext(context.Background())
	if got := logging.SessionIDFromContext(enriched); got != sessionID {
		t.Errorf("enriched ctx session = %q, want %q", got, sessionID)
	}

	// Enrichment must not create the logs directory — file lifecycle is owned
	// by main.go's lazy writer.
	logsDir := filepath.Join(entireDir, "logs")
	if _, err := os.Stat(logsDir); !os.IsNotExist(err) {
		t.Errorf("enrichHookContext should not create %s", logsDir)
	}
}

// TestEnrichHookContext_NoOpWhenNotSetUp asserts enrichment leaves ctx
// untouched when Entire is not set up in the repo.
func TestEnrichHookContext_NoOpWhenNotSetUp(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	gitInit := exec.CommandContext(context.Background(), "git", "init")
	gitInit.Dir = tmpDir
	if err := gitInit.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	enriched := enrichHookContext(context.Background())
	if got := logging.SessionIDFromContext(enriched); got != "" {
		t.Errorf("expected empty session in unset-up repo, got %q", got)
	}

	logsDir := filepath.Join(tmpDir, ".entire", "logs")
	if _, err := os.Stat(logsDir); !os.IsNotExist(err) {
		t.Errorf("expected .entire/logs to NOT be created, but it exists")
	}
}

// TestEnrichHookContext_NoOpWhenDisabled asserts enrichment is a no-op when
// Entire is set up but disabled.
func TestEnrichHookContext_NoOpWhenDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	gitInit := exec.CommandContext(context.Background(), "git", "init")
	gitInit.Dir = tmpDir
	if err := gitInit.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	entireDir := filepath.Join(tmpDir, paths.EntireDir)
	if err := os.MkdirAll(entireDir, 0o755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}
	settingsFile := filepath.Join(entireDir, "settings.json")
	if err := os.WriteFile(settingsFile, []byte(`{"enabled":false,"strategy":"manual-commit"}`), 0o644); err != nil {
		t.Fatalf("failed to create settings file: %v", err)
	}

	enriched := enrichHookContext(context.Background())
	if got := logging.SessionIDFromContext(enriched); got != "" {
		t.Errorf("expected empty session when disabled, got %q", got)
	}

	logsDir := filepath.Join(tmpDir, ".entire", "logs")
	if _, err := os.Stat(logsDir); !os.IsNotExist(err) {
		t.Errorf("expected .entire/logs to NOT be created when disabled, but it exists")
	}
}

// TestHooksGitCmd_DiscoverExternalAgents_WhenEnabled verifies that when Entire is set up
// and enabled, PersistentPreRunE calls external.DiscoverAndRegister so that external
// agents are available during hook execution (e.g. post-commit condensation).
func TestHooksGitCmd_DiscoverExternalAgents_WhenEnabled(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	tmpDir := t.TempDir()

	// Initialize git repo first, then chdir so paths cache is correct
	gitInit := exec.CommandContext(context.Background(), "git", "init")
	gitInit.Dir = tmpDir
	if err := gitInit.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	session.ClearGitCommonDirCache()

	// Reset global state before the test
	gitHooksDisabled = false

	// Create .entire/settings.json with enabled: true and external_agents: true
	entireDir := filepath.Join(tmpDir, paths.EntireDir)
	if err := os.MkdirAll(entireDir, 0o755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}
	settingsFile := filepath.Join(entireDir, "settings.json")
	if err := os.WriteFile(settingsFile, []byte(`{"enabled":true,"external_agents":true}`), 0o644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	// Create a mock external agent binary in a temp PATH directory.
	// Use a unique name to avoid conflicts with agents registered by other tests.
	agentName := types.AgentName("hooktest-discovery-agent")
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "entire-agent-"+string(agentName))
	infoJSON := `{
  "protocol_version": 1,
  "name": "` + string(agentName) + `",
  "type": "Hook Test Agent",
  "description": "Agent for hook discovery test",
  "is_preview": false,
  "protected_dirs": [],
  "hook_names": [],
  "capabilities": {}
}`
	script := "#!/bin/sh\nif [ \"$1\" = \"info\" ]; then\n  echo '" + infoJSON + "'\nfi\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write mock agent binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Execute the git hook command (post-commit) so PersistentPreRunE runs
	cmd := newHooksGitCmd()
	cmd.SetArgs([]string{"post-commit"})
	ctx := context.Background()
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("git hook command failed: %v", err)
	}

	// PersistentPreRunE should not have disabled hooks
	if gitHooksDisabled {
		t.Fatal("gitHooksDisabled should be false when Entire is enabled")
	}

	// The external agent should have been discovered and registered in the agent registry,
	// confirming that DiscoverAndRegister was called during PersistentPreRunE.
	if _, err := agent.Get(agentName); err != nil {
		t.Errorf("expected external agent %q to be registered after hook pre-run, got: %v", agentName, err)
	}
}

func TestHooksGitCmd_ExposesPostRewriteSubcommand(t *testing.T) {
	t.Parallel()

	cmd := newHooksGitCmd()
	found, _, err := cmd.Find([]string{"post-rewrite"})
	if err != nil {
		t.Fatalf("could not find post-rewrite subcommand: %v", err)
	}
	if found == nil {
		t.Fatal("expected post-rewrite subcommand, got nil")
		return
	}
	if found.Use != "post-rewrite <rewrite-type>" {
		t.Fatalf("post-rewrite Use = %q, want %q", found.Use, "post-rewrite <rewrite-type>")
	}
}
