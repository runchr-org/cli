package compact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/textutil"
	"github.com/entireio/cli/cmd/entire/cli/transcript"
)

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
func compactOpenCode(content []byte, opts Options) ([]byte, error) {
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
		case transcript.TypeUser:
			lines := convertOpenCodeUser(msg, ts, meta)
			for _, l := range lines {
				result = append(result, l...)
				result = append(result, '\n')
			}
		case transcript.TypeAssistant:
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
	content := make([]map[string]json.RawMessage, 0, len(msg.Parts))

	for _, part := range msg.Parts {
		if unquote(part["type"]) != transcript.ContentTypeText {
			continue
		}
		text := textutil.StripIDEContextTags(unquote(part[transcript.ContentTypeText]))
		if text == "" {
			continue
		}
		block := map[string]json.RawMessage{
			"text": mustMarshal(text),
		}
		if id := part["id"]; id != nil {
			block["id"] = id
		}
		content = append(content, block)
	}

	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil
	}

	b := marshalOrdered(
		"v", meta.v,
		"agent", meta.agent,
		"cli_version", meta.cliVersion,
		"type", mustMarshal(transcript.TypeUser),
		"ts", ts,
		"content", json.RawMessage(contentJSON),
	)
	return [][]byte{b}
}

func convertOpenCodeAssistant(msg openCodeMessage, ts json.RawMessage, meta compactMeta) [][]byte {
	content := make([]map[string]json.RawMessage, 0, len(msg.Parts))

	for _, part := range msg.Parts {
		partType := unquote(part["type"])

		switch partType {
		case transcript.ContentTypeText:
			content = append(content, map[string]json.RawMessage{
				"type": mustMarshal(transcript.ContentTypeText),
				"text": part[transcript.ContentTypeText],
			})
		case "tool":
			toolBlock := make(map[string]json.RawMessage)
			toolBlock["type"] = mustMarshal(transcript.ContentTypeToolUse)
			if callID := part["callID"]; callID != nil {
				toolBlock["id"] = callID
			}
			// "tool" field is the tool name (string).
			if toolName := part["tool"]; toolName != nil {
				toolBlock["name"] = toolName
			}
			// Extract input and result from state if available.
			if stateRaw := part["state"]; stateRaw != nil {
				var state map[string]json.RawMessage
				if json.Unmarshal(stateRaw, &state) == nil {
					if inp := state["input"]; inp != nil {
						toolBlock["input"] = inp
					}
					toolBlock["result"] = openCodeToolResult(state)
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
		"type", mustMarshal(transcript.TypeAssistant),
		"ts", ts,
		"id", mustMarshal(msg.Info.ID),
		"content", json.RawMessage(contentJSON),
	)
	return [][]byte{b}
}

// openCodeToolResult builds the compact {"output":"...","status":"done"|"error"}
// object from an OpenCode tool state map.
func openCodeToolResult(state map[string]json.RawMessage) json.RawMessage {
	status := "done"
	if s := unquote(state["status"]); s != "" && s != "completed" {
		status = "error"
	}
	result := map[string]string{
		"output": unquote(state["output"]),
		"status": status,
	}
	return mustMarshal(result)
}

// msToTimestamp converts a Unix millisecond timestamp to an RFC3339 JSON string.
func msToTimestamp(ms int64) json.RawMessage {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return mustMarshal(t.Format(time.RFC3339Nano))
}
