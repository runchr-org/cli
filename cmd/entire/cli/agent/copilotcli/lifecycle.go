package copilotcli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/logging"
)

// Ensure CopilotCLIAgent implements HookSupport at compile time.
var _ agent.HookSupport = (*CopilotCLIAgent)(nil)

// Copilot CLI hook names - these become subcommands under `entire hooks copilot-cli`
const (
	HookNameUserPromptSubmitted = "user-prompt-submitted"
	HookNameSessionStart        = "session-start"
	HookNameAgentStop           = "agent-stop"
	HookNameSessionEnd          = "session-end"
	HookNameSubagentStop        = "subagent-stop"
	HookNamePreToolUse          = "pre-tool-use"
	HookNamePostToolUse         = "post-tool-use"
	HookNameErrorOccurred       = "error-occurred"
)

// HookNames returns all hook verbs Copilot CLI supports.
// These become subcommands: entire hooks copilot-cli <verb>
func (c *CopilotCLIAgent) HookNames() []string {
	return []string{
		HookNameUserPromptSubmitted,
		HookNameSessionStart,
		HookNameAgentStop,
		HookNameSessionEnd,
		HookNameSubagentStop,
		HookNamePreToolUse,
		HookNamePostToolUse,
		HookNameErrorOccurred,
	}
}

// ParseHookEvent translates a Copilot CLI hook into a normalized lifecycle Event.
// Returns nil if the hook has no lifecycle significance (pass-through hooks).
//
// For VS Code payloads (detected via hookEventName), the event name is validated
// against the CLI subcommand. Mismatches are silently skipped to avoid processing
// a payload that doesn't match the hook being invoked.
func (c *CopilotCLIAgent) ParseHookEvent(ctx context.Context, hookName string, stdin io.Reader) (*agent.Event, error) {
	// Pass-through hooks: skip immediately without reading stdin.
	switch hookName {
	case HookNamePreToolUse, HookNamePostToolUse, HookNameErrorOccurred:
		return nil, nil //nolint:nilnil // Pass-through hooks have no lifecycle action
	}

	// For lifecycle hooks, read and parse the envelope first so we can
	// validate VS Code hookEventName before constructing an event.
	env, err := c.readHookEnvelope(stdin)
	if err != nil {
		return nil, err
	}

	// VS Code payloads: validate hookEventName matches the CLI subcommand.
	if env.Host == HostVSCode && env.HookEventName != "" {
		if !validateVSCodeEvent(env, hookName) {
			logging.Debug(ctx, "copilot-cli: skipping VS Code event with mismatched hookEventName",
				"hookEventName", env.HookEventName, "hookName", hookName)
			return nil, nil //nolint:nilnil // Mismatched VS Code event — skip silently.
		}
	}

	switch hookName {
	case HookNameUserPromptSubmitted:
		return c.buildUserPromptSubmitted(ctx, env), nil
	case HookNameSessionStart:
		return c.buildSessionStart(env), nil
	case HookNameAgentStop:
		return c.buildAgentStop(ctx, env), nil
	case HookNameSessionEnd:
		return c.buildSessionEnd(env), nil
	case HookNameSubagentStop:
		return c.buildSubagentStop(env), nil
	default:
		logging.Debug(ctx, "copilot-cli: ignoring unknown hook", "hook", hookName)
		return nil, nil //nolint:nilnil // Unknown hooks have no lifecycle action
	}
}

// --- Internal event builders (envelope already parsed) ---

func (c *CopilotCLIAgent) buildUserPromptSubmitted(ctx context.Context, env *hookEnvelope) *agent.Event {
	return &agent.Event{
		Type:       agent.TurnStart,
		SessionID:  env.SessionID,
		SessionRef: c.resolveTranscriptFromPayload(ctx, env),
		Prompt:     env.Prompt,
		Timestamp:  env.Timestamp,
	}
}

func (c *CopilotCLIAgent) buildSessionStart(env *hookEnvelope) *agent.Event {
	return &agent.Event{
		Type:      agent.SessionStart,
		SessionID: env.SessionID,
		Timestamp: env.Timestamp,
	}
}

func (c *CopilotCLIAgent) buildAgentStop(ctx context.Context, env *hookEnvelope) *agent.Event {
	transcriptRef := c.resolveTranscriptFromPayload(ctx, env)

	var model string
	if transcriptRef != "" {
		model = ExtractModelFromTranscript(ctx, transcriptRef)
	}

	return &agent.Event{
		Type:       agent.TurnEnd,
		SessionID:  env.SessionID,
		SessionRef: transcriptRef,
		Model:      model,
		Timestamp:  env.Timestamp,
	}
}

func (c *CopilotCLIAgent) buildSessionEnd(env *hookEnvelope) *agent.Event {
	return &agent.Event{
		Type:      agent.SessionEnd,
		SessionID: env.SessionID,
		Timestamp: env.Timestamp,
	}
}

func (c *CopilotCLIAgent) buildSubagentStop(env *hookEnvelope) *agent.Event {
	return &agent.Event{
		Type:      agent.SubagentEnd,
		SessionID: env.SessionID,
		Timestamp: env.Timestamp,
	}
}

func (c *CopilotCLIAgent) readHookEnvelope(stdin io.Reader) (*hookEnvelope, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("failed to read hook input: %w", err)
	}
	return parseHookEnvelope(data)
}

// resolveTranscriptFromPayload resolves the transcript file path for a hook event.
// It validates the payload's transcriptPath exists on disk; if not (e.g., a container
// path on a host where the file was mapped to a different directory via
// COPILOT_SESSION_STATE_DIR), it re-resolves via GetSessionDir. Falls back to the
// original payload path when neither location has the file, allowing downstream
// resolution (strategy layer) another chance.
func (c *CopilotCLIAgent) resolveTranscriptFromPayload(ctx context.Context, env *hookEnvelope) string {
	if env.TranscriptPath != "" {
		if _, err := os.Stat(env.TranscriptPath); err == nil {
			return env.TranscriptPath
		}
		// Payload path doesn't exist locally — try session-dir resolution.
		resolved := c.resolveTranscriptRef(ctx, env.SessionID)
		if resolved != "" && resolved != env.TranscriptPath {
			if _, err := os.Stat(resolved); err == nil {
				logging.Debug(ctx, "copilot-cli: resolved transcript to alternate session-state path",
					"payloadPath", env.TranscriptPath, "resolvedPath", resolved)
				return resolved
			}
		}
		// Neither path exists — keep payload path for downstream retry.
		return env.TranscriptPath
	}
	// No payload path — compute from session-dir.
	return c.resolveTranscriptRef(ctx, env.SessionID)
}

// AWFSessionStateDir is the well-known host path where GitHub's Agentic Workflow
// Firewall maps the container's Copilot session-state directory via --session-state-dir.
// Copilot CLI writes events.jsonl inside the container; AWF mounts this directory so
// the host (where Entire CLI hooks run) can access the transcript.
const AWFSessionStateDir = "/tmp/gh-aw/sandbox/agent/session-state"

// resolveTranscriptRef computes the transcript path from the session ID.
// It checks the primary session-state directory first (from GetSessionDir),
// then falls back to the AWF host-mapped path for containerized environments.
// Returns the primary path if neither location has the file (it may appear later).
func (c *CopilotCLIAgent) resolveTranscriptRef(ctx context.Context, sessionID string) string {
	sessionDir, err := c.GetSessionDir("")
	if err != nil {
		logging.Warn(ctx, "copilot-cli: failed to resolve transcript path", "sessionID", sessionID, "err", err)
		return ""
	}

	primary := c.ResolveSessionFile(sessionDir, sessionID)
	if _, err := os.Stat(primary); err == nil {
		return primary
	}

	// Check AWF host-mapped path for containerized environments.
	awfPath := c.ResolveSessionFile(AWFSessionStateDir, sessionID)
	if _, err := os.Stat(awfPath); err == nil {
		logging.Debug(ctx, "copilot-cli: found transcript at AWF session-state path",
			"primaryPath", primary, "awfPath", awfPath)
		return awfPath
	}

	return primary
}
