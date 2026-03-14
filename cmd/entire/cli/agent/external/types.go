// Package external provides an adapter that bridges external agent binaries
// (discovered via PATH as entire-agent-<name>) to the agent.Agent interface.
// Communication uses a subcommand-based protocol with JSON over stdin/stdout.
package external

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// ProtocolVersion is the current protocol version expected by the CLI.
const ProtocolVersion = 1

// InfoResponse is the JSON returned by the "info" subcommand.
//
// Declarative fields allow external agents to avoid implementing subcommands
// for common patterns. When a declarative field is set, the CLI handles the
// logic internally instead of invoking a subcommand. This reduces the minimum
// required subcommands from 15 to just 2 (info + parse-hook).
//
// Declarative fields:
//   - detect_paths: replaces "detect" subcommand
//   - transcript_format: replaces "chunk-transcript" and "reassemble-transcript"
//   - session_dir_template: replaces "get-session-dir"
//   - session_file_template: replaces "resolve-session-file"
//   - resume_command_template: replaces "format-resume-command"
//
// Subcommand fallback: if a declarative field is empty, the CLI falls back to
// invoking the corresponding subcommand. Agents can mix declarative and
// subcommand-based approaches.
type InfoResponse struct {
	ProtocolVersion int                `json:"protocol_version"`
	Name            string             `json:"name"`
	Type            string             `json:"type"`
	Description     string             `json:"description"`
	IsPreview       bool               `json:"is_preview"`
	ProtectedDirs   []string           `json:"protected_dirs"`
	HookNames       []string           `json:"hook_names"`
	Capabilities    agent.DeclaredCaps `json:"capabilities"`

	// --- Declarative fields (all optional, replace subcommands when set) ---

	// DetectPaths lists repo-relative file/directory paths to check for agent presence.
	// If set, the CLI checks whether any of these paths exist under the repo root,
	// replacing the "detect" subcommand.
	// Example: [".myagent/config.json"]
	DetectPaths []string `json:"detect_paths,omitempty"`

	// TranscriptFormat declares the transcript format for built-in chunking/reassembly.
	// Supported values: "jsonl" (line-delimited JSON) or "json" (JSON with messages array).
	// If set, the CLI handles ChunkTranscript/ReassembleTranscript internally.
	TranscriptFormat string `json:"transcript_format,omitempty"`

	// SessionDirTemplate is a Go text/template that produces the session directory path.
	// Available variables: {{.RepoPath}} (absolute repo root), {{.RepoHash}} (SHA256 hex
	// of repo path), {{.HomeDir}} (user home directory).
	// Example: "{{.HomeDir}}/.myagent/sessions/{{.RepoHash}}"
	SessionDirTemplate string `json:"session_dir_template,omitempty"`

	// SessionFileTemplate is a Go text/template that produces the session file path.
	// Available variables: {{.SessionDir}} (result of session_dir_template or get-session-dir),
	// {{.SessionID}} (agent session ID).
	// Example: "{{.SessionDir}}/{{.SessionID}}.jsonl"
	SessionFileTemplate string `json:"session_file_template,omitempty"`

	// ResumeCommandTemplate is a Go text/template for the resume command string.
	// Available variables: {{.SessionID}} (session ID to resume).
	// Example: "myagent --resume {{.SessionID}}"
	ResumeCommandTemplate string `json:"resume_command_template,omitempty"`
}

// DetectResponse is the JSON returned by the "detect" subcommand.
type DetectResponse struct {
	Present bool `json:"present"`
}

// SessionIDResponse is the JSON returned by the "get-session-id" subcommand.
type SessionIDResponse struct {
	SessionID string `json:"session_id"`
}

// SessionDirResponse is the JSON returned by the "get-session-dir" subcommand.
type SessionDirResponse struct {
	SessionDir string `json:"session_dir"`
}

// SessionFileResponse is the JSON returned by the "resolve-session-file" subcommand.
type SessionFileResponse struct {
	SessionFile string `json:"session_file"`
}

// ChunkResponse is the JSON returned by the "chunk-transcript" subcommand.
type ChunkResponse struct {
	Chunks [][]byte `json:"chunks"`
}

// ResumeCommandResponse is the JSON returned by the "format-resume-command" subcommand.
type ResumeCommandResponse struct {
	Command string `json:"command"`
}

// HooksInstalledCountResponse is the JSON returned by the "install-hooks" subcommand.
type HooksInstalledCountResponse struct {
	HooksInstalled int `json:"hooks_installed"`
}

// AreHooksInstalledResponse is the JSON returned by the "are-hooks-installed" subcommand.
type AreHooksInstalledResponse struct {
	Installed bool `json:"installed"`
}

// TranscriptPositionResponse is the JSON returned by the "get-transcript-position" subcommand.
type TranscriptPositionResponse struct {
	Position int `json:"position"`
}

// ExtractFilesResponse is the JSON returned by file-extraction subcommands.
type ExtractFilesResponse struct {
	Files           []string `json:"files"`
	CurrentPosition int      `json:"current_position"`
}

// ExtractPromptsResponse is the JSON returned by the "extract-prompts" subcommand.
type ExtractPromptsResponse struct {
	Prompts []string `json:"prompts"`
}

// ExtractSummaryResponse is the JSON returned by the "extract-summary" subcommand.
type ExtractSummaryResponse struct {
	Summary    string `json:"summary"`
	HasSummary bool   `json:"has_summary"`
}

// TokenUsageResponse is the JSON returned by token calculation subcommands.
type TokenUsageResponse struct {
	InputTokens         int                 `json:"input_tokens"`
	CacheCreationTokens int                 `json:"cache_creation_tokens"`
	CacheReadTokens     int                 `json:"cache_read_tokens"`
	OutputTokens        int                 `json:"output_tokens"`
	APICallCount        int                 `json:"api_call_count"`
	SubagentTokens      *TokenUsageResponse `json:"subagent_tokens,omitempty"`
}

// GenerateTextResponse is the JSON returned by the "generate-text" subcommand.
type GenerateTextResponse struct {
	Text string `json:"text"`
}

// AgentSessionJSON is the JSON representation of agent.AgentSession for stdin/stdout transfer.
type AgentSessionJSON struct {
	SessionID     string   `json:"session_id"`
	AgentName     string   `json:"agent_name"`
	RepoPath      string   `json:"repo_path"`
	SessionRef    string   `json:"session_ref"`
	StartTime     string   `json:"start_time"`
	NativeData    []byte   `json:"native_data"`
	ModifiedFiles []string `json:"modified_files"`
	NewFiles      []string `json:"new_files"`
	DeletedFiles  []string `json:"deleted_files"`
}

// eventJSON is the JSON-tagged representation of agent.Event for protocol transfer.
type eventJSON struct {
	Type              int               `json:"type"`
	SessionID         string            `json:"session_id"`
	PreviousSessionID string            `json:"previous_session_id,omitempty"`
	SessionRef        string            `json:"session_ref,omitempty"`
	Prompt            string            `json:"prompt,omitempty"`
	Model             string            `json:"model,omitempty"`
	Timestamp         string            `json:"timestamp,omitempty"`
	ToolUseID         string            `json:"tool_use_id,omitempty"`
	SubagentID        string            `json:"subagent_id,omitempty"`
	ToolInput         json.RawMessage   `json:"tool_input,omitempty"`
	SubagentType      string            `json:"subagent_type,omitempty"`
	TaskDescription   string            `json:"task_description,omitempty"`
	ResponseMessage   string            `json:"response_message,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

func (ej *eventJSON) toEvent() (*agent.Event, error) {
	ev := &agent.Event{
		Type:              agent.EventType(ej.Type),
		SessionID:         ej.SessionID,
		PreviousSessionID: ej.PreviousSessionID,
		SessionRef:        ej.SessionRef,
		Prompt:            ej.Prompt,
		Model:             ej.Model,
		ToolUseID:         ej.ToolUseID,
		SubagentID:        ej.SubagentID,
		ToolInput:         ej.ToolInput,
		SubagentType:      ej.SubagentType,
		TaskDescription:   ej.TaskDescription,
		ResponseMessage:   ej.ResponseMessage,
		Metadata:          ej.Metadata,
	}
	if ej.Timestamp != "" {
		t, err := time.Parse(time.RFC3339, ej.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("invalid timestamp: %w", err)
		}
		ev.Timestamp = t
	}
	return ev, nil
}

// HookInputJSON is the JSON representation of agent.HookInput for stdin/stdout transfer.
type HookInputJSON struct {
	HookType   string                 `json:"hook_type"`
	SessionID  string                 `json:"session_id"`
	SessionRef string                 `json:"session_ref"`
	Timestamp  string                 `json:"timestamp"`
	UserPrompt string                 `json:"user_prompt,omitempty"`
	ToolName   string                 `json:"tool_name,omitempty"`
	ToolUseID  string                 `json:"tool_use_id,omitempty"`
	ToolInput  json.RawMessage        `json:"tool_input,omitempty"`
	RawData    map[string]interface{} `json:"raw_data,omitempty"`
}
