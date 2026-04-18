package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agentpkg "github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/testutil"
)

// metadataDenyRuleTest is the rule that blocks Claude from reading Entire metadata
const metadataDenyRuleTest = "Read(./.entire/metadata/**)"

// TestInstallHooks_StampsEntireMeta verifies that a fresh install writes an
// entireMeta.cli_version field matching the running CLI version, and that
// ReadHookMeta round-trips that stamp back out.
func TestInstallHooks_StampsEntireMeta(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	ag := &ClaudeCodeAgent{}
	if _, err := ag.InstallHooks(context.Background(), false, false); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	settingsPath := filepath.Join(tempDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	meta, ok := agentpkg.ReadJSONHookMeta(raw)
	if !ok {
		t.Fatalf("expected entireMeta stamp in %s, got keys %v", settingsPath, keysOf(raw))
	}
	if meta.CLIVersion != agentpkg.HookMetaVersion() {
		t.Fatalf("stamp cli_version = %q, want %q", meta.CLIVersion, agentpkg.HookMetaVersion())
	}

	// ReadHookMeta must return the same stamp via the typed API.
	readMeta, found, err := ag.ReadHookMeta(context.Background())
	if err != nil {
		t.Fatalf("ReadHookMeta: %v", err)
	}
	if !found {
		t.Fatalf("ReadHookMeta reported no stamp")
	}
	if readMeta.CLIVersion != meta.CLIVersion {
		t.Fatalf("ReadHookMeta cli_version = %q, want %q", readMeta.CLIVersion, meta.CLIVersion)
	}
}

// TestInstallHooks_StampsOnPreExistingInstall makes sure an install that would
// otherwise no-op (hooks already present, permissions present) still writes the
// stamp if it is missing — this is how existing users' configs acquire an
// entireMeta field on their next `entire enable` after upgrading.
func TestInstallHooks_StampsOnPreExistingInstall(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Seed a settings.json that already has the Entire hooks and deny rule
	// but no entireMeta stamp (simulating a pre-upgrade install).
	writeSettingsFile(t, tempDir, `{
  "permissions": {
    "deny": ["Read(./.entire/metadata/**)"]
  },
  "hooks": {
    "SessionStart": [{"matcher":"","hooks":[{"type":"command","command":"sh -c 'if ! command -v entire >/dev/null 2>&1; then printf \"%s\\n\" \"x\"; exit 0; fi; exec entire hooks claude-code session-start'"}]}],
    "SessionEnd": [{"matcher":"","hooks":[{"type":"command","command":"sh -c 'if ! command -v entire >/dev/null 2>&1; then exit 0; fi; exec entire hooks claude-code session-end'"}]}],
    "Stop": [{"matcher":"","hooks":[{"type":"command","command":"sh -c 'if ! command -v entire >/dev/null 2>&1; then exit 0; fi; exec entire hooks claude-code stop'"}]}],
    "UserPromptSubmit": [{"matcher":"","hooks":[{"type":"command","command":"sh -c 'if ! command -v entire >/dev/null 2>&1; then exit 0; fi; exec entire hooks claude-code user-prompt-submit'"}]}],
    "PreToolUse": [{"matcher":"Task","hooks":[{"type":"command","command":"sh -c 'if ! command -v entire >/dev/null 2>&1; then exit 0; fi; exec entire hooks claude-code pre-task'"}]}],
    "PostToolUse": [
      {"matcher":"Task","hooks":[{"type":"command","command":"sh -c 'if ! command -v entire >/dev/null 2>&1; then exit 0; fi; exec entire hooks claude-code post-task'"}]},
      {"matcher":"TodoWrite","hooks":[{"type":"command","command":"sh -c 'if ! command -v entire >/dev/null 2>&1; then exit 0; fi; exec entire hooks claude-code post-todo'"}]}
    ]
  }
}`)

	ag := &ClaudeCodeAgent{}
	if _, err := ag.InstallHooks(context.Background(), false, false); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	meta, found, err := ag.ReadHookMeta(context.Background())
	if err != nil {
		t.Fatalf("ReadHookMeta: %v", err)
	}
	if !found {
		t.Fatal("expected stamp to be backfilled on pre-existing install")
	}
	if meta.CLIVersion != agentpkg.HookMetaVersion() {
		t.Fatalf("backfilled stamp = %q, want %q", meta.CLIVersion, agentpkg.HookMetaVersion())
	}
}

// TestInstallHooks_MissingStampForcesReinstall pins the invariant that a plain
// `entire enable` on a pre-stamp config cannot silently clear drift by writing
// just the stamp: the hook payload itself must be rewritten. We seed a bogus
// (but managed-looking) entire hook command that wouldn't match current
// output, call InstallHooks with force=false, and verify the seeded command
// is gone and the canonical one took its place.
func TestInstallHooks_MissingStampForcesReinstall(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Settings with a managed-prefix hook but stale command shape, and no stamp.
	writeSettingsFile(t, tempDir, `{
  "hooks": {
    "Stop": [{"matcher":"","hooks":[{"type":"command","command":"entire hooks claude-code stop --legacy-arg"}]}]
  }
}`)

	ag := &ClaudeCodeAgent{}
	if _, err := ag.InstallHooks(context.Background(), false, false); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tempDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "--legacy-arg") {
		t.Errorf("stale command should have been removed, still present in:\n%s", content)
	}
	if !strings.Contains(content, `"cli_version"`) {
		t.Errorf("expected stamp to be written, not found in:\n%s", content)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestInstallHooks_PermissionsDeny_FreshInstall(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	agent := &ClaudeCodeAgent{}
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	perms := readPermissions(t, tempDir)

	// Verify permissions.deny contains our rule
	if !containsRule(perms.Deny, metadataDenyRuleTest) {
		t.Errorf("permissions.deny = %v, want to contain %q", perms.Deny, metadataDenyRuleTest)
	}
}

func TestInstallHooks_PermissionsDeny_Idempotent(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	agent := &ClaudeCodeAgent{}
	// First install
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("first InstallHooks() error = %v", err)
	}

	// Second install
	_, err = agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("second InstallHooks() error = %v", err)
	}

	perms := readPermissions(t, tempDir)

	// Count occurrences of our rule
	count := 0
	for _, rule := range perms.Deny {
		if rule == metadataDenyRuleTest {
			count++
		}
	}
	if count != 1 {
		t.Errorf("permissions.deny contains %d copies of rule, want 1", count)
	}
}

func TestInstallHooks_PermissionsDeny_PreservesUserRules(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings.json with existing user deny rule
	writeSettingsFile(t, tempDir, `{
  "permissions": {
    "deny": ["Bash(rm -rf *)"]
  }
}`)

	agent := &ClaudeCodeAgent{}
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	perms := readPermissions(t, tempDir)

	// Verify both rules exist
	if !containsRule(perms.Deny, "Bash(rm -rf *)") {
		t.Errorf("permissions.deny = %v, want to contain user rule", perms.Deny)
	}
	if !containsRule(perms.Deny, metadataDenyRuleTest) {
		t.Errorf("permissions.deny = %v, want to contain Entire rule", perms.Deny)
	}
}

func TestInstallHooks_PermissionsDeny_PreservesAllowRules(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings.json with existing allow rules
	writeSettingsFile(t, tempDir, `{
  "permissions": {
    "allow": ["Read(**)", "Write(**)"]
  }
}`)

	agent := &ClaudeCodeAgent{}
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	perms := readPermissions(t, tempDir)

	// Verify allow rules are preserved
	if len(perms.Allow) != 2 {
		t.Errorf("permissions.allow = %v, want 2 rules", perms.Allow)
	}
	if !containsRule(perms.Allow, "Read(**)") {
		t.Errorf("permissions.allow = %v, want to contain Read(**)", perms.Allow)
	}
	if !containsRule(perms.Allow, "Write(**)") {
		t.Errorf("permissions.allow = %v, want to contain Write(**)", perms.Allow)
	}
}

func TestInstallHooks_PermissionsDeny_SkipsExistingRule(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings.json with the rule already present
	writeSettingsFile(t, tempDir, `{
  "permissions": {
    "deny": ["Read(./.entire/metadata/**)"]
  }
}`)

	agent := &ClaudeCodeAgent{}
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	perms := readPermissions(t, tempDir)

	// Should still have exactly 1 rule
	if len(perms.Deny) != 1 {
		t.Errorf("permissions.deny = %v, want exactly 1 rule", perms.Deny)
	}
}

func TestInstallHooks_PermissionsDeny_PreservesUnknownFields(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings.json with unknown permission fields like "ask"
	writeSettingsFile(t, tempDir, `{
  "permissions": {
    "allow": ["Read(**)"],
    "ask": ["Write(**)", "Bash(*)"],
    "customField": {"nested": "value"}
  }
}`)

	agent := &ClaudeCodeAgent{}
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Read raw settings to check for unknown fields
	settingsPath := filepath.Join(tempDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	var rawSettings map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawSettings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	var rawPermissions map[string]json.RawMessage
	if err := json.Unmarshal(rawSettings["permissions"], &rawPermissions); err != nil {
		t.Fatalf("failed to parse permissions: %v", err)
	}

	// Verify "ask" field is preserved
	if _, ok := rawPermissions["ask"]; !ok {
		t.Errorf("permissions.ask was not preserved, got keys: %v", testutil.GetKeys(rawPermissions))
	}

	// Verify "customField" is preserved
	if _, ok := rawPermissions["customField"]; !ok {
		t.Errorf("permissions.customField was not preserved, got keys: %v", testutil.GetKeys(rawPermissions))
	}

	// Verify the "ask" field content
	var askRules []string
	if err := json.Unmarshal(rawPermissions["ask"], &askRules); err != nil {
		t.Fatalf("failed to parse permissions.ask: %v", err)
	}
	if len(askRules) != 2 || askRules[0] != "Write(**)" || askRules[1] != "Bash(*)" {
		t.Errorf("permissions.ask = %v, want [Write(**), Bash(*)]", askRules)
	}

	// Verify the deny rule was added
	var denyRules []string
	if err := json.Unmarshal(rawPermissions["deny"], &denyRules); err != nil {
		t.Fatalf("failed to parse permissions.deny: %v", err)
	}
	if !containsRule(denyRules, metadataDenyRuleTest) {
		t.Errorf("permissions.deny = %v, want to contain %q", denyRules, metadataDenyRuleTest)
	}

	// Verify "allow" is preserved
	var allowRules []string
	if err := json.Unmarshal(rawPermissions["allow"], &allowRules); err != nil {
		t.Fatalf("failed to parse permissions.allow: %v", err)
	}
	if len(allowRules) != 1 || allowRules[0] != "Read(**)" {
		t.Errorf("permissions.allow = %v, want [Read(**)]", allowRules)
	}
}

// Helper functions

// testPermissions is used only for test assertions
type testPermissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

func readPermissions(t *testing.T, tempDir string) testPermissions {
	t.Helper()
	settingsPath := filepath.Join(tempDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	var rawSettings map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawSettings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}

	var perms testPermissions
	if permRaw, ok := rawSettings["permissions"]; ok {
		if err := json.Unmarshal(permRaw, &perms); err != nil {
			t.Fatalf("failed to parse permissions: %v", err)
		}
	}
	return perms
}

func writeSettingsFile(t *testing.T, tempDir, content string) {
	t.Helper()
	claudeDir := filepath.Join(tempDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write settings.json: %v", err)
	}
}

func containsRule(rules []string, rule string) bool {
	return slices.Contains(rules, rule)
}

func TestUninstallHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	agent := &ClaudeCodeAgent{}

	// First install
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Verify hooks are installed
	if !agent.AreHooksInstalled(context.Background()) {
		t.Error("hooks should be installed before uninstall")
	}

	// Uninstall
	err = agent.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// Verify hooks are removed
	if agent.AreHooksInstalled(context.Background()) {
		t.Error("hooks should not be installed after uninstall")
	}
}

func TestUninstallHooks_NoSettingsFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	agent := &ClaudeCodeAgent{}

	// Should not error when no settings file exists
	err := agent.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() should not error when no settings file: %v", err)
	}
}

func TestUninstallHooks_PreservesUserHooks(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings with both user and entire hooks
	writeSettingsFile(t, tempDir, `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo user hook"}]
      },
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "entire hooks claude-code stop"}]
      }
    ]
  }
}`)

	agent := &ClaudeCodeAgent{}
	err := agent.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	settings := readClaudeSettings(t, tempDir)

	// Verify only user hooks remain
	if len(settings.Hooks.Stop) != 1 {
		t.Errorf("Stop hooks = %d after uninstall, want 1 (user only)", len(settings.Hooks.Stop))
	}

	// Verify it's the user hook
	if len(settings.Hooks.Stop) > 0 && len(settings.Hooks.Stop[0].Hooks) > 0 {
		if settings.Hooks.Stop[0].Hooks[0].Command != "echo user hook" {
			t.Error("user hook was removed during uninstall")
		}
	}
}

func TestUninstallHooks_RemovesDenyRule(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	agent := &ClaudeCodeAgent{}

	// First install (which adds the deny rule)
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Verify deny rule was added
	perms := readPermissions(t, tempDir)
	if !containsRule(perms.Deny, metadataDenyRuleTest) {
		t.Fatal("deny rule should be present after install")
	}

	// Uninstall
	err = agent.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// Verify deny rule was removed
	perms = readPermissions(t, tempDir)
	if containsRule(perms.Deny, metadataDenyRuleTest) {
		t.Error("deny rule should be removed after uninstall")
	}
}

func TestUninstallHooks_PreservesUserDenyRules(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings with user deny rule and entire deny rule
	writeSettingsFile(t, tempDir, `{
  "permissions": {
    "deny": ["Bash(rm -rf *)", "Read(./.entire/metadata/**)"]
  },
  "hooks": {
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "entire hooks claude-code stop"}]
      }
    ]
  }
}`)

	agent := &ClaudeCodeAgent{}
	err := agent.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	perms := readPermissions(t, tempDir)

	// Verify user deny rule is preserved
	if !containsRule(perms.Deny, "Bash(rm -rf *)") {
		t.Errorf("user deny rule was removed, got: %v", perms.Deny)
	}

	// Verify entire deny rule is removed
	if containsRule(perms.Deny, metadataDenyRuleTest) {
		t.Errorf("entire deny rule should be removed, got: %v", perms.Deny)
	}
}

// readClaudeSettings reads and parses the Claude Code settings file
func readClaudeSettings(t *testing.T, tempDir string) ClaudeSettings {
	t.Helper()
	settingsPath := filepath.Join(tempDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}

	var settings ClaudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings.json: %v", err)
	}
	return settings
}

//nolint:tparallel // Parent uses t.Chdir() which prevents t.Parallel(); subtests only read from pre-loaded data
func TestInstallHooks_PreservesUserHooksOnSameType(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings with user hooks on the same hook types we use
	writeSettingsFile(t, tempDir, `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo user stop hook"}]
      }
    ],
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo user session start"}]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Write",
        "hooks": [{"type": "command", "command": "echo user wrote file"}]
      }
    ]
  }
}`)

	agent := &ClaudeCodeAgent{}
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	rawHooks := testutil.ReadRawHooks(t, tempDir, ".claude")

	t.Run("Stop", func(t *testing.T) {
		t.Parallel()
		var matchers []ClaudeHookMatcher
		if err := json.Unmarshal(rawHooks["Stop"], &matchers); err != nil {
			t.Fatalf("failed to parse Stop hooks: %v", err)
		}
		assertHookExists(t, matchers, "", "echo user stop hook", "user Stop hook")
		assertHookExists(t, matchers, "", agentpkg.WrapProductionSilentHookCommand("entire hooks claude-code stop"), "Entire Stop hook")
	})

	t.Run("SessionStart", func(t *testing.T) {
		t.Parallel()
		var matchers []ClaudeHookMatcher
		if err := json.Unmarshal(rawHooks["SessionStart"], &matchers); err != nil {
			t.Fatalf("failed to parse SessionStart hooks: %v", err)
		}
		assertHookExists(t, matchers, "", "echo user session start", "user SessionStart hook")
		assertHookExists(t, matchers, "", agentpkg.WrapProductionJSONWarningHookCommand("entire hooks claude-code session-start", agentpkg.WarningFormatMultiLine), "Entire SessionStart hook")
	})

	t.Run("PostToolUse", func(t *testing.T) {
		t.Parallel()
		var matchers []ClaudeHookMatcher
		if err := json.Unmarshal(rawHooks["PostToolUse"], &matchers); err != nil {
			t.Fatalf("failed to parse PostToolUse hooks: %v", err)
		}
		assertHookExists(t, matchers, "Write", "echo user wrote file", "user Write hook")
		assertHookExists(t, matchers, "Task", agentpkg.WrapProductionSilentHookCommand("entire hooks claude-code post-task"), "Entire Task hook")
		assertHookExists(t, matchers, "TodoWrite", agentpkg.WrapProductionSilentHookCommand("entire hooks claude-code post-todo"), "Entire TodoWrite hook")
	})
}

// assertHookExists checks that a hook with the given matcher and command exists
func assertHookExists(t *testing.T, matchers []ClaudeHookMatcher, matcher, command, description string) {
	t.Helper()
	for _, m := range matchers {
		if m.Matcher == matcher {
			for _, h := range m.Hooks {
				if h.Command == command {
					return
				}
			}
		}
	}
	t.Errorf("%s was not found (matcher=%q, command=%q)", description, matcher, command)
}

func TestInstallHooks_PreservesUnknownHookTypes(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings with a hook type we don't handle (Notification is a real Claude Code hook type)
	writeSettingsFile(t, tempDir, `{
  "hooks": {
    "Notification": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo notification received"}]
      }
    ],
    "SubagentStop": [
      {
        "matcher": ".*",
        "hooks": [{"type": "command", "command": "echo subagent stopped"}]
      }
    ]
  }
}`)

	agent := &ClaudeCodeAgent{}
	_, err := agent.InstallHooks(context.Background(), false, false)
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Read raw settings to check for unknown hook types
	rawHooks := testutil.ReadRawHooks(t, tempDir, ".claude")

	// Verify Notification hook is preserved
	if _, ok := rawHooks["Notification"]; !ok {
		t.Errorf("Notification hook type was not preserved, got keys: %v", testutil.GetKeys(rawHooks))
	}

	// Verify SubagentStop hook is preserved
	if _, ok := rawHooks["SubagentStop"]; !ok {
		t.Errorf("SubagentStop hook type was not preserved, got keys: %v", testutil.GetKeys(rawHooks))
	}

	// Verify the Notification hook content is intact
	var notificationMatchers []ClaudeHookMatcher
	if err := json.Unmarshal(rawHooks["Notification"], &notificationMatchers); err != nil {
		t.Fatalf("failed to parse Notification hooks: %v", err)
	}
	if len(notificationMatchers) != 1 {
		t.Errorf("Notification matchers = %d, want 1", len(notificationMatchers))
	}
	if len(notificationMatchers) > 0 && len(notificationMatchers[0].Hooks) > 0 {
		if notificationMatchers[0].Hooks[0].Command != "echo notification received" {
			t.Errorf("Notification hook command = %q, want %q",
				notificationMatchers[0].Hooks[0].Command, "echo notification received")
		}
	}

	// Verify the SubagentStop hook content is intact
	var subagentStopMatchers []ClaudeHookMatcher
	if err := json.Unmarshal(rawHooks["SubagentStop"], &subagentStopMatchers); err != nil {
		t.Fatalf("failed to parse SubagentStop hooks: %v", err)
	}
	if len(subagentStopMatchers) != 1 {
		t.Errorf("SubagentStop matchers = %d, want 1", len(subagentStopMatchers))
	}
	if len(subagentStopMatchers) > 0 {
		if subagentStopMatchers[0].Matcher != ".*" {
			t.Errorf("SubagentStop matcher = %q, want %q", subagentStopMatchers[0].Matcher, ".*")
		}
		if len(subagentStopMatchers[0].Hooks) > 0 {
			if subagentStopMatchers[0].Hooks[0].Command != "echo subagent stopped" {
				t.Errorf("SubagentStop hook command = %q, want %q",
					subagentStopMatchers[0].Hooks[0].Command, "echo subagent stopped")
			}
		}
	}

	// Verify our hooks were also installed
	if _, ok := rawHooks["Stop"]; !ok {
		t.Errorf("Stop hook should have been installed")
	}
}

func TestUninstallHooks_PreservesUnknownHookTypes(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	// Create settings with Entire hooks AND unknown hook types
	writeSettingsFile(t, tempDir, `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "entire hooks claude-code stop"}]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [{"type": "command", "command": "echo notification received"}]
      }
    ],
    "SubagentStop": [
      {
        "matcher": ".*",
        "hooks": [{"type": "command", "command": "echo subagent stopped"}]
      }
    ]
  }
}`)

	agent := &ClaudeCodeAgent{}
	err := agent.UninstallHooks(context.Background())
	if err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// Read raw settings to check for unknown hook types
	rawHooks := testutil.ReadRawHooks(t, tempDir, ".claude")

	// Verify Notification hook is preserved
	if _, ok := rawHooks["Notification"]; !ok {
		t.Errorf("Notification hook type was not preserved, got keys: %v", testutil.GetKeys(rawHooks))
	}

	// Verify SubagentStop hook is preserved
	if _, ok := rawHooks["SubagentStop"]; !ok {
		t.Errorf("SubagentStop hook type was not preserved, got keys: %v", testutil.GetKeys(rawHooks))
	}

	// Verify our hooks were removed
	if _, ok := rawHooks["Stop"]; ok {
		// Check if there are any hooks left (should be empty after uninstall)
		var stopMatchers []ClaudeHookMatcher
		if err := json.Unmarshal(rawHooks["Stop"], &stopMatchers); err == nil && len(stopMatchers) > 0 {
			t.Errorf("Stop hook should have been removed")
		}
	}
}
