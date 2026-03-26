// Package compact converts full.jsonl transcripts into a normalized,
// compact transcript.jsonl format. Only shared formatting is contained here.
package compact

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/textutil"
	"github.com/entireio/cli/cmd/entire/cli/transcript"
)

// Options provides metadata fields written to every output line.
type Options struct {
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

func newCompactMeta(opts Options) compactMeta {
	return compactMeta{
		v:          mustMarshal(1),
		agent:      mustMarshal(opts.Agent),
		cliVersion: mustMarshal(opts.CLIVersion),
	}
}

// Compact converts a full.jsonl transcript into the condensed transcript.jsonl format.
//
// The output format puts version, agent, and cli_version on every line,
// flattens the message wrapper, and splits user tool results into separate entries:
//
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"user","ts":"...","content":"..."}
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"user_tool_result","ts":"...","tool_use_id":"...","result":{...}}
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"assistant","ts":"...","id":"msg_xxx","content":[...]}
func Compact(content []byte, opts Options) ([]byte, error) {
	truncated := transcript.SliceFromLine(content, opts.StartLine)
	if truncated == nil {
		truncated = []byte{}
	}

	if isOpenCodeFormat(truncated) {
		return compactOpenCode(truncated, opts)
	}

	if isGeminiFormat(truncated) {
		return compactGemini(truncated, opts)
	}

	if isDroidFormat(truncated) {
		return compactDroid(truncated, opts)
	}

	return compactJSONL(truncated, opts)
}

// droppedTypes are JSONL entry types that carry no parser-relevant data.
var droppedTypes = map[string]bool{
	"progress":              true,
	"file-history-snapshot": true,
	"queue-operation":       true,
	"system":                true,
}

// userAliases maps JSONL type/role values to the canonical "user" kind.
// Covers Claude Code ("user", "human") and Cursor ("user" via "role" field).
var userAliases = map[string]bool{
	transcript.TypeUser: true,
	"human":             true,
}

// normalizeKind returns the canonical entry kind ("user" or "assistant") for a
// JSONL transcript line. It checks the "type" field, then falls back to "role".
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
		return transcript.TypeUser
	}
	if kind == transcript.TypeAssistant {
		return transcript.TypeAssistant
	}
	return ""
}

// linePreprocessor transforms a parsed JSONL line before conversion.
// Used by agent-specific converters to normalize their format (e.g., Droid
// envelope unwrapping) before the shared pipeline processes it.
type linePreprocessor func(map[string]json.RawMessage) map[string]json.RawMessage

// compactJSONL converts JSONL transcripts (Claude Code, Cursor) into the
// compact format, one line at a time.
func compactJSONL(content []byte, opts Options) ([]byte, error) {
	return compactJSONLWith(content, opts, nil)
}

// compactJSONLWith converts JSONL transcripts into the compact format,
// applying an optional per-line preprocessor before conversion.
func compactJSONLWith(content []byte, opts Options, preprocess linePreprocessor) ([]byte, error) {
	meta := newCompactMeta(opts)
	reader := bufio.NewReader(bytes.NewReader(content))
	var result []byte

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading JSONL line: %w", err)
		}

		if len(bytes.TrimSpace(lineBytes)) > 0 {
			outputLines := convertLine(lineBytes, meta, preprocess)
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

// convertLine converts a single JSONL line into zero or more compact transcript lines.
func convertLine(lineBytes []byte, meta compactMeta, preprocess linePreprocessor) [][]byte {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lineBytes, &raw); err != nil {
		return nil
	}

	if preprocess != nil {
		raw = preprocess(raw)
	}

	switch normalizeKind(raw) {
	case transcript.TypeAssistant:
		return convertAssistant(raw, meta)
	case transcript.TypeUser:
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
		"type", mustMarshal(transcript.TypeAssistant),
		"ts", raw["timestamp"],
		"id", id,
		"content", content,
	)
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
		"type", mustMarshal(transcript.TypeUser),
		"ts", ts,
		"content", mustMarshal(textContent),
	)
	lines = append(lines, b)

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
		lines = append(lines, b)
	}

	return lines
}

type toolResultEntry struct {
	toolUseID string
}

// parseMessage extracts and parses the "message" field from a JSONL transcript
// line. All JSONL agents nest content inside a "message" object.
func parseMessage(raw map[string]json.RawMessage) map[string]json.RawMessage {
	msgRaw, ok := raw["message"]
	if !ok {
		return nil
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(msgRaw, &msg) == nil {
		return msg
	}
	return nil
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

		if blockType == transcript.ContentTypeText {
			stripped := textutil.StripIDEContextTags(unquote(block[transcript.ContentTypeText]))
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

		if blockType == transcript.ContentTypeToolUse {
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
		key := pairs[i].(string)               //nolint:errcheck,forcetypeassert // contract: keys are always strings
		val, _ := pairs[i+1].(json.RawMessage) //nolint:errcheck // nil val is handled below
		if val == nil {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(key) //nolint:errcheck,errchkjson // string keys never fail
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(val)
		first = false
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// mustMarshal marshals v to JSON, panicking on error (which should never
// happen for the primitive types we pass).
func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v) //nolint:errcheck,errchkjson // only used with primitive types that never fail
	return b
}

// copyField copies a single key from src to dst if it exists.
func copyField(dst, src map[string]json.RawMessage, key string) {
	if v, ok := src[key]; ok {
		dst[key] = v
	}
}

// unquote JSON-decodes a raw message as a string. Returns "" on failure.
func unquote(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
