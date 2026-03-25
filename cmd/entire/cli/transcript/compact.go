package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

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

// compactMeta holds pre-computed JSON fragments for fields that are identical
// on every output line, avoiding repeated marshaling.
type compactMeta struct {
	v          json.RawMessage
	agent      json.RawMessage
	cliVersion json.RawMessage
}

func newCompactMeta(opts CompactOptions) compactMeta {
	return compactMeta{
		v:          mustMarshal(1),
		agent:      mustMarshal(opts.Agent),
		cliVersion: mustMarshal(opts.CLIVersion),
	}
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

	// Detect OpenCode format: a single JSON object with "info" and "messages" keys.
	if isOpenCodeFormat(truncated) {
		return compactOpenCode(truncated, opts)
	}

	meta := newCompactMeta(opts)
	reader := bufio.NewReader(bytes.NewReader(truncated))
	var result []byte

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}

		if len(bytes.TrimSpace(lineBytes)) > 0 {
			outputLines := convertLine(lineBytes, meta)
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
	TypeUser: true,
	"human":  true,
}

// assistantAliases maps transcript type/role values to the canonical "assistant" kind.
var assistantAliases = map[string]bool{
	TypeAssistant: true,
	"gemini":      true,
}

// normalizeKind returns the canonical entry kind ("user" or "assistant") for a
// transcript line. It checks the "type" field, then falls back to "role".
// Returns "" for unrecognised or dropped entries.
func normalizeKind(raw map[string]json.RawMessage) string {
	kind := unquote(raw["type"])
	if kind == "" {
		kind = unquote(raw["role"])
	}

	if droppedTypes[kind] {
		return ""
	}
	if userAliases[kind] {
		return TypeUser
	}
	if assistantAliases[kind] {
		return TypeAssistant
	}
	return ""
}

// unwrapEnvelope handles envelope formats where the actual message is nested.
// Factory AI Droid uses {"type":"message","message":{"role":"user","content":...}}.
// If the line is an envelope, it promotes inner fields (role, content) to the top
// level and carries over outer fields (timestamp, id) so converters see a flat structure.
// Otherwise it returns raw unchanged.
func unwrapEnvelope(raw map[string]json.RawMessage) map[string]json.RawMessage {
	if unquote(raw["type"]) != "message" {
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

	innerRole := unquote(inner["role"])
	if !userAliases[innerRole] && !assistantAliases[innerRole] {
		return raw
	}

	// Merge outer → inner: outer timestamp/id as defaults, inner fields override.
	merged := make(map[string]json.RawMessage, len(inner)+3)
	if v, has := raw["timestamp"]; has {
		merged["timestamp"] = v
	}
	if v, has := raw["id"]; has {
		merged["id"] = v
	}
	for k, v := range inner {
		merged[k] = v
	}
	// Promote "role" to "type" so normalizeKind resolves it.
	if _, hasType := merged["type"]; !hasType {
		merged["type"] = inner["role"]
	}
	// Keep "message" so converters can extract nested content.
	merged["message"] = msgRaw

	return merged
}

// convertLine converts a single full.jsonl line into zero or more transcript.jsonl lines.
func convertLine(lineBytes []byte, meta compactMeta) [][]byte {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lineBytes, &raw); err != nil {
		return nil
	}

	raw = unwrapEnvelope(raw)

	switch normalizeKind(raw) {
	case TypeAssistant:
		return convertAssistant(raw, meta)
	case TypeUser:
		return convertUser(raw, meta)
	default:
		return nil
	}
}

func convertAssistant(raw map[string]json.RawMessage, meta compactMeta) [][]byte {
	var id, content json.RawMessage
	if msg := parseMessage(raw); msg != nil {
		id = msg["id"]
		if contentRaw, ok := msg["content"]; ok {
			content = stripAssistantContent(contentRaw)
		}
	}

	b := marshalOrdered(
		"v", meta.v,
		"agent", meta.agent,
		"cli_version", meta.cliVersion,
		"type", mustMarshal(TypeAssistant),
		"ts", raw["timestamp"],
		"id", id,
		"content", content,
	)
	if b == nil {
		return nil
	}
	return [][]byte{b}
}

func convertUser(raw map[string]json.RawMessage, meta compactMeta) [][]byte {
	var lines [][]byte
	ts := raw["timestamp"]

	var textContent string
	var toolResults []toolResultEntry

	if msg := parseMessage(raw); msg != nil {
		if contentRaw, ok := msg["content"]; ok {
			textContent, toolResults = extractUserContent(contentRaw)
		}
	}

	b := marshalOrdered(
		"v", meta.v,
		"agent", meta.agent,
		"cli_version", meta.cliVersion,
		"type", mustMarshal(TypeUser),
		"ts", ts,
		"content", mustMarshal(textContent),
	)
	if b != nil {
		lines = append(lines, b)
	}

	// full.jsonl has a single toolUseResult per user entry, not one per tool_use_id.
	// When there are multiple tool_result blocks, each user_tool_result line gets the
	// same minimized result — a known limitation of the source format.
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
			"v", meta.v,
			"agent", meta.agent,
			"cli_version", meta.cliVersion,
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

// parseMessage extracts and parses the "message" field from a transcript line.
func parseMessage(raw map[string]json.RawMessage) map[string]json.RawMessage {
	msgRaw, ok := raw["message"]
	if ok {
		var msg map[string]json.RawMessage
		if json.Unmarshal(msgRaw, &msg) == nil {
			return msg
		}
	}

	// Native Gemini transcript entries store id/content at top-level
	// rather than inside a nested "message" object.
	msg := make(map[string]json.RawMessage, 2)
	if v, has := raw["id"]; has {
		msg["id"] = v
	}
	if v, has := raw["content"]; has {
		msg["content"] = v
	}
	if len(msg) == 0 {
		return nil
	}
	return msg
}

// extractUserContent separates user message content into text and tool_result entries.
// IDE context tags (e.g. <user_query>, <ide_opened_file>) are stripped from user text.
func extractUserContent(contentRaw json.RawMessage) (string, []toolResultEntry) {
	var str string
	if json.Unmarshal(contentRaw, &str) == nil {
		return textutil.StripIDEContextTags(str), nil
	}

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

		if blockType == ContentTypeText {
			stripped := textutil.StripIDEContextTags(unquote(block[ContentTypeText]))
			if stripped != "" {
				texts = append(texts, stripped)
			}
		}
	}

	return strings.Join(texts, "\n\n"), toolResults
}

func stripAssistantContent(contentRaw json.RawMessage) json.RawMessage {
	var str string
	if json.Unmarshal(contentRaw, &str) == nil {
		return contentRaw
	}

	var blocks []map[string]json.RawMessage
	if json.Unmarshal(contentRaw, &blocks) != nil {
		return contentRaw
	}

	result := make([]map[string]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		blockType := unquote(block["type"])

		if blockType == "thinking" || blockType == "redacted_thinking" {
			continue
		}

		if blockType == ContentTypeToolUse {
			stripped := make(map[string]json.RawMessage)
			copyField(stripped, block, "type")
			copyField(stripped, block, "id")
			copyField(stripped, block, "name")
			copyField(stripped, block, "input")
			result = append(result, stripped)
			continue
		}

		result = append(result, block)
	}

	b, err := json.Marshal(result)
	if err != nil {
		return contentRaw
	}
	return b
}

// minimizeToolUseResult keeps only fields needed for replaying tool calls
// (type, file metadata, error, answers). Output text and other verbose
// fields are dropped to reduce transcript size.
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

// --- OpenCode format support ---
//
// OpenCode transcripts are a single JSON object (not JSONL):
//
//	{"info":{...},"messages":[{"info":{"role":"user","time":{...}},"parts":[...]}, ...]}
//
// Parts use "type" values: "text", "tool", "step-start", "step-finish".
// Tool parts store the tool name in "tool" (string) and call details in "state".

// isOpenCodeFormat checks whether content is a single JSON object with the
// OpenCode session shape (top-level "info" and "messages" keys).
func isOpenCodeFormat(content []byte) bool {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	// Quick structural check: unmarshal just enough to detect the keys.
	var probe struct {
		Info     *json.RawMessage `json:"info"`
		Messages *json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(trimmed, &probe) != nil {
		return false
	}
	return probe.Info != nil && probe.Messages != nil
}

// openCodeMessage mirrors the OpenCode message structure for unmarshaling.
type openCodeMessage struct {
	Info  openCodeMessageInfo          `json:"info"`
	Parts []map[string]json.RawMessage `json:"parts"`
}

type openCodeMessageInfo struct {
	ID   string          `json:"id"`
	Role string          `json:"role"`
	Time openCodeMsgTime `json:"time"`
}

type openCodeMsgTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

// compactOpenCode converts a full OpenCode session JSON into transcript lines.
func compactOpenCode(content []byte, opts CompactOptions) ([]byte, error) {
	var session struct {
		Messages []openCodeMessage `json:"messages"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(content), &session); err != nil {
		return nil, fmt.Errorf("parsing opencode session: %w", err)
	}

	meta := newCompactMeta(opts)
	var result []byte

	for _, msg := range session.Messages {
		ts := msToTimestamp(msg.Info.Time.Created)

		switch msg.Info.Role {
		case TypeUser:
			lines := convertOpenCodeUser(msg, ts, meta)
			for _, l := range lines {
				result = append(result, l...)
				result = append(result, '\n')
			}
		case TypeAssistant:
			lines := convertOpenCodeAssistant(msg, ts, meta)
			for _, l := range lines {
				result = append(result, l...)
				result = append(result, '\n')
			}
		}
	}

	return result, nil
}

func convertOpenCodeUser(msg openCodeMessage, ts json.RawMessage, meta compactMeta) [][]byte {
	var texts []string
	for _, part := range msg.Parts {
		if unquote(part["type"]) == ContentTypeText {
			text := textutil.StripIDEContextTags(unquote(part[ContentTypeText]))
			if text != "" {
				texts = append(texts, text)
			}
		}
	}

	b := marshalOrdered(
		"v", meta.v,
		"agent", meta.agent,
		"cli_version", meta.cliVersion,
		"type", mustMarshal(TypeUser),
		"ts", ts,
		"content", mustMarshal(strings.Join(texts, "\n\n")),
	)
	if b == nil {
		return nil
	}
	return [][]byte{b}
}

func convertOpenCodeAssistant(msg openCodeMessage, ts json.RawMessage, meta compactMeta) [][]byte {
	content := make([]map[string]json.RawMessage, 0, len(msg.Parts))

	for _, part := range msg.Parts {
		partType := unquote(part["type"])

		switch partType {
		case ContentTypeText:
			content = append(content, map[string]json.RawMessage{
				"type": mustMarshal(ContentTypeText),
				"text": part[ContentTypeText],
			})
		case "tool":
			toolBlock := make(map[string]json.RawMessage)
			toolBlock["type"] = mustMarshal(ContentTypeToolUse)
			if callID := part["callID"]; callID != nil {
				toolBlock["id"] = callID
			}
			// "tool" field is the tool name (string).
			if toolName := part["tool"]; toolName != nil {
				toolBlock["name"] = toolName
			}
			// Extract input from state.input if available.
			if stateRaw := part["state"]; stateRaw != nil {
				var state map[string]json.RawMessage
				if json.Unmarshal(stateRaw, &state) == nil {
					if inp := state["input"]; inp != nil {
						toolBlock["input"] = inp
					}
				}
			}
			content = append(content, toolBlock)
			// step-start, step-finish carry no transcript-relevant data.
		}
	}

	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil
	}

	b := marshalOrdered(
		"v", meta.v,
		"agent", meta.agent,
		"cli_version", meta.cliVersion,
		"type", mustMarshal(TypeAssistant),
		"ts", ts,
		"id", mustMarshal(msg.Info.ID),
		"content", json.RawMessage(contentJSON),
	)
	if b == nil {
		return nil
	}
	return [][]byte{b}
}

// msToTimestamp converts a Unix millisecond timestamp to an RFC3339 JSON string.
func msToTimestamp(ms int64) json.RawMessage {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return mustMarshal(t.Format(time.RFC3339Nano))
}
