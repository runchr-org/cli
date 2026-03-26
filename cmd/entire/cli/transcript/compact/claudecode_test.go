package compact

import (
	"os"
	"strings"
	"testing"
)

var defaultOpts = Options{
	Agent:      "claude-code",
	CLIVersion: "0.5.1",
	StartLine:  0,
}

// --- Golden output tests ---

func TestCompact_SimpleConversation(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","parentUuid":"","cwd":"/repo","message":{"content":"hello"}}
{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","requestId":"req-1","message":{"id":"msg-1","content":[{"type":"text","text":"Hi!"}]}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"2026-01-01T00:00:00Z","content":"hello"}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"2026-01-01T00:00:01Z","id":"msg-1","content":[{"type":"text","text":"Hi!"}]}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_AssistantStripping(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","requestId":"req-1","message":{"id":"msg-1","content":[{"type":"thinking","thinking":"hmm..."},{"type":"redacted_thinking","data":"secret"},{"type":"text","text":"Here's my answer."},{"type":"tool_use","id":"tu-1","name":"Bash","input":{"command":"ls"},"caller":"internal"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}]}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"2026-01-01T00:00:01Z","id":"msg-1","content":[{"type":"text","text":"Here's my answer."},{"type":"tool_use","id":"tu-1","name":"Bash","input":{"command":"ls"}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}]}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_AssistantThinkingOnly(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","requestId":"req-1","message":{"id":"msg-1","content":[{"type":"thinking","thinking":"hmm..."}]}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"2026-01-01T00:00:01Z","id":"msg-1","content":[]}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_UserWithToolResult(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:01:00Z","parentUuid":"u1","cwd":"/repo","sessionId":"sess-1","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":"file1.txt\nfile2.txt"},{"type":"text","text":"now fix the bug"}]},"toolUseResult":{"type":"text","file":{"filePath":"/repo/file1.txt","numLines":10},"output":"long output...","matchCount":2}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"2026-01-01T00:01:00Z","content":"now fix the bug"}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"2026-01-01T00:01:00Z","tool_use_id":"tu-1","result":{"type":"text","file":{"filePath":"/repo/file1.txt","numLines":10}}}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_MultipleToolResults(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":"result1"},{"type":"tool_result","tool_use_id":"tu-2","content":"result2"},{"type":"text","text":"continue"}]},"toolUseResult":{"type":"text"}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"2026-01-01T00:00:00Z","content":"continue"}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"2026-01-01T00:00:00Z","tool_use_id":"tu-1","result":{"type":"text"}}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"2026-01-01T00:00:00Z","tool_use_id":"tu-2","result":{"type":"text"}}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_UserNoText(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"user","uuid":"u1","timestamp":"t1","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":"done"}]},"toolUseResult":{"type":"text"}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"t1","content":""}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"t1","tool_use_id":"tu-1","result":{"type":"text"}}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_AssistantStringContent(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"assistant","timestamp":"t1","requestId":"r1","message":{"id":"m1","content":"just a string"}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"t1","id":"m1","content":"just a string"}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_RealFile(t *testing.T) {
	content, err := os.ReadFile("../../../../../full-example.jsonl")
	if err != nil {
		t.Skip("no full-example.jsonl found")
	}
	result, err := Compact(content, Options{
		Agent:      "claude-code",
		CLIVersion: "0.5.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("../../../../../transcript-output.jsonl", result, 0644); err != nil {
		t.Fatalf("failed to write output: %v", err)
	}
	t.Logf("wrote %d bytes", len(result))
}

// --- Truncation + filtering tests ---

// Realistic full.jsonl: lines 0-2 are duplicated prefix, lines 3-6 are new content.
var fixtureFullJSONL = strings.Join([]string{
	`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","parentUuid":"","cwd":"/repo","sessionId":"sess-1","version":"1","gitBranch":"main","message":{"content":"hello"}}`,
	`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","requestId":"req-1","message":{"id":"msg-1","content":[{"type":"thinking","thinking":"let me think..."},{"type":"text","text":"Hi there!"},{"type":"tool_use","id":"tu-1","name":"Bash","input":{"command":"ls"},"caller":"some-caller"}]}}`,
	`{"type":"progress","message":{"type":"bash","content":"running..."}}`,
	`{"type":"user","uuid":"u2","timestamp":"2026-01-01T00:01:00Z","parentUuid":"u1","cwd":"/repo","sessionId":"sess-1","version":"1","gitBranch":"main","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":"file1.txt\nfile2.txt"},{"type":"text","text":"now fix the bug"}]},"toolUseResult":{"type":"text","file":{"filePath":"/repo/file1.txt","numLines":10},"output":"file1.txt\nfile2.txt","matchCount":2}}`,
	`{"type":"assistant","timestamp":"2026-01-01T00:01:01Z","requestId":"req-2","message":{"id":"msg-2","content":[{"type":"thinking","thinking":"analyzing the bug..."},{"type":"redacted_thinking","data":"abc123"},{"type":"text","text":"I found the issue."},{"type":"tool_use","id":"tu-2","name":"Edit","input":{"file_path":"/repo/bug.go","old_string":"bad","new_string":"good"},"caller":"internal"}]}}`,
	`{"type":"file-history-snapshot","files":["/repo/bug.go"]}`,
	`{"type":"system","message":{"content":"system reminder"}}`,
}, "\n") + "\n"

func TestCompact_FullFixture_WithTruncation(t *testing.T) {
	t.Parallel()

	opts := Options{Agent: "claude-code", CLIVersion: "0.5.1", StartLine: 3}

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"2026-01-01T00:01:00Z","content":"now fix the bug"}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"2026-01-01T00:01:00Z","tool_use_id":"tu-1","result":{"type":"text","file":{"filePath":"/repo/file1.txt","numLines":10}}}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"2026-01-01T00:01:01Z","id":"msg-2","content":[{"type":"text","text":"I found the issue."},{"type":"tool_use","id":"tu-2","name":"Edit","input":{"file_path":"/repo/bug.go","old_string":"bad","new_string":"good"}}]}`,
	}

	result, err := Compact([]byte(fixtureFullJSONL), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_FullFixture_NoTruncation(t *testing.T) {
	t.Parallel()

	expected := []string{
		// Line 0: user "hello"
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"2026-01-01T00:00:00Z","content":"hello"}`,
		// Line 1: assistant (thinking stripped, caller stripped)
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"2026-01-01T00:00:01Z","id":"msg-1","content":[{"type":"text","text":"Hi there!"},{"type":"tool_use","id":"tu-1","name":"Bash","input":{"command":"ls"}}]}`,
		// Line 2: progress — dropped
		// Line 3: user with tool_result — split
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"2026-01-01T00:01:00Z","content":"now fix the bug"}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"2026-01-01T00:01:00Z","tool_use_id":"tu-1","result":{"type":"text","file":{"filePath":"/repo/file1.txt","numLines":10}}}`,
		// Line 4: assistant (thinking + redacted_thinking stripped)
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"2026-01-01T00:01:01Z","id":"msg-2","content":[{"type":"text","text":"I found the issue."},{"type":"tool_use","id":"tu-2","name":"Edit","input":{"file_path":"/repo/bug.go","old_string":"bad","new_string":"good"}}]}`,
		// Lines 5-6: file-history-snapshot, system — dropped
	}

	result, err := Compact([]byte(fixtureFullJSONL), defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

// --- Field order test (exact byte comparison since marshalOrdered is deterministic) ---

func TestCompact_FieldOrder(t *testing.T) {
	t.Parallel()

	input := []byte(`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00Z","message":{"content":"hello"}}
`)

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"2026-01-01T00:00:00Z","content":"hello"}` + "\n"
	if string(result) != expected {
		t.Errorf("field order mismatch:\ngot:  %s\nwant: %s", string(result), expected)
	}
}

// --- Shared toolUseResult limitation test ---

func TestCompact_MultipleToolResults_SharedResult(t *testing.T) {
	t.Parallel()

	// full.jsonl has a single toolUseResult per user entry. When there are multiple
	// tool_result blocks, each user_tool_result line gets the same minimized result.
	// This test documents that known limitation.
	input := []byte(`{"type":"user","uuid":"u1","timestamp":"t1","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":"result1"},{"type":"tool_result","tool_use_id":"tu-2","content":"result2"},{"type":"text","text":"ok"}]},"toolUseResult":{"type":"text","file":{"filePath":"/repo/a.txt","numLines":5}}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"t1","content":"ok"}`,
		// Both tool results get the same minimized result — this is intentionally lossy
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"t1","tool_use_id":"tu-1","result":{"type":"text","file":{"filePath":"/repo/a.txt","numLines":5}}}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"t1","tool_use_id":"tu-2","result":{"type":"text","file":{"filePath":"/repo/a.txt","numLines":5}}}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

// --- Missing toolUseResult test ---

func TestCompact_ToolResultWithoutToolUseResult(t *testing.T) {
	t.Parallel()

	// User entry has tool_result blocks but no toolUseResult field at all.
	// The result should be an empty object.
	input := []byte(`{"type":"user","uuid":"u1","timestamp":"t1","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":"done"},{"type":"text","text":"next"}]}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"t1","content":"next"}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user_tool_result","ts":"t1","tool_use_id":"tu-1","result":{}}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

// --- Cross-agent format tests ---

func TestCompact_CursorRoleOnly(t *testing.T) {
	t.Parallel()

	cursorOpts := Options{
		Agent:      "cursor",
		CLIVersion: "0.5.1",
		StartLine:  0,
	}

	// Cursor transcripts use "role" instead of "type".
	input := []byte(`{"role":"user","timestamp":"t1","message":{"content":"hello from cursor"}}
{"role":"assistant","timestamp":"t2","message":{"content":[{"type":"text","text":"Hi from Cursor!"}]}}
`)

	expected := []string{
		`{"v":1,"agent":"cursor","cli_version":"0.5.1","type":"user","ts":"t1","content":"hello from cursor"}`,
		`{"v":1,"agent":"cursor","cli_version":"0.5.1","type":"assistant","ts":"t2","content":[{"type":"text","text":"Hi from Cursor!"}]}`,
	}

	result, err := Compact(input, cursorOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_HumanTypeAlias(t *testing.T) {
	t.Parallel()

	// Claude/Gemini-style transcripts may use type:"human" for user messages.
	input := []byte(`{"type":"human","timestamp":"2026-01-01T00:00:00Z","message":{"content":"hello human"}}
{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"id":"m1","content":[{"type":"text","text":"Hi!"}]}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"2026-01-01T00:00:00Z","content":"hello human"}`,
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"assistant","ts":"2026-01-01T00:00:01Z","id":"m1","content":[{"type":"text","text":"Hi!"}]}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_MixedFormats(t *testing.T) {
	t.Parallel()

	cursorOpts := Options{
		Agent:      "cursor",
		CLIVersion: "0.5.1",
		StartLine:  0,
	}

	// Mixed transcript: type-based Claude entries, role-based Cursor entries, and human alias.
	input := []byte(`{"type":"user","timestamp":"t1","message":{"content":"claude user"}}
{"type":"assistant","timestamp":"t2","message":{"id":"m1","content":[{"type":"text","text":"claude assistant"}]}}
{"role":"user","timestamp":"t3","message":{"content":"cursor user"}}
{"role":"assistant","timestamp":"t4","message":{"content":[{"type":"text","text":"cursor assistant"}]}}
{"type":"human","timestamp":"t5","message":{"content":"human alias"}}
{"type":"progress","message":{"content":"should be dropped"}}
`)

	expected := []string{
		`{"v":1,"agent":"cursor","cli_version":"0.5.1","type":"user","ts":"t1","content":"claude user"}`,
		`{"v":1,"agent":"cursor","cli_version":"0.5.1","type":"assistant","ts":"t2","id":"m1","content":[{"type":"text","text":"claude assistant"}]}`,
		`{"v":1,"agent":"cursor","cli_version":"0.5.1","type":"user","ts":"t3","content":"cursor user"}`,
		`{"v":1,"agent":"cursor","cli_version":"0.5.1","type":"assistant","ts":"t4","content":[{"type":"text","text":"cursor assistant"}]}`,
		`{"v":1,"agent":"cursor","cli_version":"0.5.1","type":"user","ts":"t5","content":"human alias"}`,
	}

	result, err := Compact(input, cursorOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

// --- IDE context tag stripping tests ---

func TestCompact_StripsIDEContextTags(t *testing.T) {
	t.Parallel()

	// User text with Cursor's <user_query> tags should have tags stripped.
	input := []byte(`{"role":"user","timestamp":"t1","message":{"content":"<user_query>\nhello world\n</user_query>"}}
`)

	cursorOpts := Options{
		Agent:      "cursor",
		CLIVersion: "0.5.1",
		StartLine:  0,
	}

	expected := []string{
		`{"v":1,"agent":"cursor","cli_version":"0.5.1","type":"user","ts":"t1","content":"hello world"}`,
	}

	result, err := Compact(input, cursorOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_StripsIDEContextTagsFromContentBlocks(t *testing.T) {
	t.Parallel()

	// Array content with IDE tags in text blocks.
	input := []byte(`{"type":"user","timestamp":"t1","message":{"content":[{"type":"text","text":"<user_query>\nfix the bug\n</user_query>"},{"type":"text","text":"also this"}]}}
`)

	expected := []string{
		`{"v":1,"agent":"claude-code","cli_version":"0.5.1","type":"user","ts":"t1","content":"fix the bug\n\nalso this"}`,
	}

	result, err := Compact(input, defaultOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}
