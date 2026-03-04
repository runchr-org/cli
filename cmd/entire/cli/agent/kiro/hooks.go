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

// ideHooksDir is the directory within .kiro where IDE hook files live.
const ideHooksDir = "hooks"

// ideHookFileSuffix is the file extension for IDE hook files.
const ideHookFileSuffix = ".kiro.hook"

// ideHookVersion is the schema version for IDE hook files.
const ideHookVersion = "1"

// localDevCmdPrefix is the command prefix used for local development builds.
const localDevCmdPrefix = "go run ${KIRO_PROJECT_DIR}/cmd/entire/main.go "

// prodHookCmdPrefix is the command prefix for production hook commands.
const prodHookCmdPrefix = "entire hooks kiro "

// entireHookPrefixes identify Entire hooks in the config file.
var entireHookPrefixes = []string{
	"entire ",
	localDevCmdPrefix,
}

// ideHookDef defines a single IDE hook file to install.
type ideHookDef struct {
	Filename    string // e.g. "entire-prompt-submit"
	TriggerType string // e.g. "promptSubmit"
	CLIVerb     string // e.g. "user-prompt-submit"
}

// ideHookDefs lists the 4 IDE hook files to install.
// No agentSpawn IDE hook — the IDE has no such trigger.
// The first promptSubmit serves as session start.
var ideHookDefs = []ideHookDef{
	{Filename: "entire-prompt-submit", TriggerType: "promptSubmit", CLIVerb: HookNameUserPromptSubmit},
	{Filename: "entire-stop", TriggerType: "agentStop", CLIVerb: HookNameStop},
	{Filename: "entire-pre-tool-use", TriggerType: "preToolUse", CLIVerb: HookNamePreToolUse},
	{Filename: "entire-post-tool-use", TriggerType: "postToolUse", CLIVerb: HookNamePostToolUse},
}

// InstallHooks installs Entire hooks in .kiro/agents/entire.json (CLI hooks)
// and .kiro/hooks/*.kiro.hook (IDE hooks).
// Returns the total number of hooks installed (CLI + IDE).
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
			if json.Unmarshal(existing, &file) == nil && allHooksPresent(file.Hooks, localDev) && allIDEHooksPresent(worktreeRoot, localDev) {
				return 0, nil
			}
		}
	}

	cmdPrefix := hookCmdPrefix(localDev)

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

	// Install IDE hooks (.kiro/hooks/*.kiro.hook)
	ideCount, err := installIDEHooks(worktreeRoot, cmdPrefix)
	if err != nil {
		return 0, fmt.Errorf("failed to install IDE hooks: %w", err)
	}

	return len(k.HookNames()) + ideCount, nil
}

// UninstallHooks removes the Entire hooks config file and IDE hook files.
func (k *KiroAgent) UninstallHooks(ctx context.Context) error {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		worktreeRoot = "."
	}

	// Remove CLI agent file
	hooksPath := filepath.Join(worktreeRoot, ".kiro", hooksDir, HooksFileName)
	if err := os.Remove(hooksPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove hooks config: %w", err)
	}

	// Remove IDE hook files
	for _, def := range ideHookDefs {
		idePath := filepath.Join(worktreeRoot, ".kiro", ideHooksDir, def.Filename+ideHookFileSuffix)
		if err := os.Remove(idePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove IDE hook %s: %w", def.Filename, err)
		}
	}

	return nil
}

// AreHooksInstalled checks if Entire hooks are installed.
// Returns true if EITHER CLI agent hooks or IDE hooks are present.
func (k *KiroAgent) AreHooksInstalled(ctx context.Context) bool {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		worktreeRoot = "."
	}

	// Check CLI agent hooks
	hooksPath := filepath.Join(worktreeRoot, ".kiro", hooksDir, HooksFileName)
	data, err := os.ReadFile(hooksPath) //nolint:gosec // path constructed from repo root
	if err == nil {
		var file kiroAgentFile
		if json.Unmarshal(data, &file) == nil {
			if hasEntireHook(file.Hooks.AgentSpawn) ||
				hasEntireHook(file.Hooks.UserPromptSubmit) ||
				hasEntireHook(file.Hooks.Stop) {
				return true
			}
		}
	}

	// Check IDE hooks — any Entire IDE hook file means hooks are installed
	return anyIDEHookPresent(worktreeRoot)
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
	cmdPrefix := hookCmdPrefix(localDev)

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

func hookCmdPrefix(localDev bool) string {
	if localDev {
		return localDevCmdPrefix + "hooks kiro "
	}
	return prodHookCmdPrefix
}

// installIDEHooks creates .kiro/hooks/*.kiro.hook files for the Kiro IDE.
func installIDEHooks(worktreeRoot, cmdPrefix string) (int, error) {
	dir := filepath.Join(worktreeRoot, ".kiro", ideHooksDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, fmt.Errorf("failed to create .kiro/hooks directory: %w", err)
	}

	for _, def := range ideHookDefs {
		hook := kiroIDEHookFile{
			Enabled:     true,
			Name:        def.Filename,
			Description: "Entire CLI " + def.TriggerType + " hook",
			Version:     ideHookVersion,
			When: kiroIDEHookWhen{
				Type: def.TriggerType,
			},
			Then: kiroIDEHookThen{
				Type:    "runCommand",
				Command: cmdPrefix + def.CLIVerb,
			},
		}

		data, err := jsonutil.MarshalIndentWithNewline(hook, "", "  ")
		if err != nil {
			return 0, fmt.Errorf("failed to marshal IDE hook %s: %w", def.Filename, err)
		}

		path := filepath.Join(dir, def.Filename+ideHookFileSuffix)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return 0, fmt.Errorf("failed to write IDE hook %s: %w", def.Filename, err)
		}
	}

	return len(ideHookDefs), nil
}

// allIDEHooksPresent checks that all 4 IDE hook files exist and have correct commands.
func allIDEHooksPresent(worktreeRoot string, localDev bool) bool {
	cmdPrefix := hookCmdPrefix(localDev)

	for _, def := range ideHookDefs {
		path := filepath.Join(worktreeRoot, ".kiro", ideHooksDir, def.Filename+ideHookFileSuffix)
		data, err := os.ReadFile(path) //nolint:gosec // path constructed from repo root
		if err != nil {
			return false
		}
		var hook kiroIDEHookFile
		if err := json.Unmarshal(data, &hook); err != nil {
			return false
		}
		if hook.Then.Command != cmdPrefix+def.CLIVerb {
			return false
		}
	}
	return true
}

// anyIDEHookPresent checks if any Entire IDE hook file exists.
func anyIDEHookPresent(worktreeRoot string) bool {
	for _, def := range ideHookDefs {
		path := filepath.Join(worktreeRoot, ".kiro", ideHooksDir, def.Filename+ideHookFileSuffix)
		data, err := os.ReadFile(path) //nolint:gosec // path constructed from repo root
		if err != nil {
			continue
		}
		var hook kiroIDEHookFile
		if json.Unmarshal(data, &hook) == nil && isEntireIDEHook(hook) {
			return true
		}
	}
	return false
}

// isEntireIDEHook checks if an IDE hook file belongs to Entire.
func isEntireIDEHook(hook kiroIDEHookFile) bool {
	return strings.HasPrefix(hook.Name, "entire-") && isEntireHook(hook.Then.Command)
}
