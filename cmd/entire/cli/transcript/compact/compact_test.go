package compact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// --- Edge case tests ---

func TestCompact_EmptyInput(t *testing.T) {
	t.Parallel()

	result, err := Compact([]byte{}, Options{Agent: "claude-code", CLIVersion: "0.5.1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, nil)
}

func TestCompact_StartLineBeyondEnd(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"user","uuid":"u1","timestamp":"t1","message":{"content":"hello"}}
`)
	opts := Options{Agent: "claude-code", CLIVersion: "0.5.1", StartLine: 100}

	result, err := Compact(input, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, nil)
}

func TestCompact_MalformedLinesSkipped(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"user","uuid":"u1","timestamp":"t1","message":{"content":"hello"}}
not valid json at all
{"type":"assistant","timestamp":"t2","requestId":"r1","message":{"id":"m1","content":"hi"}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"t1","content":"hello"}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"t2","id":"m1","content":"hi"}`,
	}

	result, err := Compact(input, Options{Agent: "claude-code", CLIVersion: "0.5.1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_OnlyDroppedTypes(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"progress","message":{"content":"..."}}
{"type":"file-history-snapshot","files":[]}
{"type":"queue-operation","op":"enqueue"}
{"type":"system","message":{"content":"reminder"}}
`)

	result, err := Compact(input, Options{Agent: "claude-code", CLIVersion: "0.5.1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, nil)
}

// --- Helpers ---

func nonEmptyLines(data []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// assertJSONLines compares actual output lines against expected JSON strings,
// using semantic JSON equality (order-independent for object keys).
func assertJSONLines(t *testing.T, actual []byte, expected []string) {
	t.Helper()

	actualLines := nonEmptyLines(actual)

	if len(expected) == 0 && len(actualLines) == 0 {
		return
	}

	if len(actualLines) != len(expected) {
		t.Fatalf("line count mismatch: got %d, want %d\nactual:\n%s", len(actualLines), len(expected), string(actual))
	}

	for i := range expected {
		var got, want interface{}
		if err := json.Unmarshal([]byte(actualLines[i]), &got); err != nil {
			t.Fatalf("line %d: failed to parse actual JSON: %v\nline: %s", i, err, actualLines[i])
		}
		if err := json.Unmarshal([]byte(expected[i]), &want); err != nil {
			t.Fatalf("line %d: failed to parse expected JSON: %v\nline: %s", i, err, expected[i])
		}
		if !reflect.DeepEqual(got, want) {
			prettyGot, _ := json.MarshalIndent(got, "", "  ")   //nolint:errcheck,errchkjson // test helper, marshal of interface{} is best-effort
			prettyWant, _ := json.MarshalIndent(want, "", "  ") //nolint:errcheck,errchkjson // test helper, marshal of interface{} is best-effort
			t.Errorf("line %d mismatch:\ngot:\n%s\nwant:\n%s", i, prettyGot, prettyWant)
		}
	}
}
