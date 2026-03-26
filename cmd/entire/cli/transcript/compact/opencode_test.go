package compact

import (
	"os"
	"testing"
)

func TestCompact_OpenCodeFixture(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/opencode_full.jsonl")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	openCodeOpts := Options{
		Agent:      "opencode",
		CLIVersion: "0.5.1",
		StartLine:  0,
	}

	expected, err := os.ReadFile("testdata/opencode_expected.jsonl")
	if err != nil {
		t.Fatalf("failed to read expected output: %v", err)
	}

	result, err := Compact(input, openCodeOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, nonEmptyLines(expected))
}
