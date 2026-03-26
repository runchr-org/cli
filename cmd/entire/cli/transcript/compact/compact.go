// Package compact converts full.jsonl transcripts into a normalized,
// compact transcript.jsonl format. Each agent format (Claude Code JSONL,
// OpenCode JSON, Gemini, Factory AI Droid) has its own converter file;
// this file holds the shared entry point and helper utilities.
package compact

import (
	"bytes"
	"encoding/json"
)

// droppedTypes are entry types that carry no parser-relevant data.
var droppedTypes = map[string]bool{
	"progress":              true,
	"file-history-snapshot": true,
	"queue-operation":       true,
	"system":                true,
}

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

// Compact converts a full.jsonl transcript into the transcript.jsonl format.
//
// The output format puts version, agent, and cli_version on every line,
// flattens the message wrapper, and splits user tool results into separate entries:
//
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"user","ts":"...","content":"..."}
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"user_tool_result","ts":"...","tool_use_id":"...","result":{...}}
//	{"v":1,"agent":"claude-code","cli_version":"0.42.0","type":"assistant","ts":"...","id":"msg_xxx","content":[...]}
func Compact(content []byte, opts Options) ([]byte, error) {
	truncated := sliceFromLine(content, opts.StartLine)
	if truncated == nil {
		truncated = []byte{}
	}

	if isOpenCodeFormat(truncated) {
		return compactOpenCode(truncated, opts)
	}

	if isGeminiFormat(truncated) {
		return compactGemini(truncated, opts)
	}

	return compactJSONL(truncated, opts)
}

// sliceFromLine returns the content starting from line number startLine (0-indexed).
// This is used to extract only the checkpoint-specific portion of a cumulative transcript.
// Returns empty slice if startLine exceeds the number of lines.
func sliceFromLine(content []byte, startLine int) []byte {
	if len(content) == 0 || startLine <= 0 {
		return content
	}

	lineCount := 0
	offset := 0
	for i, b := range content {
		if b == '\n' {
			lineCount++
			if lineCount == startLine {
				offset = i + 1
				break
			}
		}
	}

	if lineCount < startLine {
		return nil
	}

	if offset >= len(content) {
		return nil
	}

	return content[offset:]
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
