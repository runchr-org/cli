package compact

import (
	"bytes"
	"os"
	"testing"

	"github.com/entireio/cli/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WithOffset is required to be byte-identical to Compact(StartLine=0) and
// to produce an offset equal to today's
// `lines(Compact(StartLine=0)) - lines(Compact(StartLine=K))` calculation —
// the exact value migrate.computeCompactOffset would store on each
// checkpoint. Drift here would silently shift every continuation
// checkpoint's transcript-scroll position, which the user explicitly ruled
// out as a regression risk.
func TestWithOffset_MatchesTodaysComputation(t *testing.T) {
	t.Parallel()

	type fixture struct {
		name    string
		opts    MetadataFields
		path    string
		content []byte // when set, overrides path
		// startLines we want to verify the offset for. 0 is always tested
		// (degenerate case — offset must be 0).
		startLines []int
	}

	cases := []fixture{
		{
			name:       "claude_full",
			opts:       agentOpts("claude-code"),
			content:    []byte(fixtureFullJSONL),
			startLines: []int{0, 1, 2, 3, 4, 5, 6},
		},
		{
			name:       "claude_fixture_file",
			opts:       agentOpts("claude-code"),
			path:       "testdata/claude_full.jsonl",
			startLines: []int{0, 1, 2, 5, 10},
		},
		{
			name:       "claude_fixture_file2",
			opts:       agentOpts("claude-code"),
			path:       "testdata/claude_full2.jsonl",
			startLines: []int{0, 1, 3, 8},
		},
		{
			name:       "copilot_fixture",
			opts:       agentOpts("copilot-cli"),
			path:       "testdata/copilot_full.jsonl",
			startLines: []int{0, 1, 4},
		},
		{
			name:       "droid_fixture",
			opts:       agentOpts("droid"),
			path:       "testdata/droid_full.jsonl",
			startLines: []int{0, 1, 3},
		},
		{
			name:       "codex_fixture",
			opts:       agentOpts("codex"),
			path:       "testdata/codex_full.jsonl",
			startLines: []int{0, 1, 3},
		},
		{
			name:       "opencode_fixture",
			opts:       agentOpts("opencode"),
			path:       "testdata/opencode_full.jsonl",
			startLines: []int{0, 1, 2},
		},
		{
			name:       "gemini_fixture",
			opts:       agentOpts("gemini-cli"),
			path:       "testdata/gemini_full.jsonl",
			startLines: []int{0, 1, 2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			content := tc.content
			if content == nil {
				var err error
				content, err = os.ReadFile(tc.path)
				require.NoError(t, err, "read fixture")
			}

			full, err := Compact(redact.AlreadyRedacted(content), withStartLine(tc.opts, 0))
			require.NoError(t, err, "baseline full Compact")

			for _, k := range tc.startLines {
				t.Run("startLine="+itoa(k), func(t *testing.T) {
					t.Parallel()

					scoped, err := Compact(redact.AlreadyRedacted(content), withStartLine(tc.opts, k))
					require.NoError(t, err)

					expectedOffset := bytes.Count(full, []byte{'\n'}) - bytes.Count(scoped, []byte{'\n'})

					gotBytes, gotOffset, err := WithOffset(redact.AlreadyRedacted(content), tc.opts, k)
					require.NoError(t, err)

					assert.Equal(t, full, gotBytes, "bytes must match Compact(StartLine=0) exactly")
					assert.Equal(t, expectedOffset, gotOffset, "offset must equal lines(full)-lines(scoped)")
				})
			}
		})
	}
}

// withStartLine returns opts with StartLine swapped, leaving the original
// untouched (the test reuses the same fixture opts struct across iterations).
func withStartLine(opts MetadataFields, startLine int) MetadataFields {
	opts.StartLine = startLine
	return opts
}

// itoa avoids pulling strconv just for subtest names.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
