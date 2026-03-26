package compact

import "encoding/json"

// stubs.go provides temporary forward declarations so the package compiles
// before all agent-specific files are added. Remove this file once
// claudecode.go, opencode.go, and gemini.go are in place.

func isOpenCodeFormat(_ []byte) bool                      { return false }
func compactOpenCode(_ []byte, _ Options) ([]byte, error) { return nil, nil }
func isGeminiFormat(_ []byte) bool                        { return false }
func compactGemini(_ []byte, _ Options) ([]byte, error)   { return nil, nil }

// compactJSONL is the default JSONL converter (Claude Code, Cursor, Factory AI Droid).
// Stubbed until claudecode.go is added.
func compactJSONL(_ []byte, _ Options) ([]byte, error) { return nil, nil }

// ensureHelpersUsed prevents "unused" lint errors while stubs are in place.
// It will be removed along with stubs.go once agent-specific files are added.
var _ = ensureHelpersUsed

func ensureHelpersUsed() {
	_ = droppedTypes
	_ = newCompactMeta(Options{})
	_ = marshalOrdered("k", json.RawMessage(`"v"`))
	_ = mustMarshal(0)
	dst := map[string]json.RawMessage{}
	src := map[string]json.RawMessage{"a": json.RawMessage(`1`)}
	copyField(dst, src, "a")
	_ = unquote(json.RawMessage(`"x"`))
}
