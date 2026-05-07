package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Compile-time interface assertions.
var (
	_ agent.HookSupport        = (*CodexAgent)(nil)
	_ agent.HookResponseWriter = (*CodexAgent)(nil)
)

// WriteHookResponse outputs a JSON hook response to stdout.
// Codex reads the systemMessage field and displays it to the user.
func (c *CodexAgent) WriteHookResponse(message string) error {
	resp := struct {
		SystemMessage string `json:"systemMessage,omitempty"`
	}{SystemMessage: message}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		return fmt.Errorf("failed to encode hook response: %w", err)
	}
	return nil
}

// Codex hook names — these become subcommands under `entire hooks codex`
const (
	HookNameSessionStart     = "session-start"
	HookNameUserPromptSubmit = "user-prompt-submit"
	HookNameStop             = "stop"
	HookNamePreToolUse       = "pre-tool-use"
	HookNamePostToolUse      = "post-tool-use"
)

// HookNames returns the hook verbs Codex supports.
func (c *CodexAgent) HookNames() []string {
	return []string{
		HookNameSessionStart,
		HookNameUserPromptSubmit,
		HookNameStop,
		HookNamePreToolUse,
		HookNamePostToolUse,
	}
}

// ParseHookEvent translates a Codex hook into a normalized lifecycle Event.
// Returns nil if the hook has no lifecycle significance.
func (c *CodexAgent) ParseHookEvent(_ context.Context, hookName string, stdin io.Reader) (*agent.Event, error) {
	switch hookName {
	case HookNameSessionStart:
		return c.parseSessionStart(stdin)
	case HookNameUserPromptSubmit:
		return c.parseTurnStart(stdin)
	case HookNameStop:
		return c.parseTurnEnd(stdin)
	case HookNamePreToolUse:
		// PreToolUse has no lifecycle significance — pass through
		return nil, nil //nolint:nilnil // nil event = no lifecycle action
	case HookNamePostToolUse:
		return c.parsePostToolUse(stdin)
	default:
		return nil, nil //nolint:nilnil // Unknown hooks have no lifecycle action
	}
}

func (c *CodexAgent) parseSessionStart(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[sessionStartRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.SessionStart,
		SessionID:  raw.SessionID,
		SessionRef: derefString(raw.TranscriptPath),
		Model:      raw.Model,
		Timestamp:  time.Now(),
	}, nil
}

func (c *CodexAgent) parseTurnStart(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[userPromptSubmitRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.TurnStart,
		SessionID:  raw.SessionID,
		SessionRef: derefString(raw.TranscriptPath),
		Prompt:     raw.Prompt,
		Model:      raw.Model,
		Timestamp:  time.Now(),
	}, nil
}

// parsePostToolUse turns a Codex PostToolUse hook into a ToolUse lifecycle event.
//
// Codex fires PostToolUse for every tool the agent runs (apply_patch, shell,
// unified_exec, MCP tools). We only care about file mutations: apply_patch
// (canonical name) and its matcher aliases Write/Edit (Codex accepts those for
// Claude-style hook configs — see codex-rs/core/src/tools/hook_names.rs).
// Other tool names produce a no-op event (nil) so the dispatcher skips them.
//
// The patch envelope arrives in tool_input.command using Codex's plain-text
// format (`*** Add File: …`, `*** Update File: …`, `*** Delete File: …`).
// Path classification matters here: Add → NewFiles, Delete → DeletedFiles,
// Update → ModifiedFiles. The strategy's mergeFilesTouched dedups across the
// three buckets, so misclassification only loses signal — but keeping them
// separate matches what SaveStep does at TurnEnd and lets future consumers
// reason about agent intent.
func (c *CodexAgent) parsePostToolUse(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[postToolUseRaw](stdin)
	if err != nil {
		return nil, err
	}

	if !isApplyPatchTool(raw.ToolName) {
		return nil, nil //nolint:nilnil // non-mutating tools have no lifecycle action
	}

	var input applyPatchToolInput
	if len(raw.ToolInput) > 0 {
		// Best-effort: an unparseable tool_input means we can't extract files,
		// but we shouldn't fail the hook (which would block the agent's tool call).
		_ = json.Unmarshal(raw.ToolInput, &input) //nolint:errcheck // input.Command stays empty on failure
	}
	if input.Command == "" {
		return nil, nil //nolint:nilnil // no patch envelope, nothing to record
	}

	added, modified, deleted := classifyApplyPatchPaths(input.Command)
	if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
		return nil, nil //nolint:nilnil // empty patch — no files to record
	}

	return &agent.Event{
		Type:          agent.ToolUse,
		SessionID:     raw.SessionID,
		SessionRef:    derefString(raw.TranscriptPath),
		Model:         raw.Model,
		ToolUseID:     raw.ToolUseID,
		CWD:           raw.CWD,
		ModifiedFiles: modified,
		NewFiles:      added,
		DeletedFiles:  deleted,
		Timestamp:     time.Now(),
	}, nil
}

// isApplyPatchTool reports whether a Codex PostToolUse tool_name represents an
// apply_patch invocation. The canonical name is "apply_patch"; Write and Edit
// are matcher aliases Codex accepts for compatibility with Claude-style hook
// configs (see codex-rs/core/src/tools/hook_names.rs:apply_patch).
func isApplyPatchTool(name string) bool {
	switch name {
	case "apply_patch", "Write", "Edit":
		return true
	default:
		return false
	}
}

func (c *CodexAgent) parseTurnEnd(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[stopRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.TurnEnd,
		SessionID:  raw.SessionID,
		SessionRef: derefString(raw.TranscriptPath),
		Model:      raw.Model,
		Timestamp:  time.Now(),
	}, nil
}
