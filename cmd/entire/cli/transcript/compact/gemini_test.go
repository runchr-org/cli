package compact

import (
	"os"
	"testing"
)

func TestCompact_GeminiFixture(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/gemini_full.jsonl")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	geminiOpts := Options{
		Agent:      "gemini-cli",
		CLIVersion: "0.5.1",
		StartLine:  0,
	}

	expected, err := os.ReadFile("testdata/gemini_expected.jsonl")
	if err != nil {
		t.Fatalf("failed to read expected output: %v", err)
	}

	result, err := Compact(input, geminiOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, nonEmptyLines(expected))
}
