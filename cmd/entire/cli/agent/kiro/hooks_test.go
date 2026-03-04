package kiro

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- InstallHooks ---

func TestInstallHooks_FreshInstall(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	count, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}
	if count != 9 {
		t.Errorf("InstallHooks() returned count %d, want 9 (5 CLI + 4 IDE)", count)
	}

	file := readKiroAgentFile(t, tempDir)

	if file.Name != "entire" {
		t.Errorf("Name = %q, want %q", file.Name, "entire")
	}
	if len(file.Tools) == 0 {
		t.Error("Tools should not be empty")
	}
	assertKiroHookCommand(t, file.Hooks.AgentSpawn, "entire hooks kiro agent-spawn")
	assertKiroHookCommand(t, file.Hooks.UserPromptSubmit, "entire hooks kiro user-prompt-submit")
	assertKiroHookCommand(t, file.Hooks.PreToolUse, "entire hooks kiro pre-tool-use")
	assertKiroHookCommand(t, file.Hooks.PostToolUse, "entire hooks kiro post-tool-use")
	assertKiroHookCommand(t, file.Hooks.Stop, "entire hooks kiro stop")
}

func TestInstallHooks_LocalDevMode(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	count, err := ag.InstallHooks(context.Background(), true, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}
	if count != 9 {
		t.Errorf("InstallHooks() returned count %d, want 9 (5 CLI + 4 IDE)", count)
	}

	file := readKiroAgentFile(t, tempDir)

	expectedPrefix := "go run ${KIRO_PROJECT_DIR}/cmd/entire/main.go hooks kiro "
	assertKiroHookCommand(t, file.Hooks.AgentSpawn, expectedPrefix+"agent-spawn")
	assertKiroHookCommand(t, file.Hooks.Stop, expectedPrefix+"stop")
}

func TestInstallHooks_Idempotent(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// First install
	count1, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("first InstallHooks() error = %v", err)
	}
	if count1 != 9 {
		t.Errorf("first InstallHooks() count = %d, want 9 (5 CLI + 4 IDE)", count1)
	}

	// Second install should detect hooks are already current and skip
	count2, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("second InstallHooks() error = %v", err)
	}
	if count2 != 0 {
		t.Errorf("second InstallHooks() count = %d, want 0 (no new hooks)", count2)
	}
}

func TestInstallHooks_ForceReinstall(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// First install
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("first InstallHooks() error = %v", err)
	}

	// Force reinstall should overwrite
	count, err := ag.InstallHooks(context.Background(), false, true)
	if err != nil {
		t.Fatalf("force InstallHooks() error = %v", err)
	}
	if count != 9 {
		t.Errorf("force InstallHooks() count = %d, want 9 (5 CLI + 4 IDE)", count)
	}
}

func TestInstallHooks_IncludesAllDefaultTools(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	file := readKiroAgentFile(t, tempDir)

	expectedTools := []string{
		"read", "write", "shell", "grep", "glob",
		"aws", "report", "introspect", "knowledge",
		"thinking", "todo", "delegate",
	}

	if len(file.Tools) != len(expectedTools) {
		t.Fatalf("Tools count = %d, want %d", len(file.Tools), len(expectedTools))
	}

	for i, want := range expectedTools {
		if file.Tools[i] != want {
			t.Errorf("Tools[%d] = %q, want %q", i, file.Tools[i], want)
		}
	}
}

func TestInstallHooks_CountMatchesHookNames(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	count, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	expectedCount := len(ag.HookNames()) + len(ideHookDefs)
	if count != expectedCount {
		t.Errorf("InstallHooks() count = %d, want %d (len(HookNames()) + len(ideHookDefs))", count, expectedCount)
	}
}

func TestInstallHooks_CreatesDirectoryStructure(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	hooksPath := filepath.Join(tempDir, ".kiro", "agents", "entire.json")
	if _, err := os.Stat(hooksPath); err != nil {
		t.Errorf("hooks file not found at %s: %v", hooksPath, err)
	}
}

func TestInstallHooks_FilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	hooksPath := filepath.Join(tempDir, ".kiro", "agents", "entire.json")
	info, err := os.Stat(hooksPath)
	if err != nil {
		t.Fatalf("failed to stat hooks file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("hooks file permissions = %o, want %o", perm, 0o600)
	}
}

func TestInstallHooks_ProducesValidJSON(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	hooksPath := filepath.Join(tempDir, ".kiro", "agents", "entire.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("failed to read hooks file: %v", err)
	}

	if !json.Valid(data) {
		t.Error("hooks file content is not valid JSON")
	}

	// Verify trailing newline (jsonutil.MarshalIndentWithNewline)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Error("hooks file should end with a newline")
	}
}

func TestInstallHooks_SwitchFromLocalDevToProduction(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Install in localDev mode
	_, err := ag.InstallHooks(context.Background(), true, false)
	if err != nil {
		t.Fatalf("localDev InstallHooks() error = %v", err)
	}

	// Force-install in production mode
	_, err = ag.InstallHooks(context.Background(), false, true)
	if err != nil {
		t.Fatalf("production InstallHooks() error = %v", err)
	}

	file := readKiroAgentFile(t, tempDir)
	assertKiroHookCommand(t, file.Hooks.AgentSpawn, "entire hooks kiro agent-spawn")
	assertKiroHookCommand(t, file.Hooks.Stop, "entire hooks kiro stop")
}

// --- UninstallHooks ---

func TestUninstallHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Install first
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Verify file exists
	hooksPath := filepath.Join(tempDir, ".kiro", "agents", "entire.json")
	if _, err := os.Stat(hooksPath); err != nil {
		t.Fatalf("hooks file should exist before uninstall: %v", err)
	}

	// Uninstall
	err = ag.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// Verify file is removed
	if _, err := os.Stat(hooksPath); err == nil {
		t.Error("hooks file should be removed after uninstall")
	}
}

func TestUninstallHooks_NoFileExists(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Should not error when file doesn't exist
	err := ag.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() should not error when no file exists: %v", err)
	}
}

// --- AreHooksInstalled ---

func TestAreHooksInstalled_AfterInstall(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Not installed initially
	if ag.AreHooksInstalled(context.Background()) {
		t.Error("hooks should not be installed initially")
	}

	// Install
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Should be installed now
	if !ag.AreHooksInstalled(context.Background()) {
		t.Error("hooks should be installed after InstallHooks()")
	}
}

func TestAreHooksInstalled_AfterUninstall(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Install
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}
	if !ag.AreHooksInstalled(context.Background()) {
		t.Fatal("hooks should be installed")
	}

	// Uninstall
	if err := ag.UninstallHooks(context.Background()); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// Should not be installed after uninstall
	if ag.AreHooksInstalled(context.Background()) {
		t.Error("hooks should not be installed after UninstallHooks()")
	}
}

func TestAreHooksInstalled_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Write invalid JSON to the hooks file
	hooksDir := filepath.Join(tempDir, ".kiro", "agents")
	if err := os.MkdirAll(hooksDir, 0o750); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "entire.json"), []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("failed to write invalid JSON: %v", err)
	}

	ag := &KiroAgent{}
	if ag.AreHooksInstalled(context.Background()) {
		t.Error("AreHooksInstalled() should return false for invalid JSON")
	}
}

func TestAreHooksInstalled_EmptyHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Write valid JSON but with empty hooks
	hooksDir := filepath.Join(tempDir, ".kiro", "agents")
	if err := os.MkdirAll(hooksDir, 0o750); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}
	emptyFile := `{"name": "entire", "tools": [], "hooks": {}}`
	if err := os.WriteFile(filepath.Join(hooksDir, "entire.json"), []byte(emptyFile), 0o600); err != nil {
		t.Fatalf("failed to write empty hooks file: %v", err)
	}

	ag := &KiroAgent{}
	if ag.AreHooksInstalled(context.Background()) {
		t.Error("AreHooksInstalled() should return false when hooks section is empty")
	}
}

func TestAreHooksInstalled_OnlyEntireHooksDetected(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Write a file with non-Entire hooks only
	hooksDir := filepath.Join(tempDir, ".kiro", "agents")
	if err := os.MkdirAll(hooksDir, 0o750); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}
	otherHooksFile := `{
		"name": "other-agent",
		"tools": [],
		"hooks": {
			"agentSpawn": [{"command": "some-other-tool agent-spawn"}],
			"stop": [{"command": "some-other-tool stop"}]
		}
	}`
	if err := os.WriteFile(filepath.Join(hooksDir, "entire.json"), []byte(otherHooksFile), 0o600); err != nil {
		t.Fatalf("failed to write other hooks file: %v", err)
	}

	ag := &KiroAgent{}
	if ag.AreHooksInstalled(context.Background()) {
		t.Error("AreHooksInstalled() should return false for non-Entire hooks")
	}
}

func TestAreHooksInstalled_DetectsLocalDevHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Install in localDev mode
	_, err := ag.InstallHooks(context.Background(), true, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Should detect localDev hooks as installed
	if !ag.AreHooksInstalled(context.Background()) {
		t.Error("AreHooksInstalled() should detect localDev hooks")
	}
}

// --- Helper functions ---

func readKiroAgentFile(t *testing.T, tempDir string) kiroAgentFile {
	t.Helper()
	hooksPath := filepath.Join(tempDir, ".kiro", "agents", "entire.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("failed to read hooks file: %v", err)
	}

	var file kiroAgentFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("failed to parse hooks file: %v", err)
	}
	return file
}

func assertKiroHookCommand(t *testing.T, entries []kiroHookEntry, expectedCommand string) {
	t.Helper()
	if len(entries) == 0 {
		t.Errorf("expected hook entry with command %q, got empty slice", expectedCommand)
		return
	}
	found := false
	for _, entry := range entries {
		if entry.Command == expectedCommand {
			found = true
			break
		}
	}
	if !found {
		commands := make([]string, 0, len(entries))
		for _, entry := range entries {
			commands = append(commands, entry.Command)
		}
		t.Errorf("expected hook command %q, got %v", expectedCommand, commands)
	}
}

// --- IDE Hook Tests ---

func TestInstallHooks_CreatesIDEHookFiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	for _, def := range ideHookDefs {
		path := filepath.Join(tempDir, ".kiro", "hooks", def.Filename+ideHookFileSuffix)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("IDE hook file not found at %s: %v", path, statErr)
		}
	}
}

func TestInstallHooks_IDEHooksValidJSON(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	for _, def := range ideHookDefs {
		path := filepath.Join(tempDir, ".kiro", "hooks", def.Filename+ideHookFileSuffix)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("failed to read %s: %v", path, readErr)
		}

		var hook kiroIDEHookFile
		if err := json.Unmarshal(data, &hook); err != nil {
			t.Fatalf("failed to parse %s: %v", path, err)
		}

		if !hook.Enabled {
			t.Errorf("%s: enabled = false, want true", def.Filename)
		}
		if hook.Version != ideHookVersion {
			t.Errorf("%s: version = %q, want %q", def.Filename, hook.Version, ideHookVersion)
		}
		if hook.When.Type != def.TriggerType {
			t.Errorf("%s: when.type = %q, want %q", def.Filename, hook.When.Type, def.TriggerType)
		}
		if hook.Then.Type != "runCommand" {
			t.Errorf("%s: then.type = %q, want %q", def.Filename, hook.Then.Type, "runCommand")
		}
		expectedCmd := "entire hooks kiro " + def.CLIVerb
		if hook.Then.Command != expectedCmd {
			t.Errorf("%s: then.command = %q, want %q", def.Filename, hook.Then.Command, expectedCmd)
		}
	}
}

func TestInstallHooks_IDEHooksLocalDevMode(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), true, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	expectedPrefix := "go run ${KIRO_PROJECT_DIR}/cmd/entire/main.go hooks kiro "

	for _, def := range ideHookDefs {
		path := filepath.Join(tempDir, ".kiro", "hooks", def.Filename+ideHookFileSuffix)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("failed to read %s: %v", path, readErr)
		}

		var hook kiroIDEHookFile
		if err := json.Unmarshal(data, &hook); err != nil {
			t.Fatalf("failed to parse %s: %v", path, err)
		}

		expectedCmd := expectedPrefix + def.CLIVerb
		if hook.Then.Command != expectedCmd {
			t.Errorf("%s: then.command = %q, want %q", def.Filename, hook.Then.Command, expectedCmd)
		}
	}
}

func TestInstallHooks_IDEHooksFilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	for _, def := range ideHookDefs {
		path := filepath.Join(tempDir, ".kiro", "hooks", def.Filename+ideHookFileSuffix)
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("failed to stat %s: %v", path, statErr)
		}
		perm := info.Mode().Perm()
		if perm != 0o600 {
			t.Errorf("%s: permissions = %o, want %o", def.Filename, perm, 0o600)
		}
	}
}

func TestInstallHooks_IDEHooksAllTriggerTypes(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	expectedTriggers := map[string]string{
		"entire-prompt-submit": "promptSubmit",
		"entire-stop":          "agentStop",
		"entire-pre-tool-use":  "preToolUse",
		"entire-post-tool-use": "postToolUse",
	}

	for filename, wantTrigger := range expectedTriggers {
		path := filepath.Join(tempDir, ".kiro", "hooks", filename+ideHookFileSuffix)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("failed to read %s: %v", path, readErr)
		}

		var hook kiroIDEHookFile
		if err := json.Unmarshal(data, &hook); err != nil {
			t.Fatalf("failed to parse %s: %v", path, err)
		}

		if hook.When.Type != wantTrigger {
			t.Errorf("%s: when.type = %q, want %q", filename, hook.When.Type, wantTrigger)
		}
	}
}

func TestUninstallHooks_RemovesIDEHookFiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Install first
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Verify IDE hook files exist
	for _, def := range ideHookDefs {
		path := filepath.Join(tempDir, ".kiro", "hooks", def.Filename+ideHookFileSuffix)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("IDE hook file should exist before uninstall: %v", statErr)
		}
	}

	// Uninstall
	if err := ag.UninstallHooks(context.Background()); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// Verify IDE hook files are removed
	for _, def := range ideHookDefs {
		path := filepath.Join(tempDir, ".kiro", "hooks", def.Filename+ideHookFileSuffix)
		if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("IDE hook file should be removed after uninstall: %s", path)
		}
	}
}

func TestAreHooksInstalled_IDEOnlyHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Install hooks, then remove only the CLI agent file
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Remove the CLI agent file only
	cliPath := filepath.Join(tempDir, ".kiro", "agents", "entire.json")
	if err := os.Remove(cliPath); err != nil {
		t.Fatalf("failed to remove CLI agent file: %v", err)
	}

	// Should still detect hooks via IDE hook files
	if !ag.AreHooksInstalled(context.Background()) {
		t.Error("AreHooksInstalled() should return true when IDE hooks are present")
	}
}

func TestInstallHooks_Idempotent_IncludesIDEHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// First install
	count1, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("first InstallHooks() error = %v", err)
	}
	if count1 != 9 {
		t.Errorf("first InstallHooks() count = %d, want 9", count1)
	}

	// Second install should be idempotent (both CLI and IDE hooks present)
	count2, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("second InstallHooks() error = %v", err)
	}
	if count2 != 0 {
		t.Errorf("second InstallHooks() count = %d, want 0 (no new hooks)", count2)
	}

	// Delete one IDE hook file — re-install should detect and fix
	firstDef := ideHookDefs[0]
	path := filepath.Join(tempDir, ".kiro", "hooks", firstDef.Filename+ideHookFileSuffix)
	if err := os.Remove(path); err != nil {
		t.Fatalf("failed to remove IDE hook for test: %v", err)
	}

	count3, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("third InstallHooks() error = %v", err)
	}
	if count3 != 9 {
		t.Errorf("third InstallHooks() count = %d, want 9 (re-installs all)", count3)
	}
}

// --- Trusted Commands Tests ---

func TestInstallHooks_CreatesTrustedCommands(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	commands := readTrustedCommands(t, tempDir)
	if len(commands) != 1 {
		t.Fatalf("expected 1 trusted command, got %d: %v", len(commands), commands)
	}
	if commands[0] != prodTrustedCommand {
		t.Errorf("trusted command = %q, want %q", commands[0], prodTrustedCommand)
	}
}

func TestInstallHooks_TrustedCommands_PreservesExistingSettings(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create existing .vscode/settings.json with other settings
	vscodeDir := filepath.Join(tempDir, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o750); err != nil {
		t.Fatalf("failed to create .vscode dir: %v", err)
	}
	existing := `{
  "editor.fontSize": 14,
  "editor.tabSize": 2
}
`
	if err := os.WriteFile(filepath.Join(vscodeDir, "settings.json"), []byte(existing), 0o600); err != nil {
		t.Fatalf("failed to write existing settings: %v", err)
	}

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Verify trusted commands added
	commands := readTrustedCommands(t, tempDir)
	if len(commands) != 1 || commands[0] != prodTrustedCommand {
		t.Errorf("trusted commands = %v, want [\"entire hooks *\"]", commands)
	}

	// Verify other settings preserved
	settings := readVSCodeSettings(t, tempDir)
	if _, ok := settings["editor.fontSize"]; !ok {
		t.Error("editor.fontSize setting was not preserved")
	}
	if _, ok := settings["editor.tabSize"]; !ok {
		t.Error("editor.tabSize setting was not preserved")
	}
}

func TestInstallHooks_TrustedCommands_LocalDevMode(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), true, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	commands := readTrustedCommands(t, tempDir)
	expected := localDevCmdPrefix + "hooks *"
	if len(commands) != 1 {
		t.Fatalf("expected 1 trusted command, got %d: %v", len(commands), commands)
	}
	if commands[0] != expected {
		t.Errorf("trusted command = %q, want %q", commands[0], expected)
	}
}

func TestInstallHooks_TrustedCommands_Idempotent(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// First install
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("first InstallHooks() error = %v", err)
	}

	// Force second install to bypass the early-return check
	_, err = ag.InstallHooks(context.Background(), false, true)
	if err != nil {
		t.Fatalf("second InstallHooks() error = %v", err)
	}

	// Should not have duplicated the entry
	commands := readTrustedCommands(t, tempDir)
	if len(commands) != 1 {
		t.Errorf("expected 1 trusted command after second install, got %d: %v", len(commands), commands)
	}
}

func TestInstallHooks_TrustedCommands_PreservesExistingTrustedCommands(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create existing settings with user-added trusted commands
	vscodeDir := filepath.Join(tempDir, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o750); err != nil {
		t.Fatalf("failed to create .vscode dir: %v", err)
	}
	existing := `{
  "kiroAgent.trustedCommands": ["my-custom-tool *", "another-tool run"]
}
`
	if err := os.WriteFile(filepath.Join(vscodeDir, "settings.json"), []byte(existing), 0o600); err != nil {
		t.Fatalf("failed to write existing settings: %v", err)
	}

	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	commands := readTrustedCommands(t, tempDir)
	if len(commands) != 3 {
		t.Fatalf("expected 3 trusted commands, got %d: %v", len(commands), commands)
	}
	// User commands should be preserved
	if commands[0] != "my-custom-tool *" {
		t.Errorf("commands[0] = %q, want %q", commands[0], "my-custom-tool *")
	}
	if commands[1] != "another-tool run" {
		t.Errorf("commands[1] = %q, want %q", commands[1], "another-tool run")
	}
	if commands[2] != prodTrustedCommand {
		t.Errorf("commands[2] = %q, want %q", commands[2], prodTrustedCommand)
	}
}

func TestInstallHooks_TrustedCommands_IdempotencyCheckIncludesTrustedCommands(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// First install
	count1, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("first InstallHooks() error = %v", err)
	}
	if count1 != 9 {
		t.Errorf("first install count = %d, want 9", count1)
	}

	// Second install (idempotent — should return 0)
	count2, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("second InstallHooks() error = %v", err)
	}
	if count2 != 0 {
		t.Errorf("second install count = %d, want 0 (idempotent)", count2)
	}

	// Remove the trusted command from settings — simulates user deletion
	settingsPath := filepath.Join(tempDir, ".vscode", "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("failed to clear settings: %v", err)
	}

	// Third install should detect missing trusted command and re-install
	count3, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("third InstallHooks() error = %v", err)
	}
	if count3 != 9 {
		t.Errorf("third install count = %d, want 9 (should re-install)", count3)
	}
}

func TestUninstallHooks_RemovesTrustedCommands(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Install
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Verify trusted commands exist
	commands := readTrustedCommands(t, tempDir)
	if len(commands) != 1 {
		t.Fatalf("expected 1 trusted command before uninstall, got %d", len(commands))
	}

	// Uninstall
	if err := ag.UninstallHooks(context.Background()); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// The key should be removed (empty array → key deleted)
	settings := readVSCodeSettings(t, tempDir)
	if _, ok := settings[trustedCommandsKey]; ok {
		t.Error("trustedCommands key should be removed after uninstall when no other commands remain")
	}
}

func TestUninstallHooks_TrustedCommands_PreservesOtherSettings(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Install hooks
	_, err := ag.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Add other settings to the file
	settingsPath := filepath.Join(tempDir, ".vscode", "settings.json")
	settings := readVSCodeSettings(t, tempDir)
	settings["editor.fontSize"] = json.RawMessage(`14`)
	output, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal settings: %v", err)
	}
	if err := os.WriteFile(settingsPath, append(output, '\n'), 0o600); err != nil {
		t.Fatalf("failed to write settings: %v", err)
	}

	// Uninstall
	if err := ag.UninstallHooks(context.Background()); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// Other settings should remain
	settingsAfter := readVSCodeSettings(t, tempDir)
	if _, ok := settingsAfter["editor.fontSize"]; !ok {
		t.Error("editor.fontSize setting was not preserved after uninstall")
	}
}

func TestUninstallHooks_TrustedCommands_PreservesUserTrustedCommands(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings with both user and Entire trusted commands
	vscodeDir := filepath.Join(tempDir, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o750); err != nil {
		t.Fatalf("failed to create .vscode dir: %v", err)
	}
	existing := `{
  "kiroAgent.trustedCommands": ["my-custom-tool *", "entire hooks *", "another-tool run"]
}
`
	if err := os.WriteFile(filepath.Join(vscodeDir, "settings.json"), []byte(existing), 0o600); err != nil {
		t.Fatalf("failed to write existing settings: %v", err)
	}

	// Also install CLI + IDE hooks so uninstall has something to remove
	ag := &KiroAgent{}
	_, err := ag.InstallHooks(context.Background(), false, true)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Uninstall
	if err := ag.UninstallHooks(context.Background()); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// User trusted commands should survive
	commands := readTrustedCommands(t, tempDir)
	if len(commands) != 2 {
		t.Fatalf("expected 2 user trusted commands after uninstall, got %d: %v", len(commands), commands)
	}
	if commands[0] != "my-custom-tool *" {
		t.Errorf("commands[0] = %q, want %q", commands[0], "my-custom-tool *")
	}
	if commands[1] != "another-tool run" {
		t.Errorf("commands[1] = %q, want %q", commands[1], "another-tool run")
	}
}

func TestUninstallHooks_TrustedCommands_NoSettingsFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &KiroAgent{}

	// Should not error when .vscode/settings.json doesn't exist
	err := ag.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() should not error when no settings file exists: %v", err)
	}
}

// --- Trusted Commands Helper Functions ---

func readVSCodeSettings(t *testing.T, tempDir string) map[string]json.RawMessage {
	t.Helper()
	settingsPath := filepath.Join(tempDir, ".vscode", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read .vscode/settings.json: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse .vscode/settings.json: %v", err)
	}
	return settings
}

func readTrustedCommands(t *testing.T, tempDir string) []string {
	t.Helper()
	settings := readVSCodeSettings(t, tempDir)
	raw, ok := settings[trustedCommandsKey]
	if !ok {
		t.Fatal("kiroAgent.trustedCommands key not found in .vscode/settings.json")
	}
	var commands []string
	if err := json.Unmarshal(raw, &commands); err != nil {
		t.Fatalf("failed to parse kiroAgent.trustedCommands: %v", err)
	}
	return commands
}

// --- Internal helper functions ---

func TestIsEntireHook(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		command string
		want    bool
	}{
		{"entire hooks kiro agent-spawn", true},
		{"entire hooks kiro stop", true},
		{"entire hooks kiro user-prompt-submit", true},
		{localDevCmdPrefix + "hooks kiro stop", true},
		{"some-other-tool agent-spawn", false},
		{"entire-something-else", false},
		{"", false},
	}

	for _, tc := range testCases {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()
			got := isEntireHook(tc.command)
			if got != tc.want {
				t.Errorf("isEntireHook(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestHasEntireHook(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		entries []kiroHookEntry
		want    bool
	}{
		{
			name:    "empty",
			entries: nil,
			want:    false,
		},
		{
			name:    "non-entire hook",
			entries: []kiroHookEntry{{Command: "other-tool stop"}},
			want:    false,
		},
		{
			name:    "entire hook",
			entries: []kiroHookEntry{{Command: "entire hooks kiro stop"}},
			want:    true,
		},
		{
			name: "mixed hooks",
			entries: []kiroHookEntry{
				{Command: "other-tool stop"},
				{Command: "entire hooks kiro stop"},
			},
			want: true,
		},
		{
			name:    "local dev hook",
			entries: []kiroHookEntry{{Command: localDevCmdPrefix + "hooks kiro stop"}},
			want:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := hasEntireHook(tc.entries)
			if got != tc.want {
				t.Errorf("hasEntireHook() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllHooksPresent(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		hooks    kiroHooks
		localDev bool
		want     bool
	}{
		{
			name: "all production hooks present",
			hooks: kiroHooks{
				AgentSpawn:       []kiroHookEntry{{Command: "entire hooks kiro agent-spawn"}},
				UserPromptSubmit: []kiroHookEntry{{Command: "entire hooks kiro user-prompt-submit"}},
				PreToolUse:       []kiroHookEntry{{Command: "entire hooks kiro pre-tool-use"}},
				PostToolUse:      []kiroHookEntry{{Command: "entire hooks kiro post-tool-use"}},
				Stop:             []kiroHookEntry{{Command: "entire hooks kiro stop"}},
			},
			localDev: false,
			want:     true,
		},
		{
			name: "all local dev hooks present",
			hooks: kiroHooks{
				AgentSpawn:       []kiroHookEntry{{Command: localDevCmdPrefix + "hooks kiro agent-spawn"}},
				UserPromptSubmit: []kiroHookEntry{{Command: localDevCmdPrefix + "hooks kiro user-prompt-submit"}},
				PreToolUse:       []kiroHookEntry{{Command: localDevCmdPrefix + "hooks kiro pre-tool-use"}},
				PostToolUse:      []kiroHookEntry{{Command: localDevCmdPrefix + "hooks kiro post-tool-use"}},
				Stop:             []kiroHookEntry{{Command: localDevCmdPrefix + "hooks kiro stop"}},
			},
			localDev: true,
			want:     true,
		},
		{
			name:     "empty hooks",
			hooks:    kiroHooks{},
			localDev: false,
			want:     false,
		},
		{
			name: "missing stop hook",
			hooks: kiroHooks{
				AgentSpawn:       []kiroHookEntry{{Command: "entire hooks kiro agent-spawn"}},
				UserPromptSubmit: []kiroHookEntry{{Command: "entire hooks kiro user-prompt-submit"}},
				PreToolUse:       []kiroHookEntry{{Command: "entire hooks kiro pre-tool-use"}},
				PostToolUse:      []kiroHookEntry{{Command: "entire hooks kiro post-tool-use"}},
			},
			localDev: false,
			want:     false,
		},
		{
			name: "wrong mode - production hooks with localDev=true",
			hooks: kiroHooks{
				AgentSpawn:       []kiroHookEntry{{Command: "entire hooks kiro agent-spawn"}},
				UserPromptSubmit: []kiroHookEntry{{Command: "entire hooks kiro user-prompt-submit"}},
				PreToolUse:       []kiroHookEntry{{Command: "entire hooks kiro pre-tool-use"}},
				PostToolUse:      []kiroHookEntry{{Command: "entire hooks kiro post-tool-use"}},
				Stop:             []kiroHookEntry{{Command: "entire hooks kiro stop"}},
			},
			localDev: true,
			want:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := allHooksPresent(tc.hooks, tc.localDev)
			if got != tc.want {
				t.Errorf("allHooksPresent() = %v, want %v", got, tc.want)
			}
		})
	}
}
