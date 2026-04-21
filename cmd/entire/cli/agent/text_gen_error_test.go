package agent

import (
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

func TestTextGenError_ErrorEmptyMessageIncludesExitCode(t *testing.T) {
	t.Parallel()
	e := &TextGenError{Kind: TextGenErrorUnknown, Provider: AgentNameClaudeCode, ExitCode: 137}
	want := "claude-code CLI error (kind=unknown, exit=137)"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q; want %q", got, want)
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

func TestClassifyStderrHTTPStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stderr string
		want   TextGenErrorKind
	}{
		{"401 maps to auth", "ERROR: 401 Unauthorized", TextGenErrorAuth},
		{"403 maps to auth", "ERROR: 403 Forbidden", TextGenErrorAuth},
		{"429 maps to rate_limit", "ERROR: 429 Too Many Requests", TextGenErrorRateLimit},
		{"400 maps to config", "ERROR: 400 Bad Request", TextGenErrorConfig},
		{"404 maps to config", "ERROR: 404 Not Found", TextGenErrorConfig},
		{"no status maps to unknown", "something weird and unclassifiable", TextGenErrorUnknown},
		{"empty maps to unknown", "", TextGenErrorUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyStderrHTTPStatus(tc.stderr); got != tc.want {
				t.Errorf("ClassifyStderrHTTPStatus(%q) = %q; want %q", tc.stderr, got, tc.want)
			}
		})
	}
}

func TestTruncateStderr(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 800)
	got := TruncateStderr(long)
	if len(got) > stderrMessageMaxLen {
		t.Errorf("len = %d; want <= %d", len(got), stderrMessageMaxLen)
	}
	if got := TruncateStderr("  hello  "); got != "hello" {
		t.Errorf("TruncateStderr trims whitespace = %q; want 'hello'", got)
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
			if got := IsExecNotFoundErr(tc.err); got != tc.want {
				t.Errorf("IsExecNotFoundErr(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}
