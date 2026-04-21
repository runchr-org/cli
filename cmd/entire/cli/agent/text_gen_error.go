package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
)

// TextGenErrorKind classifies a typed text-generation CLI error so callers can
// produce actionable user-facing messages without parsing strings.
type TextGenErrorKind string

const (
	// TextGenErrorAuth indicates an authentication or authorization failure
	// (HTTP 401/403, or recognized stderr substring).
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

// PhraseRule is a substring→Kind mapping used by Classifier. Matching is
// case-insensitive; the first matching rule wins.
type PhraseRule struct {
	Kind   TextGenErrorKind
	Phrase string
}

// EnvelopeResult is what a provider's ParseEnvelope reports when it
// recognizes a structured CLI error (currently Claude only).
type EnvelopeResult struct {
	Kind      TextGenErrorKind
	Message   string
	APIStatus int
}

// Classifier is declarative per-agent configuration. The shared engine
// consumes it; each provider package declares one package-level
// `var Classifier = &agent.Classifier{...}`.
type Classifier struct {
	Provider      types.AgentName
	Phrases       []PhraseRule                                // per-CLI, ordered
	ParseEnvelope func(stdout []byte) (*EnvelopeResult, bool) // optional — Claude only
}

// ExecResult is what RunIsolatedTextGeneratorCLIRaw returns: the raw pieces
// the Classifier consumes.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// stderrMessageMaxLen caps the Message field size when derived from stderr.
// Matches 963's claudecode cap exactly; see spec Risk #3 for rationale.
const stderrMessageMaxLen = 500

// isExecNotFoundErr returns true when err indicates the CLI binary was not
// found on PATH. Mirrors 963's claudecode.isExecNotFound exactly: it
// intentionally excludes other *exec.Error causes (permission denied,
// invalid executable format), which should surface as a generic failure so
// operators aren't misdirected to a reinstall when the real problem is a
// broken/inaccessible binary.
func isExecNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return true
	}
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

// httpStatusBaseline is a provider-agnostic first pass: most CLIs pass through
// the underlying API's HTTP status in stderr. Checked before per-agent phrases
// so behavior is consistent across providers when the status is visible.
var httpStatusBaseline = []PhraseRule{
	{Kind: TextGenErrorAuth, Phrase: "401"},
	{Kind: TextGenErrorAuth, Phrase: "403"},
	{Kind: TextGenErrorRateLimit, Phrase: "429"},
	{Kind: TextGenErrorConfig, Phrase: "400"},
	{Kind: TextGenErrorConfig, Phrase: "404"},
}

// Classify converts raw subprocess signals into *TextGenError. Callers invoke
// Classify *unconditionally* — both on exit 0 and on non-nil runErr — because
// Claude's primary failure mode is exit 0 with is_error:true in the envelope.
//
// Returns nil only when runErr is nil AND (ParseEnvelope is unset OR
// ParseEnvelope reports no structured error). Otherwise returns *TextGenError.
//
// Classification order (first match wins):
//  1. ctx sentinels on runErr (DeadlineExceeded / Canceled) — passthrough,
//     not wrapped in TextGenError.
//  2. CLIMissing detection via isExecNotFoundErr.
//  3. ParseEnvelope(res.Stdout) if set — used for Claude's structured envelope.
//  4. If runErr != nil: HTTP-status baseline substrings in stderr.
//  5. If runErr != nil: per-agent Phrases in stderr, case-insensitive, first-match-wins.
//  6. If runErr != nil: Unknown with Message = trimmed+truncated stderr.
func (c *Classifier) Classify(_ context.Context, res ExecResult, runErr error) error {
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		if errors.Is(runErr, context.Canceled) {
			return context.Canceled
		}
		if isExecNotFoundErr(runErr) {
			return &TextGenError{
				Kind:     TextGenErrorCLIMissing,
				Provider: c.Provider,
				Cause:    runErr,
			}
		}
	}

	// Envelope parser (Claude only) runs regardless of runErr so an is_error
	// envelope on stdout is preferred over stderr classification — matches
	// 963's behavior at claudecode/generate.go:57-69.
	if c.ParseEnvelope != nil {
		if env, ok := c.ParseEnvelope(res.Stdout); ok && env != nil {
			return &TextGenError{
				Kind:      env.Kind,
				Provider:  c.Provider,
				Message:   env.Message,
				APIStatus: env.APIStatus,
				ExitCode:  res.ExitCode,
				Cause:     runErr,
			}
		}
	}

	if runErr == nil {
		return nil
	}

	stderrStr := truncateStderr(string(res.Stderr))

	// HTTP-status baseline before per-agent phrases: any CLI that passes
	// through an HTTP status in stderr gets uniform treatment.
	if rule := matchPhrase(stderrStr, httpStatusBaseline); rule != nil {
		return &TextGenError{
			Kind:     rule.Kind,
			Provider: c.Provider,
			Message:  stderrStr,
			ExitCode: res.ExitCode,
			Cause:    runErr,
		}
	}
	if rule := matchPhrase(stderrStr, c.Phrases); rule != nil {
		return &TextGenError{
			Kind:     rule.Kind,
			Provider: c.Provider,
			Message:  stderrStr,
			ExitCode: res.ExitCode,
			Cause:    runErr,
		}
	}

	return &TextGenError{
		Kind:     TextGenErrorUnknown,
		Provider: c.Provider,
		Message:  stderrStr,
		ExitCode: res.ExitCode,
		Cause:    runErr,
	}
}

func matchPhrase(s string, rules []PhraseRule) *PhraseRule {
	lower := strings.ToLower(s)
	for i := range rules {
		if strings.Contains(lower, strings.ToLower(rules[i].Phrase)) {
			return &rules[i]
		}
	}
	return nil
}

func truncateStderr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > stderrMessageMaxLen {
		s = s[:stderrMessageMaxLen]
	}
	return s
}
