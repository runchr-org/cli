package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTextGenError_ErrorIncludesKindAndMessage(t *testing.T) {
	t.Parallel()
	e := &TextGenError{Kind: TextGenErrorAuth, Provider: AgentNameClaudeCode, Message: "Invalid API key"}
	s := e.Error()
	if !strings.Contains(s, "auth") {
		t.Errorf("Error() = %q; want to contain kind 'auth'", s)
	}
	if !strings.Contains(s, "Invalid API key") {
		t.Errorf("Error() = %q; want to contain message", s)
	}
}

func TestTextGenError_UnwrapReturnsCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("underlying")
	e := &TextGenError{Kind: TextGenErrorUnknown, Cause: cause}
	if got := errors.Unwrap(e); !errors.Is(got, cause) {
		t.Errorf("Unwrap() = %v; want %v", got, cause)
	}
}

func TestTextGenError_ErrorsAsIntegration(t *testing.T) {
	t.Parallel()
	cause := errors.New("timeout")
	wrapped := fmt.Errorf("operation failed: %w", &TextGenError{
		Kind:     TextGenErrorCLIMissing,
		Provider: AgentNameCodex,
		Message:  "codex not found",
		Cause:    cause,
	})

	var tge *TextGenError
	if !errors.As(wrapped, &tge) {
		t.Fatal("errors.As did not find *TextGenError in wrapped chain")
	}
	if tge.Kind != TextGenErrorCLIMissing {
		t.Errorf("Kind = %q; want %q", tge.Kind, TextGenErrorCLIMissing)
	}
	if !errors.Is(tge, cause) {
		t.Error("errors.Is did not find cause through TextGenError.Unwrap()")
	}
}

// Compile-time coverage for the declarative types introduced alongside
// TextGenError. No behavior assertions yet; the Classify engine and its
// tests land in a follow-up task.
var (
	_ = PhraseRule{Kind: TextGenErrorAuth, Phrase: "invalid api key"}
	_ = EnvelopeResult{Kind: TextGenErrorRateLimit, Message: "slow down", APIStatus: 429}
	_ = ExecResult{Stdout: []byte("o"), Stderr: []byte("e"), ExitCode: 1}
	_ = &Classifier{
		Provider: AgentNameClaudeCode,
		Phrases:  []PhraseRule{{Kind: TextGenErrorAuth, Phrase: "unauthorized"}},
		ParseEnvelope: func(_ []byte) (*EnvelopeResult, bool) {
			return nil, false
		},
	}
	_ = stderrMessageMaxLen
)
