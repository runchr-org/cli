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
// merges streaming assistant fragments with the same message ID, and inlines
// tool results into the preceding assistant's tool_use blocks:
//
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"user","ts":"...","content":"..."}
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"assistant","ts":"...","id":"msg_xxx","content":[{"type":"text","text":"..."},{"type":"tool_use","id":"...","name":"...","input":{...},"result":{"output":"...","status":"..."}}]}
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
// compact format.
func compactJSONL(content []byte, opts Options) ([]byte, error) {
	return compactJSONLWith(content, opts, nil)
}

// parsedEntry is an intermediate representation of a JSONL line used during
// the two-pass compact conversion.
type parsedEntry struct {
	kind      string // "user" or "assistant"
	ts        json.RawMessage
	id        string          // message ID (assistant only)
	content   json.RawMessage // stripped assistant content array, or nil
	userText  string          // extracted user text
	toolUseID string          // tool_use_id from user tool_result (single result per entry)
	toolOut   string          // tool output text (from toolUseResult.stdout or tool_result.content)
	isError   bool            // whether the tool result was an error
}

// compactJSONLWith converts JSONL transcripts into the compact format.
// It uses a two-pass approach:
//  1. Parse all lines into intermediate entries
//  2. Merge streaming assistant fragments (same msg ID), inline tool results
//     from user lines into preceding assistant tool_use blocks, and drop
//     tool-result-only user lines.
func compactJSONLWith(content []byte, opts Options, preprocess linePreprocessor) ([]byte, error) {
	meta := newCompactMeta(opts)

	// Pass 1: parse all lines into intermediate entries.
	entries, err := parseJSONLEntries(content, preprocess)
	if err != nil {
		return nil, err
	}

	// Pass 2: merge and emit.
	var result []byte
	for i := 0; i < len(entries); i++ {
		e := entries[i]

		switch e.kind {
		case transcript.TypeAssistant:
			// Merge consecutive assistant entries with the same message ID.
			merged := e
			for i+1 < len(entries) && entries[i+1].kind == transcript.TypeAssistant && entries[i+1].id == e.id {
				i++
				merged = mergeAssistantEntries(merged, entries[i])
			}

			// Look ahead for a user tool_result to inline.
			if i+1 < len(entries) && entries[i+1].kind == transcript.TypeUser && entries[i+1].toolUseID != "" {
				userEntry := entries[i+1]
				merged = inlineToolResult(merged, userEntry)
				i++ // consume the user tool_result entry

				// If the consumed user entry also had text content, emit it
				// as a separate user line after the assistant.
				if userEntry.userText != "" {
					emitAssistant(&result, meta, merged)
					emitUser(&result, meta, userEntry)
					continue
				}
			}

			if isEmptyContentArray(merged.content) {
				continue
			}

			emitAssistant(&result, meta, merged)

		case transcript.TypeUser:
			// User entries that are purely tool results were already consumed
			// by the assistant look-ahead above. If we reach one here it was
			// not preceded by an assistant with a matching tool_use, so emit
			// it only if it has text content.
			if e.toolUseID != "" && e.userText == "" {
				continue
			}
			emitUser(&result, meta, e)
		}
	}

	return result, nil
}

func emitAssistant(result *[]byte, meta compactMeta, e parsedEntry) {
	b := marshalOrdered(
		"v", meta.v,
		"agent", meta.agent,
		"cli_version", meta.cliVersion,
		"type", mustMarshal(transcript.TypeAssistant),
		"ts", e.ts,
		"id", jsonStringOrNil(e.id),
		"content", e.content,
	)
	*result = append(*result, b...)
	*result = append(*result, '\n')
}

func emitUser(result *[]byte, meta compactMeta, e parsedEntry) {
	b := marshalOrdered(
		"v", meta.v,
		"agent", meta.agent,
		"cli_version", meta.cliVersion,
		"type", mustMarshal(transcript.TypeUser),
		"ts", e.ts,
		"content", mustMarshal(e.userText),
	)
	*result = append(*result, b...)
	*result = append(*result, '\n')
}

// parseJSONLEntries parses all JSONL lines into intermediate entries,
// filtering dropped types and malformed lines.
func parseJSONLEntries(content []byte, preprocess linePreprocessor) ([]parsedEntry, error) {
	reader := bufio.NewReader(bytes.NewReader(content))
	var entries []parsedEntry

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading JSONL line: %w", err)
		}

		if len(bytes.TrimSpace(lineBytes)) > 0 {
			if e, ok := parseLine(lineBytes, preprocess); ok {
				entries = append(entries, e)
			}
		}

		if err == io.EOF {
			break
		}
	}

	return entries, nil
}

// parseLine converts a single JSONL line into a parsedEntry.
// Returns ok=false for dropped/malformed lines.
func parseLine(lineBytes []byte, preprocess linePreprocessor) (parsedEntry, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lineBytes, &raw); err != nil {
		return parsedEntry{}, false
	}

	if preprocess != nil {
		raw = preprocess(raw)
	}

	kind := normalizeKind(raw)
	if kind == "" {
		return parsedEntry{}, false
	}

	e := parsedEntry{
		kind: kind,
		ts:   raw["timestamp"],
	}

	msg := parseMessage(raw)

	switch kind {
	case transcript.TypeAssistant:
		if msg != nil {
			e.id = unquote(msg["id"])
			if contentRaw, ok := msg["content"]; ok {
				e.content = stripAssistantContent(contentRaw)
			}
		}

	case transcript.TypeUser:
		if msg != nil {
			if contentRaw, ok := msg["content"]; ok {
				text, toolResults := extractUserContent(contentRaw)
				e.userText = text
				if len(toolResults) > 0 {
					e.toolUseID = toolResults[0].toolUseID
					e.toolOut = toolResults[0].output
					e.isError = toolResults[0].isError
				}
			}
		}
		// Also check toolUseResult.stdout which may have the output.
		if turRaw, ok := raw["toolUseResult"]; ok {
			var tur map[string]json.RawMessage
			if json.Unmarshal(turRaw, &tur) == nil {
				if stdout := unquote(tur["stdout"]); stdout != "" {
					e.toolOut = stdout
				}
			}
		}
	}

	return e, true
}

// mergeAssistantEntries combines two assistant entries with the same message ID.
// Content arrays are concatenated; the later timestamp wins.
func mergeAssistantEntries(a, b parsedEntry) parsedEntry {
	merged := a
	merged.ts = b.ts

	var aBlocks, bBlocks []json.RawMessage
	_ = json.Unmarshal(a.content, &aBlocks) //nolint:errcheck // best-effort merge
	_ = json.Unmarshal(b.content, &bBlocks) //nolint:errcheck // best-effort merge
	all := append(aBlocks, bBlocks...)      //nolint:gocritic // intentional append to new slice
	if data, err := json.Marshal(all); err == nil {
		merged.content = data
	}

	return merged
}

// inlineToolResult adds a "result" field to the last tool_use block in the
// assistant entry's content, using the output from a user tool_result entry.
func inlineToolResult(assistant, user parsedEntry) parsedEntry {
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(assistant.content, &blocks) != nil || len(blocks) == 0 {
		return assistant
	}

	// Find the tool_use block matching the user's tool_use_id.
	idx := -1
	for i := len(blocks) - 1; i >= 0; i-- {
		if unquote(blocks[i]["type"]) == transcript.ContentTypeToolUse {
			if user.toolUseID == "" || unquote(blocks[i]["id"]) == user.toolUseID {
				idx = i
				break
			}
		}
	}
	// No matching tool_use block: do not attach a result to unrelated content.
	if idx == -1 {
		return assistant
	}

	status := "success"
	if user.isError {
		status = "error"
	}

	resultObj := marshalOrdered(
		"output", mustMarshal(user.toolOut),
		"status", mustMarshal(status),
	)
	blocks[idx]["result"] = resultObj

	if data, err := json.Marshal(blocks); err == nil {
		assistant.content = data
	}

	return assistant
}

// jsonStringOrNil returns a JSON-encoded string, or nil if s is empty.
func jsonStringOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return mustMarshal(s)
}

// isEmptyContentArray returns true if raw is a JSON empty array (`[]`).
func isEmptyContentArray(raw json.RawMessage) bool {
	var arr []json.RawMessage
	return json.Unmarshal(raw, &arr) == nil && len(arr) == 0
}

type toolResultEntry struct {
	toolUseID string
	output    string
	isError   bool
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
			var isErr bool
			if raw, ok := block["is_error"]; ok {
				_ = json.Unmarshal(raw, &isErr) //nolint:errcheck // best-effort
			}
			toolResults = append(toolResults, toolResultEntry{
				toolUseID: unquote(block["tool_use_id"]),
				output:    unquote(block["content"]),
				isError:   isErr,
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
