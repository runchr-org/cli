package compact

import (
	"testing"
)

func TestCompact_GeminiFixture(t *testing.T) {
	t.Parallel()
	assertFixtureTransform(t, agentOpts("gemini-cli"), "testdata/gemini_full.jsonl", "testdata/gemini_expected.jsonl")
}
