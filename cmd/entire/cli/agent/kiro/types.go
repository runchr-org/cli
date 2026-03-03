package kiro

import "encoding/json"

// hookInputRaw matches Kiro's hook stdin JSON payload.
// All hooks receive the same structure; fields are populated based on the event.
type hookInputRaw struct {
	HookEventName string `json:"hook_event_name"`
	CWD           string `json:"cwd"`
	Prompt        string `json:"prompt,omitempty"`
	ToolName      string `json:"tool_name,omitempty"`
	ToolInput     string `json:"tool_input,omitempty"`
	ToolResponse  string `json:"tool_response,omitempty"`
}

// kiroAgentFile represents the .kiro/agents/entire.json structure.
// This is a Kiro agent definition file — hooks are nested under the "hooks" field.
// Entire owns this file entirely — no round-trip preservation needed.
type kiroAgentFile struct {
	Name  string    `json:"name"`
	Tools []string  `json:"tools"`
	Hooks kiroHooks `json:"hooks"`
}

// kiroHooks contains all hook configurations using camelCase keys.
type kiroHooks struct {
	AgentSpawn       []kiroHookEntry `json:"agentSpawn,omitempty"`
	UserPromptSubmit []kiroHookEntry `json:"userPromptSubmit,omitempty"`
	PreToolUse       []kiroHookEntry `json:"preToolUse,omitempty"`
	PostToolUse      []kiroHookEntry `json:"postToolUse,omitempty"`
	Stop             []kiroHookEntry `json:"stop,omitempty"`
}

// kiroHookEntry represents a single hook command in the config file.
type kiroHookEntry struct {
	Command string `json:"command"`
}

// kiroIDEHookFile represents a .kiro/hooks/*.kiro.hook file for the Kiro IDE.
// Unlike CLI agent hooks (nested in entire.json), IDE hooks are standalone files
// with a when/then structure. The IDE delivers data via environment variables
// (e.g., USER_PROMPT) rather than JSON on stdin.
type kiroIDEHookFile struct {
	Enabled     bool            `json:"enabled"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Version     string          `json:"version"`
	When        kiroIDEHookWhen `json:"when"`
	Then        kiroIDEHookThen `json:"then"`
}

// kiroIDEHookWhen defines the trigger condition for an IDE hook.
type kiroIDEHookWhen struct {
	Type string `json:"type"`
}

// kiroIDEHookThen defines the action for an IDE hook.
type kiroIDEHookThen struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// --- Transcript types ---
// These types model the Kiro SQLite-cached conversation JSON.
// The format uses paired user+assistant history entries with tagged unions
// for content variants (Prompt vs ToolUseResults, Response vs ToolUse).

// kiroTranscript is the top-level transcript structure cached from SQLite.
type kiroTranscript struct {
	ConversationID string             `json:"conversation_id"`
	History        []kiroHistoryEntry `json:"history"`
}

// kiroHistoryEntry is a single user+assistant exchange in the conversation.
type kiroHistoryEntry struct {
	User      kiroUserMessage `json:"user"`
	Assistant json.RawMessage `json:"assistant"`
}

// kiroUserMessage wraps the user's contribution in a history entry.
type kiroUserMessage struct {
	Content   json.RawMessage `json:"content"`
	Timestamp string          `json:"timestamp,omitempty"`
}

// kiroPromptContent represents the {"Prompt": {"prompt": "..."}} user content variant.
type kiroPromptContent struct {
	Prompt struct {
		Prompt string `json:"prompt"`
	} `json:"Prompt"`
}

// kiroToolUseContent represents the {"ToolUse": {...}} assistant content variant.
type kiroToolUseContent struct {
	ToolUse kiroToolUsePayload `json:"ToolUse"`
}

// kiroToolUsePayload carries tool call details within a ToolUse assistant message.
type kiroToolUsePayload struct {
	MessageID string         `json:"message_id"`
	ToolUses  []kiroToolCall `json:"tool_uses"`
}

// kiroResponseContent represents the {"Response": {...}} assistant content variant.
type kiroResponseContent struct {
	Response kiroResponsePayload `json:"Response"`
}

// kiroResponsePayload carries text content within a Response assistant message.
type kiroResponsePayload struct {
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
}

// kiroToolCall represents a single tool invocation within a ToolUse message.
type kiroToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}
