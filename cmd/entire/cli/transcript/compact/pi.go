package compact

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// --- pi format support ---
//
// Pi sessions are JSONL with a tree-shaped entry layout. Each entry has a
// top-level type ("session", "message", "model_change", "thinking_level_change",
// "compaction", "branch_summary", "label", "custom", "custom_message",
// "session_info") and a parentId pointer.
//
// Branching: when the user forks/branches mid-conversation the JSONL
// accumulates entries from BOTH branches. Compaction must walk only the
// active branch (root → most-recent message) so abandoned tool calls don't
// pollute the compact transcript.
//
// Ported from github.com/entireio/external-agents/agents/entire-agent-pi
// (internal/pi/compact.go) so the in-tree built-in agent and the external
// plugin produce byte-identical compact transcripts.

const (
	piEntryTypeMessage    = "message"
	piEntryTypeSession    = "session"
	piRoleUser            = "user"
	piRoleAssistant       = "assistant"
	piRoleToolResult      = "toolResult"
	piContentTypeText     = "text"
	piToolResultStatusOK  = "success"
	piToolResultStatusErr = "error"
)

// piToolNameMap normalises Pi's lowercase tool names to the title-cased names
// used elsewhere in Entire's compact format (matching Claude's "Read"/"Write"/"Edit").
var piToolNameMap = map[string]string{
	"edit":  "Edit",
	"read":  "Read",
	"write": "Write",
}

const piMaxScannerLine = 1 << 20

func piNewScanner(data []byte) *bufio.Scanner {
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 0, piMaxScannerLine), piMaxScannerLine)
	return s
}

// isPiFormat reports whether content looks like a Pi session JSONL file.
// Anchored on the persisted session header that pi writes as the first line.
func isPiFormat(content []byte) bool {
	scanner := piNewScanner(content)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type    string `json:"type"`
			Version int    `json:"version"`
		}
		if json.Unmarshal(line, &probe) != nil {
			return false
		}
		// Pi auto-migrates v1/v2 to v3 on load; accept any positive version.
		return probe.Type == piEntryTypeSession && probe.Version > 0
	}
	return false
}

type piMessageEntry struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Timestamp string    `json:"timestamp"`
	Message   piMessage `json:"message"`
}

type piMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Usage      *piMsgUsage     `json:"usage,omitempty"`
	StopReason string          `json:"stopReason,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
}

type piMsgUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

type piContentItem struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Name      string          `json:"name,omitempty"`
	ID        string          `json:"id,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// --- output structures (compact format) ---

type piCompactUserBlock struct {
	Text string `json:"text"`
}

type piCompactAssistantTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type piCompactToolUseBlock struct {
	Type   string               `json:"type"`
	ID     string               `json:"id,omitempty"`
	Name   string               `json:"name"`
	Input  any                  `json:"input"`
	Result *piCompactToolResult `json:"result,omitempty"`
}

type piCompactToolResult struct {
	Output string `json:"output"`
	Status string `json:"status"`
}

// compactPi converts a Pi JSONL transcript into the Entire compact format.
//
// opts.StartLine is treated as a JSONL line offset.
func compactPi(content []byte, opts MetadataFields) ([]byte, error) {
	if opts.StartLine > 0 {
		content = piSkipLines(content, opts.StartLine)
		if content == nil {
			return []byte{}, nil
		}
	}

	active := piResolveActiveBranch(content)
	results, err := piCollectToolResults(content, active)
	if err != nil {
		return nil, err
	}

	base := newTranscriptLine(opts)
	var out []byte

	scanner := piNewScanner(content)
	for scanner.Scan() {
		var entry piMessageEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != piEntryTypeMessage {
			continue
		}
		if active != nil && !active[entry.ID] {
			continue
		}

		switch entry.Message.Role {
		case piRoleUser:
			blocks := piEmitUserContent(entry.Message.Content)
			if len(blocks) == 0 {
				continue
			}
			contentJSON, err := json.Marshal(blocks)
			if err != nil {
				return nil, fmt.Errorf("marshal pi user content: %w", err)
			}
			line := base
			line.Type = piRoleUser
			line.TS = piTimestampJSON(entry.Timestamp)
			line.Content = contentJSON
			appendLine(&out, line)

		case piRoleAssistant:
			blocks := piEmitAssistantContent(entry.Message.Content, results)
			if len(blocks) == 0 {
				continue
			}
			contentJSON, err := json.Marshal(blocks)
			if err != nil {
				return nil, fmt.Errorf("marshal pi assistant content: %w", err)
			}
			line := base
			line.Type = piRoleAssistant
			line.TS = piTimestampJSON(entry.Timestamp)
			line.ID = entry.ID
			line.Content = contentJSON
			if entry.Message.Usage != nil {
				line.InputTokens = entry.Message.Usage.Input
				line.OutputTokens = entry.Message.Usage.Output
			}
			appendLine(&out, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan pi transcript: %w", err)
	}
	return out, nil
}

// piEmitUserContent decodes a Pi user message's content (string or block array)
// into compact user blocks.
func piEmitUserContent(raw json.RawMessage) []piCompactUserBlock {
	if text := piDecodeString(raw); text != "" {
		return []piCompactUserBlock{{Text: text}}
	}
	var items []piContentItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	blocks := make([]piCompactUserBlock, 0, len(items))
	for _, item := range items {
		if item.Type == piContentTypeText && item.Text != "" {
			blocks = append(blocks, piCompactUserBlock{Text: item.Text})
		}
	}
	return blocks
}

// piEmitAssistantContent decodes a Pi assistant message's content into
// compact assistant blocks (text + tool_use). Tool results are spliced in
// from `results` keyed by toolCallID.
func piEmitAssistantContent(raw json.RawMessage, results map[string]piCompactToolResult) []any {
	if text := piDecodeString(raw); text != "" {
		return []any{piCompactAssistantTextBlock{Type: piContentTypeText, Text: text}}
	}
	var items []piContentItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	blocks := make([]any, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case piContentTypeText:
			if item.Text != "" {
				blocks = append(blocks, piCompactAssistantTextBlock{
					Type: piContentTypeText,
					Text: item.Text,
				})
			}
		case "toolCall":
			block := piCompactToolUseBlock{
				Type:  "tool_use",
				ID:    item.ID,
				Name:  piNormalizeToolName(item.Name),
				Input: piDecodeArguments(item.Arguments),
			}
			if r, ok := results[item.ID]; ok {
				block.Result = &r
			}
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// piCollectToolResults walks the transcript and returns a map of tool-call id
// to spliceable result. Branch-aware.
func piCollectToolResults(data []byte, active map[string]bool) (map[string]piCompactToolResult, error) {
	results := map[string]piCompactToolResult{}
	scanner := piNewScanner(data)
	for scanner.Scan() {
		var entry piMessageEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != piEntryTypeMessage || entry.Message.Role != piRoleToolResult {
			continue
		}
		if active != nil && !active[entry.ID] {
			continue
		}
		if entry.Message.ToolCallID == "" {
			continue
		}
		results[entry.Message.ToolCallID] = piCompactToolResult{
			Output: piDecodeResultOutput(entry.Message.Content),
			Status: piResultStatus(entry.Message.IsError),
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan pi tool results: %w", err)
	}
	return results, nil
}

// piResolveActiveBranch returns the set of message IDs on the active branch
// (root → most-recent message). Returns nil if the transcript has no tree
// references — caller treats nil as "all entries are active".
func piResolveActiveBranch(data []byte) map[string]bool {
	type node struct {
		Type     string  `json:"type"`
		ID       string  `json:"id"`
		ParentID *string `json:"parentId"`
	}
	var lastMessageID string
	hasTree := false
	parentOf := make(map[string]string)

	scanner := piNewScanner(data)
	for scanner.Scan() {
		var n node
		if err := json.Unmarshal(scanner.Bytes(), &n); err != nil || n.ID == "" {
			continue
		}
		if n.ParentID != nil {
			parentOf[n.ID] = *n.ParentID
			if *n.ParentID != "" {
				hasTree = true
			}
		}
		if n.Type == piEntryTypeMessage {
			lastMessageID = n.ID
		}
	}
	if !hasTree || lastMessageID == "" {
		return nil
	}
	active := make(map[string]bool)
	for cur := lastMessageID; cur != ""; {
		if active[cur] {
			break // cycle protection
		}
		active[cur] = true
		parent, ok := parentOf[cur]
		if !ok {
			break
		}
		cur = parent
	}
	return active
}

// piSkipLines removes the first n newline-terminated lines from data.
func piSkipLines(data []byte, n int) []byte {
	if n <= 0 {
		return data
	}
	off := 0
	for i := 0; i < n && off < len(data); i++ {
		idx := bytes.IndexByte(data[off:], '\n')
		if idx < 0 {
			return nil
		}
		off += idx + 1
	}
	return data[off:]
}

func piNormalizeToolName(name string) string {
	if normalized, ok := piToolNameMap[name]; ok {
		return normalized
	}
	return name
}

func piDecodeArguments(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return map[string]any{}
	}
	return decoded
}

func piDecodeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func piDecodeResultOutput(raw json.RawMessage) string {
	if text := piDecodeString(raw); text != "" {
		return text
	}
	var items []piContentItem
	if err := json.Unmarshal(raw, &items); err == nil {
		texts := make([]string, 0, len(items))
		for _, item := range items {
			if item.Type == piContentTypeText && item.Text != "" {
				texts = append(texts, item.Text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	// Fall through: serialize unknown structure as JSON.
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		if encoded, err := json.Marshal(decoded); err == nil {
			return string(encoded)
		}
	}
	return string(raw)
}

func piResultStatus(isError bool) string {
	if isError {
		return piToolResultStatusErr
	}
	return piToolResultStatusOK
}

func piTimestampJSON(ts string) json.RawMessage {
	if ts == "" {
		return nil
	}
	b, err := json.Marshal(ts)
	if err != nil {
		return nil
	}
	return b
}
