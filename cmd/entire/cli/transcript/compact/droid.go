package compact

import (
	"bufio"
	"bytes"
	"encoding/json"

	"github.com/entireio/cli/cmd/entire/cli/transcript"
)

// isDroidFormat checks whether JSONL content uses Factory AI Droid's envelope
// format. It scans lines looking for one with a recognizable "type" field — if
// the first such line has type "message" with a nested "message" object, it's
// Droid format. Unrecognized types (session_start, session_event) are skipped.
func isDroidFormat(content []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type    string           `json:"type"`
			Message *json.RawMessage `json:"message"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		if probe.Type == "message" && probe.Message != nil {
			return true
		}
		// If we hit a known Claude Code/Cursor type, it's not Droid.
		if userAliases[probe.Type] || probe.Type == transcript.TypeAssistant || droppedTypes[probe.Type] {
			return false
		}
	}
	return false
}

// compactDroid converts Factory AI Droid JSONL transcripts into the compact
// format. Droid uses the same Anthropic Messages API structure as Claude Code
// and Cursor, but wraps each message in an envelope that must be unwrapped first.
func compactDroid(content []byte, opts Options) ([]byte, error) {
	return compactJSONLWith(content, opts, unwrapDroidEnvelope)
}

// unwrapDroidEnvelope handles Factory AI Droid's envelope format where the
// actual message is nested:
//
//	{"type":"message","message":{"role":"user","content":...}}
//
// It promotes inner fields (role, content) to the top level and carries over
// outer fields (timestamp, id) so the shared converters see a flat structure.
// Returns raw unchanged if the line is not a Droid envelope.
func unwrapDroidEnvelope(raw map[string]json.RawMessage) map[string]json.RawMessage {
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
	if !userAliases[innerRole] && innerRole != transcript.TypeAssistant {
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
