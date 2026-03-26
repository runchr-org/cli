package compact

import (
	"testing"
)

func TestCompact_OpenCodeFixture(t *testing.T) {
	t.Parallel()
	assertFixtureTransform(t, agentOpts("opencode"), "testdata/opencode_full.jsonl", "testdata/opencode_expected.jsonl")
}
