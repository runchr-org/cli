package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
)

// TextGenErrorKind classifies a typed text-generation CLI error so callers can
// produce actionable user-facing messages without parsing strings.
type TextGenErrorKind string

const (
	// TextGenErrorAuth indicates an authentication or authorization failure
	// (HTTP 401/403, or provider-specific stderr phrase).
	TextGenErrorAuth TextGenErrorKind = "auth"
	// TextGenErrorRateLimit indicates the request was rejected for rate-limit
	// or quota reasons (HTTP 429).
	TextGenErrorRateLimit TextGenErrorKind = "rate_limit"
	// TextGenErrorConfig indicates a client-side request error other than
	// auth or rate-limit (e.g., HTTP 4xx for invalid model or malformed args).
	TextGenErrorConfig TextGenErrorKind = "config"
	// TextGenErrorCLIMissing indicates the provider's binary was not found on PATH.
	TextGenErrorCLIMissing TextGenErrorKind = "cli_missing"
	// TextGenErrorUnknown is the catch-all for failures we cannot classify.
	TextGenErrorUnknown TextGenErrorKind = "unknown"
)

// TextGenError is the shared typed error every summary provider's GenerateText
// returns on failure. APIStatus and ExitCode use 0 for "not applicable".
type TextGenError struct {
	Kind      TextGenErrorKind
	Provider  types.AgentName
	Message   string
	APIStatus int
	ExitCode  int
	Cause     error
}

func (e *TextGenError) Error() string {
	if e.Message == "" {
		if e.ExitCode != 0 {
			return fmt.Sprintf("%s CLI error (kind=%s, exit=%d)", e.Provider, e.Kind, e.ExitCode)
		}
		return fmt.Sprintf("%s CLI error (kind=%s)", e.Provider, e.Kind)
	}
	return fmt.Sprintf("%s CLI error (kind=%s): %s", e.Provider, e.Kind, e.Message)
}

func (e *TextGenError) Unwrap() error { return e.Cause }

// ExecResult is what RunIsolatedTextGeneratorCLIRaw returns: the raw pieces
// a caller needs to classify a subprocess outcome.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// stderrMessageMaxLen caps the Message field size when derived from stderr.
const stderrMessageMaxLen = 500

// TruncateStderr trims whitespace and caps stderr for use as a TextGenError
// Message. Shared across providers so the user-facing Message is predictable.
//
// UTF-8 safe: a naive byte-slice at stderrMessageMaxLen can land in the middle
// of a multi-byte rune, producing invalid UTF-8 in the rendered error message.
// strings.ToValidUTF8 replaces any broken trailing sequence with "".
func TruncateStderr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= stderrMessageMaxLen {
		return s
	}
	truncated := s[:stderrMessageMaxLen]
	if !utf8.ValidString(truncated) {
		truncated = strings.ToValidUTF8(truncated, "")
	}
	return truncated
}

// IsExecNotFoundErr reports whether err indicates the CLI binary was not found
// on PATH. Intentionally excludes other *exec.Error causes (permission denied,
// invalid executable format), which should surface as a generic failure so
// operators aren't misdirected to a reinstall when the real problem is a
// broken/inaccessible binary.
func IsExecNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return true
	}
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

// http4xxPattern matches a standalone 4xx HTTP status code, bounded by word
// breaks on both sides. This avoids the false positives that plain
// strings.Contains hits: port numbers ("port 14010" contains "401"),
// timestamps ("took 429ms" contains "429"), request IDs, byte counts, etc.
// The \b boundaries require a non-word character (or string edge) on either
// side — so "401 Unauthorized" / "status 401" / "HTTP 401:" all match, while
// "14010" / "429ms" / "status429" do not.
var http4xxPattern = regexp.MustCompile(`\b4\d{2}\b`)

// ClassifyStderrHTTPStatus scans stderr for an HTTP status code and returns
// the matching error Kind. Most CLIs pass through their upstream API's HTTP
// status on failure, so this is the load-bearing classification signal.
// Returns TextGenErrorUnknown when no recognized status is present.
//
// When multiple 4xx codes appear in stderr, the first (leftmost) match wins —
// this matches the typical "HTTP error: 401 Unauthorized\ndetail: ..." pattern
// where the leading status is the primary failure.
func ClassifyStderrHTTPStatus(stderr string) TextGenErrorKind {
	match := http4xxPattern.FindString(stderr)
	switch match {
	case "401", "403":
		return TextGenErrorAuth
	case "429":
		return TextGenErrorRateLimit
	case "400", "404":
		return TextGenErrorConfig
	}
	return TextGenErrorUnknown
}

// HandleTextGenResult converts the outcome of a RunIsolatedTextGeneratorCLIRaw
// call into (trimmed stdout, err). On success returns (output, nil). On
// failure returns ("", *TextGenError) or ("", ctx sentinel).
//
// extraClassify is an optional per-agent hook invoked only when the shared
// HTTP-status baseline returned Unknown — used by agents whose stderr carries
// auth/rate-limit signals without an HTTP status (e.g. gemini). Pass nil to
// skip.
//
// emptyMsg populates TextGenError.Message when the subprocess exits 0 with no
// stdout.
//
// Claude does not use this helper — its envelope-first classification order
// differs and is inlined in claudecode.GenerateText.
func HandleTextGenResult(res ExecResult, runErr error, provider types.AgentName, emptyMsg string, extraClassify func(stderr string) TextGenErrorKind) (string, error) {
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			return "", context.Canceled
		}
		if errors.Is(runErr, context.DeadlineExceeded) {
			return "", context.DeadlineExceeded
		}
		if IsExecNotFoundErr(runErr) {
			return "", &TextGenError{Kind: TextGenErrorCLIMissing, Provider: provider, Cause: runErr}
		}
		stderr := TruncateStderr(string(res.Stderr))
		kind := ClassifyStderrHTTPStatus(stderr)
		if kind == TextGenErrorUnknown && extraClassify != nil {
			if k := extraClassify(stderr); k != TextGenErrorUnknown {
				kind = k
			}
		}
		return "", &TextGenError{
			Kind:     kind,
			Provider: provider,
			Message:  stderr,
			ExitCode: res.ExitCode,
			Cause:    runErr,
		}
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return "", &TextGenError{
			Kind:     TextGenErrorUnknown,
			Provider: provider,
			Message:  emptyMsg,
		}
	}
	return out, nil
}
