package checkpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJoinAndSplitPrompts_RoundTrip(t *testing.T) {
	t.Parallel()

	original := []string{
		"first line\nwith newline",
		"second prompt",
	}

	joined := JoinPrompts(original)
	split := SplitPromptContent(joined)

	require.Len(t, split, 2)
	assert.Equal(t, original, split)
}

func TestSplitPromptContent_EmptyContent(t *testing.T) {
	t.Parallel()

	assert.Nil(t, SplitPromptContent(""))
}

// TestRedactedJoinedPrompts_PreRedactedIsTrustedVerbatim verifies that when
// the caller supplies a non-empty preRedacted string the helper returns it
// untouched and never invokes redact.StringWithPrivacyFilter. The
// pre-redacted path is the optimization that finalizeAllTurnCheckpoints
// relies on to avoid running the OpenAI Privacy Filter once per checkpoint
// over identical joined-prompt strings.
func TestRedactedJoinedPrompts_PreRedactedIsTrustedVerbatim(t *testing.T) {
	t.Parallel()

	const preRedacted = "[REDACTED_PERSON] asked about [REDACTED_EMAIL]"
	got := redactedJoinedPrompts(context.Background(), []string{"raw prompt text"}, preRedacted)
	if got != preRedacted {
		t.Errorf("redactedJoinedPrompts(preRedacted) = %q, want verbatim pass-through %q", got, preRedacted)
	}
}

// TestRedactedJoinedPrompts_EmptyFallsBackToRedaction verifies that when the
// caller omits preRedacted the helper joins and runs the full redaction
// pipeline as a safety net. The test asserts non-empty output and that the
// canonical separator is present, which lets us confirm JoinPrompts ran
// without depending on the exact regex behavior of layers we don't own here.
func TestRedactedJoinedPrompts_EmptyFallsBackToRedaction(t *testing.T) {
	t.Parallel()

	got := redactedJoinedPrompts(context.Background(), []string{"hello", "world"}, "")
	if got == "" {
		t.Fatal("redactedJoinedPrompts(empty preRedacted): want non-empty output, got empty")
	}
	if !strings.Contains(got, PromptSeparator) {
		t.Errorf("redactedJoinedPrompts output = %q, want it to contain PromptSeparator %q", got, PromptSeparator)
	}
}
