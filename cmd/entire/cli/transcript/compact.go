package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/textutil"
)

// droppedTypes are entry types that carry no parser-relevant data.
var droppedTypes = map[string]bool{
	"progress":              true,
	"file-history-snapshot": true,
	"queue-operation":       true,
	"system":                true,
}

// CompactOptions provides metadata fields written to every output line.
type CompactOptions struct {
	Agent      string // e.g. "claude-code"
	CLIVersion string // e.g. "0.42.0"
	StartLine  int    // checkpoint_transcript_start (0 = no truncation)
}

// Compact converts a full.jsonl transcript into the transcript.jsonl format.
//
// The output format puts version, agent, and cli_version on every line,
// flattens the message wrapper, and splits user tool results into separate entries:
//
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"user","ts":"...","content":"..."}
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"user_tool_result","ts":"...","tool_use_id":"...","result":{...}}
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"assistant","ts":"...","id":"msg_xxx","content":[...]}
func Compact(content []byte, opts CompactOptions) ([]byte, error) {
	truncated := SliceFromLine(content, opts.StartLine)
	if truncated == nil {
		truncated = []byte{}
	}

	reader := bufio.NewReader(bytes.NewReader(truncated))
	var result []byte

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}

		if len(bytes.TrimSpace(lineBytes)) > 0 {
			outputLines := convertLine(lineBytes, opts)
			for _, ol := range outputLines {
				result = append(result, ol...)
				result = append(result, '\n')
			}
		}

		if err == io.EOF {
			break
		}
	}

	return result, nil
}

// userAliases maps transcript type/role values to the canonical "user" kind.
var userAliases = map[string]bool{
	"user":  true,
	"human": true,
}

// assistantAliases maps transcript type/role values to the canonical "assistant" kind.
var assistantAliases = map[string]bool{
	"assistant": true,
	"gemini":    true,
}

// normalizeKind returns the canonical entry kind ("user" or "assistant") for a
// transcript line. It checks the "type" field, then falls back to "role".
// Returns "" for unrecognised or dropped entries.
func normalizeKind(raw map[string]json.RawMessage) string {
	// Try "type" first, then "role".
	kind := unquote(raw["type"])
	if kind == "" {
		kind = unquote(raw["role"])
	}

	if droppedTypes[kind] {
		return ""
	}
	if userAliases[kind] {
		return "user"
	}
	if assistantAliases[kind] {
		return "assistant"
	}
	return ""
}

// unwrapEnvelope handles envelope formats where the actual message is nested.
// Factory AI Droid uses {"type":"message","message":{"role":"user","content":...}}.
// If the line is an envelope, it returns the inner message merged with outer fields
// (timestamp, id). Otherwise it returns raw unchanged.
func unwrapEnvelope(raw map[string]json.RawMessage) map[string]json.RawMessage {
	kind := unquote(raw["type"])
	if kind != "message" {
		return raw
	}

	msgRaw, ok := raw["message"]
	if !ok {
		return raw
	}

	var inner map[string]json.RawMessage
	if json.Unmarshal(msgRaw, &inner) != nil {
		return raw
	}

	// The inner message should have a "role" field — check it resolves to a known kind.
	innerRole := unquote(inner["role"])
	if !userAliases[innerRole] && !assistantAliases[innerRole] {
		return raw
	}

	// Build a merged view: inner fields take precedence, but carry over outer
	// fields (timestamp, id) that the inner message may lack.
	merged := make(map[string]json.RawMessage, len(inner)+2)
	// Copy outer timestamp if present.
	if v, has := raw["timestamp"]; has {
		merged["timestamp"] = v
	}
	// Copy outer id as fallback.
	if v, has := raw["id"]; has {
		merged["id"] = v
	}
	// Copy all inner fields (overrides outer if keys collide).
	for k, v := range inner {
		merged[k] = v
	}
	// Promote "role" to "type" so normalizeKind resolves it.
	if _, hasType := merged["type"]; !hasType {
		merged["type"] = inner["role"]
	}

	// Re-wrap content into a "message" field so converters find it.
	// The inner message IS the message wrapper for converters.
	merged["message"] = msgRaw

	return merged
}

// convertLine converts a single full.jsonl line into zero or more transcript.jsonl lines.
// A user entry with tool_result blocks produces multiple output lines.
func convertLine(lineBytes []byte, opts CompactOptions) [][]byte {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lineBytes, &raw); err != nil {
		return nil
	}

	// Unwrap envelope formats (e.g. Factory AI Droid's type:"message" wrapper).
	raw = unwrapEnvelope(raw)

	switch normalizeKind(raw) {
	case "assistant":
		return convertAssistant(raw, opts)
	case "user":
		return convertUser(raw, opts)
	default:
		return nil // drop unknown types in the new format
	}
}

func convertAssistant(raw map[string]json.RawMessage, opts CompactOptions) [][]byte {
	// Hoist id and content from message to top level
	var id, content json.RawMessage
	if msgRaw, ok := raw["message"]; ok {
		var msg map[string]json.RawMessage
		if json.Unmarshal(msgRaw, &msg) == nil {
			id = msg["id"]
			if contentRaw, ok := msg["content"]; ok {
				content = stripAssistantContent(contentRaw)
			}
		}
	}

	b := marshalOrdered(
		"v", mustMarshal(1),
		"agent", mustMarshal(opts.Agent),
		"cli_version", mustMarshal(opts.CLIVersion),
		"type", mustMarshal("assistant"),
		"ts", raw["timestamp"],
		"id", id,
		"content", content,
	)
	if b == nil {
		return nil
	}
	return [][]byte{b}
}

func convertUser(raw map[string]json.RawMessage, opts CompactOptions) [][]byte {
	var lines [][]byte
	ts := raw["timestamp"]

	// Parse message content to separate text from tool_results
	var textContent string
	var toolResults []toolResultEntry

	if msgRaw, ok := raw["message"]; ok {
		var msg map[string]json.RawMessage
		if json.Unmarshal(msgRaw, &msg) == nil {
			if contentRaw, ok := msg["content"]; ok {
				textContent, toolResults = extractUserContent(contentRaw)
			}
		}
	}

	// Emit the user text entry
	b := marshalOrdered(
		"v", mustMarshal(1),
		"agent", mustMarshal(opts.Agent),
		"cli_version", mustMarshal(opts.CLIVersion),
		"type", mustMarshal("user"),
		"ts", ts,
		"content", mustMarshal(textContent),
	)
	if b != nil {
		lines = append(lines, b)
	}

	// Emit separate user_tool_result entries.
	//
	// Note: full.jsonl has a single toolUseResult per user entry, not one per tool_use_id.
	// When there are multiple tool_result blocks, each user_tool_result line gets the same
	// minimized result. This is a known limitation of the source format — per-tool-use-id
	// result data is not available.
	var minimizedResult json.RawMessage
	if turRaw, ok := raw["toolUseResult"]; ok {
		minimizedResult = minimizeToolUseResult(turRaw)
	}

	for _, tr := range toolResults {
		result := minimizedResult
		if result == nil {
			result = mustMarshal(map[string]interface{}{})
		}

		b := marshalOrdered(
			"v", mustMarshal(1),
			"agent", mustMarshal(opts.Agent),
			"cli_version", mustMarshal(opts.CLIVersion),
			"type", mustMarshal("user_tool_result"),
			"ts", ts,
			"tool_use_id", mustMarshal(tr.toolUseID),
			"result", result,
		)
		if b != nil {
			lines = append(lines, b)
		}
	}

	return lines
}

type toolResultEntry struct {
	toolUseID string
}

// extractUserContent separates user message content into text and tool_result entries.
// IDE context tags (e.g. <user_query>, <ide_opened_file>) are stripped from user text.
func extractUserContent(contentRaw json.RawMessage) (string, []toolResultEntry) {
	// String content
	var str string
	if json.Unmarshal(contentRaw, &str) == nil {
		return textutil.StripIDEContextTags(str), nil
	}

	// Array content
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(contentRaw, &blocks) != nil {
		return "", nil
	}

	var texts []string
	var toolResults []toolResultEntry

	for _, block := range blocks {
		blockType := unquote(block["type"])

		if blockType == "tool_result" {
			toolResults = append(toolResults, toolResultEntry{
				toolUseID: unquote(block["tool_use_id"]),
			})
			continue
		}

		if blockType == "text" {
			stripped := textutil.StripIDEContextTags(unquote(block["text"]))
			if stripped != "" {
				texts = append(texts, stripped)
			}
		}
	}

	text := ""
	if len(texts) > 0 {
		text = texts[0]
		var sb strings.Builder
		for i := 1; i < len(texts); i++ {
			sb.WriteString("\n\n" + texts[i])
		}
		text += sb.String()
	}

	return text, toolResults
}

func stripAssistantContent(contentRaw json.RawMessage) json.RawMessage {
	// String content — keep as-is
	var str string
	if json.Unmarshal(contentRaw, &str) == nil {
		return contentRaw
	}

	// Array of content blocks
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(contentRaw, &blocks) != nil {
		return contentRaw
	}

	var result []map[string]json.RawMessage
	for _, block := range blocks {
		blockType := unquote(block["type"])

		// Drop thinking blocks
		if blockType == "thinking" || blockType == "redacted_thinking" {
			continue
		}

		// Strip tool_use: keep type, id, name, input — drop caller
		if blockType == "tool_use" {
			stripped := make(map[string]json.RawMessage)
			copyField(stripped, block, "type")
			copyField(stripped, block, "id")
			copyField(stripped, block, "name")
			copyField(stripped, block, "input")
			result = append(result, stripped)
			continue
		}

		// Other block types (text, image) — keep as-is
		result = append(result, block)
	}

	b, err := json.Marshal(result)
	if err != nil {
		return contentRaw
	}
	return b
}

// minimizeToolUseResult strips a toolUseResult to only the fields the API needs.
func minimizeToolUseResult(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw
	}

	return marshalOrdered(
		"type", obj["type"],
		"file", obj["file"],
		"error", obj["error"],
		"answers", obj["answers"],
	)
}

// marshalOrdered produces a JSON object with keys in the given order.
// Pairs with nil values are omitted.
func marshalOrdered(pairs ...interface{}) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for i := 0; i < len(pairs)-1; i += 2 {
		key := pairs[i].(string)
		val, _ := pairs[i+1].(json.RawMessage)
		if val == nil {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(key)
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(val)
		first = false
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func copyField(dst, src map[string]json.RawMessage, key string) {
	if v, ok := src[key]; ok {
		dst[key] = v
	}
}

func unquote(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
