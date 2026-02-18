package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

// Ensure CursorAgent implements HookSupport and HookHandler
var (
	_ agent.HookSupport = (*CursorAgent)(nil)
	_ agent.HookHandler = (*CursorAgent)(nil)
)

// Cursor hook names - these become subcommands under `entire hooks cursor`
const (
	HookNameSessionStart       = "session-start"
	HookNameSessionEnd         = "session-end"
	HookNameBeforeSubmitPrompt = "before-submit-prompt"
	HookNameStop               = "stop"
	HookNamePreTask            = "pre-task"
	HookNamePostTask           = "post-task"
	HookNamePostTodo           = "post-todo"
)

// HooksFileName is the hooks file used by Cursor.
const HooksFileName = "hooks.json"

// entireHookPrefixes are command prefixes that identify Entire hooks
var entireHookPrefixes = []string{
	"entire ",
	"go run ${CURSOR_PROJECT_DIR}/cmd/entire/main.go ",
}

// GetHookNames returns the hook verbs Cursor supports.
// These become subcommands: entire hooks cursor <verb>
func (c *CursorAgent) GetHookNames() []string {
	return []string{
		HookNameSessionStart,
		HookNameSessionEnd,
		HookNameBeforeSubmitPrompt,
		HookNameStop,
		HookNamePreTask,
		HookNamePostTask,
		HookNamePostTodo,
	}
}

// InstallHooks installs Cursor hooks in .cursor/hooks.json.
// If force is true, removes existing Entire hooks before installing.
// Returns the number of hooks installed.
func (c *CursorAgent) InstallHooks(localDev bool, force bool) (int, error) {
	repoRoot, err := paths.RepoRoot()
	if err != nil {
		repoRoot, err = os.Getwd() //nolint:forbidigo // Intentional fallback when RepoRoot() fails (tests run outside git repos)
		if err != nil {
			return 0, fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	hooksPath := filepath.Join(repoRoot, ".cursor", HooksFileName)

	// Read existing hooks file if it exists
	var hooksFile CursorHooksFile

	existingData, readErr := os.ReadFile(hooksPath) //nolint:gosec // path is constructed from repo root + fixed path
	if readErr == nil {
		if err := json.Unmarshal(existingData, &hooksFile); err != nil {
			return 0, fmt.Errorf("failed to parse existing hooks.json: %w", err)
		}
	} else {
		hooksFile.Version = 1
	}

	// If force is true, remove all existing Entire hooks first
	if force {
		hooksFile.Hooks.SessionStart = removeEntireHooks(hooksFile.Hooks.SessionStart)
		hooksFile.Hooks.SessionEnd = removeEntireHooks(hooksFile.Hooks.SessionEnd)
		hooksFile.Hooks.BeforeSubmitPrompt = removeEntireHooks(hooksFile.Hooks.BeforeSubmitPrompt)
		hooksFile.Hooks.Stop = removeEntireHooks(hooksFile.Hooks.Stop)
		hooksFile.Hooks.PreToolUse = removeEntireHooks(hooksFile.Hooks.PreToolUse)
		hooksFile.Hooks.PostToolUse = removeEntireHooks(hooksFile.Hooks.PostToolUse)
	}

	// Define hook commands
	var cmdPrefix string
	if localDev {
		cmdPrefix = "go run ${CURSOR_PROJECT_DIR}/cmd/entire/main.go hooks cursor "
	} else {
		cmdPrefix = "entire hooks cursor "
	}

	sessionStartCmd := cmdPrefix + "session-start"
	sessionEndCmd := cmdPrefix + "session-end"
	beforeSubmitPromptCmd := cmdPrefix + "before-submit-prompt"
	stopCmd := cmdPrefix + "stop"
	preTaskCmd := cmdPrefix + "pre-task"
	postTaskCmd := cmdPrefix + "post-task"
	postTodoCmd := cmdPrefix + "post-todo"

	count := 0

	// Add hooks if they don't exist
	if !hookCommandExists(hooksFile.Hooks.SessionStart, sessionStartCmd) {
		hooksFile.Hooks.SessionStart = append(hooksFile.Hooks.SessionStart, CursorHookEntry{Command: sessionStartCmd})
		count++
	}
	if !hookCommandExists(hooksFile.Hooks.SessionEnd, sessionEndCmd) {
		hooksFile.Hooks.SessionEnd = append(hooksFile.Hooks.SessionEnd, CursorHookEntry{Command: sessionEndCmd})
		count++
	}
	if !hookCommandExists(hooksFile.Hooks.BeforeSubmitPrompt, beforeSubmitPromptCmd) {
		hooksFile.Hooks.BeforeSubmitPrompt = append(hooksFile.Hooks.BeforeSubmitPrompt, CursorHookEntry{Command: beforeSubmitPromptCmd})
		count++
	}
	if !hookCommandExists(hooksFile.Hooks.Stop, stopCmd) {
		hooksFile.Hooks.Stop = append(hooksFile.Hooks.Stop, CursorHookEntry{Command: stopCmd})
		count++
	}
	if !hookCommandExistsWithMatcher(hooksFile.Hooks.PreToolUse, "Task", preTaskCmd) {
		hooksFile.Hooks.PreToolUse = append(hooksFile.Hooks.PreToolUse, CursorHookEntry{Command: preTaskCmd, Matcher: "Task"})
		count++
	}
	if !hookCommandExistsWithMatcher(hooksFile.Hooks.PostToolUse, "Task", postTaskCmd) {
		hooksFile.Hooks.PostToolUse = append(hooksFile.Hooks.PostToolUse, CursorHookEntry{Command: postTaskCmd, Matcher: "Task"})
		count++
	}
	if !hookCommandExistsWithMatcher(hooksFile.Hooks.PostToolUse, "TodoWrite", postTodoCmd) {
		hooksFile.Hooks.PostToolUse = append(hooksFile.Hooks.PostToolUse, CursorHookEntry{Command: postTodoCmd, Matcher: "TodoWrite"})
		count++
	}

	if count == 0 {
		return 0, nil
	}

	// Write to file
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return 0, fmt.Errorf("failed to create .cursor directory: %w", err)
	}

	output, err := jsonutil.MarshalIndentWithNewline(hooksFile, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal hooks.json: %w", err)
	}

	if err := os.WriteFile(hooksPath, output, 0o600); err != nil {
		return 0, fmt.Errorf("failed to write hooks.json: %w", err)
	}

	return count, nil
}

// UninstallHooks removes Entire hooks from Cursor hooks.json.
func (c *CursorAgent) UninstallHooks() error {
	repoRoot, err := paths.RepoRoot()
	if err != nil {
		repoRoot = "."
	}
	hooksPath := filepath.Join(repoRoot, ".cursor", HooksFileName)
	data, err := os.ReadFile(hooksPath) //nolint:gosec // path is constructed from repo root + fixed path
	if err != nil {
		return nil //nolint:nilerr // No hooks file means nothing to uninstall
	}

	var hooksFile CursorHooksFile
	if err := json.Unmarshal(data, &hooksFile); err != nil {
		return fmt.Errorf("failed to parse hooks.json: %w", err)
	}

	// Remove Entire hooks from all hook types
	hooksFile.Hooks.SessionStart = removeEntireHooks(hooksFile.Hooks.SessionStart)
	hooksFile.Hooks.SessionEnd = removeEntireHooks(hooksFile.Hooks.SessionEnd)
	hooksFile.Hooks.BeforeSubmitPrompt = removeEntireHooks(hooksFile.Hooks.BeforeSubmitPrompt)
	hooksFile.Hooks.Stop = removeEntireHooks(hooksFile.Hooks.Stop)
	hooksFile.Hooks.PreToolUse = removeEntireHooks(hooksFile.Hooks.PreToolUse)
	hooksFile.Hooks.PostToolUse = removeEntireHooks(hooksFile.Hooks.PostToolUse)

	// Write back
	output, err := jsonutil.MarshalIndentWithNewline(hooksFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal hooks.json: %w", err)
	}

	if err := os.WriteFile(hooksPath, output, 0o600); err != nil {
		return fmt.Errorf("failed to write hooks.json: %w", err)
	}
	return nil
}

// AreHooksInstalled checks if Entire hooks are installed.
func (c *CursorAgent) AreHooksInstalled() bool {
	repoRoot, err := paths.RepoRoot()
	if err != nil {
		repoRoot = "."
	}
	hooksPath := filepath.Join(repoRoot, ".cursor", HooksFileName)
	data, err := os.ReadFile(hooksPath) //nolint:gosec // path is constructed from repo root + fixed path
	if err != nil {
		return false
	}

	var hooksFile CursorHooksFile
	if err := json.Unmarshal(data, &hooksFile); err != nil {
		return false
	}

	return hasEntireHook(hooksFile.Hooks.Stop) ||
		hasEntireHook(hooksFile.Hooks.SessionStart) ||
		hasEntireHook(hooksFile.Hooks.BeforeSubmitPrompt)
}

// GetSupportedHooks returns the hook types Cursor supports.
func (c *CursorAgent) GetSupportedHooks() []agent.HookType {
	return []agent.HookType{
		agent.HookSessionStart,
		agent.HookSessionEnd,
		agent.HookUserPromptSubmit,
		agent.HookStop,
		agent.HookPreToolUse,
		agent.HookPostToolUse,
	}
}

// Helper functions for hook management

func hookCommandExists(entries []CursorHookEntry, command string) bool {
	for _, entry := range entries {
		if entry.Command == command {
			return true
		}
	}
	return false
}

func hookCommandExistsWithMatcher(entries []CursorHookEntry, matcher, command string) bool {
	for _, entry := range entries {
		if entry.Matcher == matcher && entry.Command == command {
			return true
		}
	}
	return false
}

func isEntireHook(command string) bool {
	for _, prefix := range entireHookPrefixes {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func hasEntireHook(entries []CursorHookEntry) bool {
	for _, entry := range entries {
		if isEntireHook(entry.Command) {
			return true
		}
	}
	return false
}

func removeEntireHooks(entries []CursorHookEntry) []CursorHookEntry {
	result := make([]CursorHookEntry, 0, len(entries))
	for _, entry := range entries {
		if !isEntireHook(entry.Command) {
			result = append(result, entry)
		}
	}
	return result
}
