package compact

import "testing"

// --- Factory AI Droid tests ---

func TestCompact_FactoryDroidInlineCases(t *testing.T) {
	t.Parallel()

	droidOpts := agentOpts("factoryai-droid")

	tests := []struct {
		name     string
		input    []byte
		expected []string
	}{
		{
			name: "envelope",
			// Factory AI Droid wraps messages in a type:"message" envelope with role inside.
			input: []byte(`{"type":"message","id":"m1","timestamp":"t1","message":{"role":"user","content":[{"type":"text","text":"create a file"}]}}
{"type":"message","id":"m2","timestamp":"t2","message":{"role":"assistant","content":[{"type":"text","text":"Done!"},{"type":"tool_use","id":"tu-1","name":"Write","input":{"file_path":"hello.txt","content":"hi"}}]}}
`),
			expected: []string{
				`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"user","ts":"t1","content":"create a file"}`,
				`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"assistant","ts":"t2","content":[{"type":"text","text":"Done!"},{"type":"tool_use","id":"tu-1","name":"Write","input":{"file_path":"hello.txt","content":"hi"}}]}`,
			},
		},
		{
			name: "tool result",
			// Droid user message with tool_result blocks inside the envelope.
			input: []byte(`{"type":"message","id":"m1","timestamp":"t1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-1","content":"success"},{"type":"text","text":"next step"}]}}
`),
			expected: []string{
				`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"user","ts":"t1","content":"next step"}`,
			},
		},
		{
			name: "drops non-message entries",
			// Non-message Droid entries (session_start, etc.) should be dropped.
			input: []byte(`{"type":"session_start","id":"sess-1","title":"test"}
{"type":"message","id":"m1","timestamp":"t1","message":{"role":"user","content":"hello"}}
{"type":"session_event","data":"something"}
{"type":"message","id":"m2","timestamp":"t2","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
`),
			expected: []string{
				`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"user","ts":"t1","content":"hello"}`,
				`{"v":1,"agent":"factoryai-droid","cli_version":"0.5.1","type":"assistant","ts":"t2","content":[{"type":"text","text":"hi"}]}`,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := Compact(tc.input, droidOpts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertJSONLines(t, result, tc.expected)
		})
	}
}
