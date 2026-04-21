package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

func TestClassify_ContextDeadline(t *testing.T) {
	t.Parallel()
	c := &Classifier{Provider: AgentNameCodex}
	err := c.Classify(context.Background(), ExecResult{}, context.DeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Classify(DeadlineExceeded) = %v; want sentinel passthrough", err)
	}
	var tge *TextGenError
	if errors.As(err, &tge) {
		t.Errorf("ctx sentinel must not be wrapped in TextGenError; got %#v", tge)
	}
}

func TestClassify_ContextCanceled(t *testing.T) {
	t.Parallel()
	c := &Classifier{Provider: AgentNameCodex}
	err := c.Classify(context.Background(), ExecResult{}, context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Classify(Canceled) = %v; want sentinel passthrough", err)
	}
}

func TestClassify_CLIMissingFromExecNotFound(t *testing.T) {
	t.Parallel()
	c := &Classifier{Provider: AgentNameGemini}
	err := c.Classify(context.Background(), ExecResult{}, &exec.Error{Name: "gemini", Err: exec.ErrNotFound})
	var tge *TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("want *TextGenError; got %v", err)
	}
	if tge.Kind != TextGenErrorCLIMissing {
		t.Errorf("Kind = %q; want %q", tge.Kind, TextGenErrorCLIMissing)
	}
	if tge.Provider != AgentNameGemini {
		t.Errorf("Provider = %q; want %q", tge.Provider, AgentNameGemini)
	}
}

func TestClassify_EnvelopeWinsOverStderr(t *testing.T) {
	t.Parallel()
	// Fake envelope parser simulates Claude's is_error:true with HTTP 401.
	parseEnv := func(_ []byte) (*EnvelopeResult, bool) {
		return &EnvelopeResult{Kind: TextGenErrorAuth, Message: "auth from envelope", APIStatus: 401}, true
	}
	c := &Classifier{Provider: AgentNameClaudeCode, ParseEnvelope: parseEnv}
	err := c.Classify(context.Background(), ExecResult{Stderr: []byte("401 Unauthorized in stderr")}, errors.New("exit 1"))
	var tge *TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("want *TextGenError; got %v", err)
	}
	if tge.Kind != TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth (from envelope)", tge.Kind)
	}
	if tge.Message != "auth from envelope" {
		t.Errorf("Message = %q; want envelope message to win over stderr", tge.Message)
	}
	if tge.APIStatus != 401 {
		t.Errorf("APIStatus = %d; want 401", tge.APIStatus)
	}
}

func TestClassify_EnvelopeParserReportsNoStructuredError(t *testing.T) {
	t.Parallel()
	parseEnv := func(_ []byte) (*EnvelopeResult, bool) { return nil, false }
	c := &Classifier{Provider: AgentNameClaudeCode, ParseEnvelope: parseEnv}
	// runErr == nil AND parser says no structured error → nil.
	if err := c.Classify(context.Background(), ExecResult{}, nil); err != nil {
		t.Errorf("Classify(nil, nil) with no-structured-error envelope = %v; want nil", err)
	}
}

func TestClassify_HTTPBaseline401MapsToAuth(t *testing.T) {
	t.Parallel()
	c := &Classifier{Provider: AgentNameCodex}
	err := c.Classify(context.Background(),
		ExecResult{Stderr: []byte("ERROR: unexpected status 401 Unauthorized"), ExitCode: 1},
		errors.New("exit 1"))
	var tge *TextGenError
	if !errors.As(err, &tge) || tge.Kind != TextGenErrorAuth {
		t.Errorf("want Auth; got %#v", tge)
	}
}

func TestClassify_HTTPBaseline429MapsToRateLimit(t *testing.T) {
	t.Parallel()
	c := &Classifier{Provider: AgentNameGemini}
	err := c.Classify(context.Background(),
		ExecResult{Stderr: []byte("429 Too Many Requests"), ExitCode: 1},
		errors.New("exit 1"))
	var tge *TextGenError
	if !errors.As(err, &tge) || tge.Kind != TextGenErrorRateLimit {
		t.Errorf("want RateLimit; got %#v", tge)
	}
}

func TestClassify_EnvelopeWinsOverContextSentinel(t *testing.T) {
	t.Parallel()
	// A Claude subprocess that both hits a context deadline AND emits an
	// is_error envelope on stdout — the envelope wins. Matches 963 at
	// claudecode/generate.go:52-77.
	parseEnv := func(_ []byte) (*EnvelopeResult, bool) {
		return &EnvelopeResult{Kind: TextGenErrorAuth, Message: "auth despite ctx", APIStatus: 401}, true
	}
	c := &Classifier{Provider: AgentNameClaudeCode, ParseEnvelope: parseEnv}
	err := c.Classify(context.Background(),
		ExecResult{Stdout: []byte(`{"is_error":true}`), Stderr: nil, ExitCode: 0},
		context.DeadlineExceeded)
	var tge *TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("want *TextGenError from envelope; got %v", err)
	}
	if tge.Kind != TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth (envelope should win over ctx sentinel)", tge.Kind)
	}
	if tge.Message != "auth despite ctx" {
		t.Errorf("Message = %q; want envelope's message", tge.Message)
	}
	// The returned error is the structured TextGenError, not a bare ctx
	// sentinel passthrough — that is 963's entire point. (The ctx sentinel
	// may still be reachable via Unwrap through TextGenError.Cause for
	// debugging, but the outer error type must be *TextGenError.)
	if err == context.DeadlineExceeded { //nolint:errorlint // must check identity, not unwrap chain
		t.Error("returned error must be *TextGenError from envelope, not bare context.DeadlineExceeded")
	}
}

func TestClassify_PerAgentPhraseMatches(t *testing.T) {
	t.Parallel()
	c := &Classifier{
		Provider: AgentNameCursor,
		Phrases: []PhraseRule{
			{Kind: TextGenErrorAuth, Phrase: "Authentication required"},
		},
	}
	err := c.Classify(context.Background(),
		ExecResult{Stderr: []byte("Error: Authentication required."), ExitCode: 1},
		errors.New("exit 1"))
	var tge *TextGenError
	if !errors.As(err, &tge) || tge.Kind != TextGenErrorAuth {
		t.Errorf("want Auth from phrase match; got %#v", tge)
	}
	if !strings.Contains(tge.Message, "Authentication required") {
		t.Errorf("Message = %q; want to contain the CLI's verbatim stderr", tge.Message)
	}
}

func TestClassify_PhraseMatchCaseInsensitive(t *testing.T) {
	t.Parallel()
	c := &Classifier{
		Provider: AgentNameClaudeCode,
		Phrases:  []PhraseRule{{Kind: TextGenErrorAuth, Phrase: "invalid api key"}},
	}
	err := c.Classify(context.Background(),
		ExecResult{Stderr: []byte("INVALID API KEY"), ExitCode: 1},
		errors.New("exit 1"))
	var tge *TextGenError
	if !errors.As(err, &tge) || tge.Kind != TextGenErrorAuth {
		t.Errorf("want Auth (case-insensitive); got %#v", tge)
	}
}

func TestClassify_FirstMatchWinsInPhraseOrder(t *testing.T) {
	t.Parallel()
	c := &Classifier{
		Provider: AgentNameCodex,
		Phrases: []PhraseRule{
			{Kind: TextGenErrorAuth, Phrase: "specific auth phrase"},
			{Kind: TextGenErrorConfig, Phrase: "auth"}, // would also match, but Auth wins by order
		},
	}
	err := c.Classify(context.Background(),
		ExecResult{Stderr: []byte("specific auth phrase triggered"), ExitCode: 1},
		errors.New("exit 1"))
	var tge *TextGenError
	if !errors.As(err, &tge) || tge.Kind != TextGenErrorAuth {
		t.Errorf("want Auth (first match wins); got %#v", tge)
	}
}

func TestClassify_FallthroughToUnknownPreservesStderr(t *testing.T) {
	t.Parallel()
	c := &Classifier{Provider: AgentNameCopilotCLI}
	err := c.Classify(context.Background(),
		ExecResult{Stderr: []byte("something weird and unclassifiable"), ExitCode: 2},
		errors.New("exit 2"))
	var tge *TextGenError
	if !errors.As(err, &tge) || tge.Kind != TextGenErrorUnknown {
		t.Errorf("want Unknown; got %#v", tge)
	}
	if tge.Message != "something weird and unclassifiable" {
		t.Errorf("Message = %q; want trimmed stderr verbatim", tge.Message)
	}
	if tge.ExitCode != 2 {
		t.Errorf("ExitCode = %d; want 2", tge.ExitCode)
	}
}

func TestClassify_EmptyStderrStillConstructsError(t *testing.T) {
	t.Parallel()
	c := &Classifier{Provider: AgentNameCodex}
	err := c.Classify(context.Background(), ExecResult{ExitCode: 137}, errors.New("killed"))
	var tge *TextGenError
	if !errors.As(err, &tge) {
		t.Fatal("want non-nil *TextGenError even with empty stderr")
	}
	if tge.Message != "" {
		t.Errorf("Message = %q; want empty (formatter fills from ExitCode)", tge.Message)
	}
	if tge.ExitCode != 137 {
		t.Errorf("ExitCode = %d; want 137", tge.ExitCode)
	}
}

func TestClassify_StderrTruncatedTo500Bytes(t *testing.T) {
	t.Parallel()
	c := &Classifier{Provider: AgentNameCodex}
	long := strings.Repeat("x", 800)
	err := c.Classify(context.Background(),
		ExecResult{Stderr: []byte(long), ExitCode: 1},
		errors.New("exit 1"))
	var tge *TextGenError
	if !errors.As(err, &tge) {
		t.Fatal("want *TextGenError")
	}
	if len(tge.Message) > 500 {
		t.Errorf("len(Message) = %d; want <= 500", len(tge.Message))
	}
}

func TestIsExecNotFoundErr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"exec.Error wrapping ErrNotFound", &exec.Error{Name: "codex", Err: exec.ErrNotFound}, true},
		{"top-level exec.ErrNotFound", exec.ErrNotFound, true},
		{"os.ErrNotExist", os.ErrNotExist, true},
		{"wrapped exec.ErrNotFound via fmt.Errorf", fmt.Errorf("spawn failed: %w", exec.ErrNotFound), true},
		{"permission denied is NOT CLI-missing", &exec.Error{Name: "x", Err: os.ErrPermission}, false},
		{"nil is NOT CLI-missing", nil, false},
		{"arbitrary error is NOT CLI-missing", errors.New("some other failure"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isExecNotFoundErr(tc.err); got != tc.want {
				t.Errorf("isExecNotFoundErr(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}
