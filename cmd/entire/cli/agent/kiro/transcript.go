package kiro

import (
	"encoding/json"
	"fmt"
)

// kiroFileModificationTools is the set of tool names that create or modify files.
var kiroFileModificationTools = map[string]struct{}{
	"fs_write": {},
	"fs_edit":  {},
}

// parseTranscript unmarshals raw JSON into a kiroTranscript, supporting both
// Kiro CLI format (paired user+assistant entries) and Kiro IDE format
// (sequential {role, content} messages). IDE format is converted to CLI format
// so all downstream extraction functions work unchanged.
//
// Returns an empty transcript (not an error) for empty or "{}" input,
// matching the placeholder transcript created in IDE mode.
func parseTranscript(data []byte) (*kiroTranscript, error) {
	if len(data) == 0 {
		return &kiroTranscript{}, nil
	}

	// Detect format by checking which structure produces meaningful data.
	// Both formats have a "history" array, but CLI format has "user"/"assistant"
	// subfields while IDE format has "message" with "role"/"content".
	// Since json.Unmarshal silently ignores unknown fields, we try both and
	// check which one has non-empty content.

	// Try CLI format first (has "conversation_id" and paired user+assistant entries).
	var t kiroTranscript
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("failed to parse kiro transcript: %w", err)
	}
	if isCLITranscript(&t) {
		return &t, nil
	}

	// Try IDE format (sequential {role, content} messages).
	converted := tryParseIDETranscript(data)
	if converted != nil {
		return converted, nil
	}

	return &t, nil
}

// isCLITranscript checks whether a parsed kiroTranscript contains actual CLI-format
// data (with populated user/assistant fields) rather than zero-valued entries
// from unmarshalling a different format.
func isCLITranscript(t *kiroTranscript) bool {
	if len(t.History) == 0 {
		return false
	}
	// CLI format entries have user content; IDE format entries would have empty
	// user/assistant fields when unmarshalled into kiroHistoryEntry.
	return len(t.History[0].User.Content) > 0 || len(t.History[0].Assistant) > 0
}

// tryParseIDETranscript attempts to parse data as a Kiro IDE transcript.
// Returns the converted transcript if successful, or nil if the data doesn't
// contain IDE-format history entries.
func tryParseIDETranscript(data []byte) *kiroTranscript {
	var ide kiroIDETranscript
	if err := json.Unmarshal(data, &ide); err != nil {
		return nil
	}
	if len(ide.History) == 0 {
		return nil
	}
	// Verify it's actually IDE format by checking the first entry has a role.
	if ide.History[0].Message.Role == "" {
		return nil
	}
	return convertIDETranscript(&ide)
}

// convertIDETranscript converts IDE sequential messages into paired
// kiroHistoryEntry entries so downstream extraction functions work unchanged.
// It pairs consecutive user+assistant messages; unpaired messages at the end
// are included with empty counterparts.
func convertIDETranscript(ide *kiroIDETranscript) *kiroTranscript {
	t := &kiroTranscript{}

	var pendingUser *kiroIDEHistoryEntry
	for i := range ide.History {
		entry := &ide.History[i]
		role := entry.Message.Role

		switch role {
		case "user":
			// If we already have a pending user, flush it without an assistant.
			if pendingUser != nil {
				t.History = append(t.History, ideEntryToPaired(pendingUser, nil))
			}
			pendingUser = entry
		case "assistant":
			t.History = append(t.History, ideEntryToPaired(pendingUser, entry))
			pendingUser = nil
		}
	}

	// Flush any trailing user message without an assistant response.
	if pendingUser != nil {
		t.History = append(t.History, ideEntryToPaired(pendingUser, nil))
	}

	return t
}

// ideEntryToPaired converts an IDE user+assistant message pair into a
// kiroHistoryEntry. Either user or assistant may be nil.
func ideEntryToPaired(user, assistant *kiroIDEHistoryEntry) kiroHistoryEntry {
	entry := kiroHistoryEntry{}

	if user != nil {
		// Convert IDE user content to CLI format.
		// IDE: [{"type":"text","text":"..."}] → CLI: {"Prompt":{"prompt":"..."}}
		prompt := extractIDEUserText(user.Message.Content)
		if prompt != "" {
			content, marshalErr := json.Marshal(kiroPromptContent{
				Prompt: struct {
					Prompt string `json:"prompt"`
				}{Prompt: prompt},
			})
			if marshalErr == nil {
				entry.User.Content = content
			}
		} else {
			entry.User.Content = user.Message.Content
		}
	}

	if assistant != nil {
		// Convert IDE assistant content to CLI format.
		// IDE: "text string" → CLI: {"Response":{"message_id":"","content":"..."}}
		text := extractIDEAssistantText(assistant.Message.Content)
		if text != "" {
			content, marshalErr := json.Marshal(kiroResponseContent{
				Response: kiroResponsePayload{Content: text},
			})
			if marshalErr == nil {
				entry.Assistant = content
			}
		} else {
			entry.Assistant = assistant.Message.Content
		}
	}

	return entry
}

// extractIDEUserText extracts the text from an IDE user message's content.
// Handles both array format [{"type":"text","text":"..."}] and plain string.
func extractIDEUserText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	// Try array of content blocks.
	var blocks []kiroIDEContentBlock
	if err := json.Unmarshal(content, &blocks); err == nil && len(blocks) > 0 {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
		return ""
	}

	// Try plain string.
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}

	return ""
}

// extractIDEAssistantText extracts the text from an IDE assistant message's content.
// Handles both plain string and array format.
func extractIDEAssistantText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	// Try plain string (most common for assistant).
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}

	// Try array of content blocks.
	var blocks []kiroIDEContentBlock
	if err := json.Unmarshal(content, &blocks); err == nil && len(blocks) > 0 {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}

	return ""
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
	_, ok := kiroFileModificationTools[name]
	return ok
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
