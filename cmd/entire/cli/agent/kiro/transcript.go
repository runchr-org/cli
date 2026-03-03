package kiro

import (
	"encoding/json"
	"fmt"
)

// kiroFileModificationTools lists tool names that create or modify files.
var kiroFileModificationTools = []string{"fs_write", "fs_edit"}

// parseTranscript unmarshals raw JSON into a kiroTranscript.
// Returns an empty transcript (not an error) for empty or "{}" input,
// matching the placeholder transcript created in IDE mode.
func parseTranscript(data []byte) (*kiroTranscript, error) {
	if len(data) == 0 {
		return &kiroTranscript{}, nil
	}

	var t kiroTranscript
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("failed to parse kiro transcript: %w", err)
	}
	return &t, nil
}

// extractUserPrompt tries to extract a prompt string from a user message's
// raw content. Returns "" if the content is a ToolUseResults variant or
// cannot be parsed.
func extractUserPrompt(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	var pc kiroPromptContent
	if err := json.Unmarshal(content, &pc); err == nil && pc.Prompt.Prompt != "" {
		return pc.Prompt.Prompt
	}
	return ""
}

// extractModifiedFilesFromHistory returns deduplicated file paths modified by
// tool calls across the given history entries.
func extractModifiedFilesFromHistory(entries []kiroHistoryEntry) []string {
	seen := make(map[string]bool)
	var files []string

	for i := range entries {
		for _, path := range extractFilesFromAssistant(entries[i].Assistant) {
			if path != "" && !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
		}
	}
	return files
}

// extractFilesFromAssistant extracts file paths from an assistant message's
// raw JSON. Returns nil if the message is not a ToolUse variant.
func extractFilesFromAssistant(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var tc kiroToolUseContent
	if err := json.Unmarshal(raw, &tc); err != nil || len(tc.ToolUse.ToolUses) == 0 {
		return nil
	}

	var paths []string
	for _, call := range tc.ToolUse.ToolUses {
		if !isFileModificationTool(call.Name) {
			continue
		}
		if p := extractFilePath(call.Args); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// isFileModificationTool reports whether the tool name is a file-modifying tool.
func isFileModificationTool(name string) bool {
	for _, t := range kiroFileModificationTools {
		if name == t {
			return true
		}
	}
	return false
}

// extractFilePath extracts a file path from tool call args JSON.
// Checks "path", "file_path", and "filename" keys in order.
func extractFilePath(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}

	for _, key := range []string{"path", "file_path", "filename"} {
		raw, ok := m[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
	}
	return ""
}

// extractLastAssistantResponse walks the history backward and returns the
// content of the last Response-type assistant message.
func extractLastAssistantResponse(entries []kiroHistoryEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if len(entries[i].Assistant) == 0 {
			continue
		}
		var rc kiroResponseContent
		if err := json.Unmarshal(entries[i].Assistant, &rc); err == nil && rc.Response.Content != "" {
			return rc.Response.Content
		}
	}
	return ""
}
