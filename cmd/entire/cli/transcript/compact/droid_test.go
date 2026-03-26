package compact

import "testing"

// --- Factory AI Droid envelope tests ---

func TestCompact_FactoryDroidEnvelope(t *testing.T) {
	t.Parallel()

	droidOpts := Options{
		Agent:      "factoryai-droid",
		CLIVersion: "0.5.1",
		StartLine:  0,
	}

	// Factory AI Droid wraps messages in a type:"message" envelope with role inside.
	input := []byte(`{"type":"message","id":"m1","timestamp":"t1","message":{"role":"user","content":[{"type":"text","text":"create a file"}]}}
{"type":"message","id":"m2","timestamp":"t2","message":{"role":"assistant","content":[{"type":"text","text":"Done!"},{"type":"tool_use","id":"tu-1","name":"Write","input":{"file_path":"hello.txt","content":"hi"}}]}}
`)

	expected := []string{
		`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"user","ts":"t1","content":"create a file"}`,
		`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"assistant","ts":"t2","content":[{"type":"text","text":"Done!"},{"type":"tool_use","id":"tu-1","name":"Write","input":{"file_path":"hello.txt","content":"hi"}}]}`,
	}

	result, err := Compact(input, droidOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_FactoryDroidWithToolResult(t *testing.T) {
	t.Parallel()

	droidOpts := Options{
		Agent:      "factoryai-droid",
		CLIVersion: "0.5.1",
		StartLine:  0,
	}

	// Droid user message with tool_result blocks inside the envelope.
	input := []byte(`{"type":"message","id":"m1","timestamp":"t1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-1","content":"success"},{"type":"text","text":"next step"}]}}
`)

	expected := []string{
		`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"user","ts":"t1","content":"next step"}`,
		`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"user_tool_result","ts":"t1","tool_use_id":"tu-1","result":{}}`,
	}

	result, err := Compact(input, droidOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}

func TestCompact_FactoryDroidNonMessageEntriesDropped(t *testing.T) {
	t.Parallel()

	droidOpts := Options{
		Agent:      "factoryai-droid",
		CLIVersion: "0.5.1",
		StartLine:  0,
	}

	// Non-message Droid entries (session_start, etc.) should be dropped.
	input := []byte(`{"type":"session_start","id":"sess-1","title":"test"}
{"type":"message","id":"m1","timestamp":"t1","message":{"role":"user","content":"hello"}}
{"type":"session_event","data":"something"}
{"type":"message","id":"m2","timestamp":"t2","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
`)

	expected := []string{
		`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"user","ts":"t1","content":"hello"}`,
		`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"assistant","ts":"t2","content":[{"type":"text","text":"hi"}]}`,
	}

	result, err := Compact(input, droidOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}
