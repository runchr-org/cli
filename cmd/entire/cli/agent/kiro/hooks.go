package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

// Ensure KiroAgent implements HookSupport
var _ agent.HookSupport = (*KiroAgent)(nil)

// HooksFileName is the config file for Kiro hooks.
const HooksFileName = "entire.json"

// hooksDir is the directory within .kiro where agent hook configs live.
const hooksDir = "agents"

// localDevCmdPrefix is the command prefix used for local development builds.
const localDevCmdPrefix = "go run ${KIRO_PROJECT_DIR}/cmd/entire/main.go "

// entireHookPrefixes identify Entire hooks in the config file.
var entireHookPrefixes = []string{
	"entire ",
	localDevCmdPrefix,
}

// InstallHooks installs Entire hooks in .kiro/agents/entire.json.
// Since Entire owns this file entirely, we write it from scratch each time.
// Returns the number of hooks installed.
func (k *KiroAgent) InstallHooks(ctx context.Context, localDev bool, force bool) (int, error) {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		worktreeRoot = "."
	}

	hooksPath := filepath.Join(worktreeRoot, ".kiro", hooksDir, HooksFileName)

	// If hooks are already installed and not forcing, check if they're current
	if !force {
		if existing, readErr := os.ReadFile(hooksPath); readErr == nil { //nolint:gosec // path constructed from repo root
			var file kiroAgentFile
			if json.Unmarshal(existing, &file) == nil && allHooksPresent(file.Hooks, localDev) {
				return 0, nil
			}
		}
	}

	var cmdPrefix string
	if localDev {
		cmdPrefix = localDevCmdPrefix + "hooks kiro "
	} else {
		cmdPrefix = "entire hooks kiro "
	}

	file := kiroAgentFile{
		Name: "entire",
		// Include all default Kiro tools so the agent profile doesn't restrict them.
		Tools: []string{
			"read", "write", "shell", "grep", "glob",
			"aws", "report", "introspect", "knowledge",
			"thinking", "todo", "delegate",
		},
		Hooks: kiroHooks{
			AgentSpawn:       []kiroHookEntry{{Command: cmdPrefix + HookNameAgentSpawn}},
			UserPromptSubmit: []kiroHookEntry{{Command: cmdPrefix + HookNameUserPromptSubmit}},
			PreToolUse:       []kiroHookEntry{{Command: cmdPrefix + HookNamePreToolUse}},
			PostToolUse:      []kiroHookEntry{{Command: cmdPrefix + HookNamePostToolUse}},
			Stop:             []kiroHookEntry{{Command: cmdPrefix + HookNameStop}},
		},
	}

	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return 0, fmt.Errorf("failed to create .kiro/agents directory: %w", err)
	}

	output, err := jsonutil.MarshalIndentWithNewline(file, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal hooks config: %w", err)
	}

	if err := os.WriteFile(hooksPath, output, 0o600); err != nil {
		return 0, fmt.Errorf("failed to write hooks config: %w", err)
	}

	return len(k.HookNames()), nil
}

// UninstallHooks removes the Entire hooks config file.
func (k *KiroAgent) UninstallHooks(ctx context.Context) error {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		worktreeRoot = "."
	}

	hooksPath := filepath.Join(worktreeRoot, ".kiro", hooksDir, HooksFileName)
	if err := os.Remove(hooksPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove hooks config: %w", err)
	}
	return nil
}

// AreHooksInstalled checks if Entire hooks are installed.
func (k *KiroAgent) AreHooksInstalled(ctx context.Context) bool {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		worktreeRoot = "."
	}

	hooksPath := filepath.Join(worktreeRoot, ".kiro", hooksDir, HooksFileName)
	data, err := os.ReadFile(hooksPath) //nolint:gosec // path constructed from repo root
	if err != nil {
		return false
	}

	var file kiroAgentFile
	if err := json.Unmarshal(data, &file); err != nil {
		return false
	}

	return hasEntireHook(file.Hooks.AgentSpawn) ||
		hasEntireHook(file.Hooks.UserPromptSubmit) ||
		hasEntireHook(file.Hooks.Stop)
}

// GetSupportedHooks returns the hook types Kiro supports.
func (k *KiroAgent) GetSupportedHooks() []agent.HookType {
	return []agent.HookType{
		agent.HookSessionStart,
		agent.HookUserPromptSubmit,
		agent.HookPreToolUse,
		agent.HookPostToolUse,
		agent.HookStop,
	}
}

func allHooksPresent(hooks kiroHooks, localDev bool) bool {
	var cmdPrefix string
	if localDev {
		cmdPrefix = localDevCmdPrefix + "hooks kiro "
	} else {
		cmdPrefix = "entire hooks kiro "
	}

	return hookCommandExists(hooks.AgentSpawn, cmdPrefix+HookNameAgentSpawn) &&
		hookCommandExists(hooks.UserPromptSubmit, cmdPrefix+HookNameUserPromptSubmit) &&
		hookCommandExists(hooks.PreToolUse, cmdPrefix+HookNamePreToolUse) &&
		hookCommandExists(hooks.PostToolUse, cmdPrefix+HookNamePostToolUse) &&
		hookCommandExists(hooks.Stop, cmdPrefix+HookNameStop)
}

func hookCommandExists(entries []kiroHookEntry, command string) bool {
	for _, entry := range entries {
		if entry.Command == command {
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

func hasEntireHook(entries []kiroHookEntry) bool {
	for _, entry := range entries {
		if isEntireHook(entry.Command) {
			return true
		}
	}
	return false
}
