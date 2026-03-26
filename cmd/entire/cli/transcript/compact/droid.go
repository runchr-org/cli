package compact

import "encoding/json"

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
