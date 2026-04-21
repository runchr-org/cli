package claudecode

import (
	"context"
	"errors"
	"fmt"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// GenerateText sends a prompt to the Claude CLI and returns the raw text response.
// Implements the agent.TextGenerator interface.
//
// Model defaults to "haiku" for fast, cheap generation (the summarize package
// overrides to "sonnet" via ResolveModel for quality).
//
// Classification order:
//  1. Envelope on stdout — checked first because Claude's primary failure mode
//     is exit 0 with is_error:true; the structured envelope wins over stderr
//     and ctx sentinels.
//  2. Context sentinels (ctx canceled/deadline) — passthrough, not typed.
//  3. CLIMissing — typed error for "install the binary" remediation.
//  4. Any other run error — stderr classified by HTTP status.
//  5. Empty stdout on exit 0 — typed Unknown with "empty output" message.
func (c *ClaudeCodeAgent) GenerateText(ctx context.Context, prompt string, model string) (string, error) {
	if model == "" {
		model = "haiku"
	}
	args := []string{
		"--print", "--output-format", "json",
		"--model", model, "--setting-sources", "",
	}
	res, runErr := agent.RunIsolatedTextGeneratorCLIRaw(ctx, c.CommandRunner, "claude", args, prompt)

	if env := classifyClaudeEnvelope(res.Stdout); env != nil {
		env.ExitCode = res.ExitCode
		env.Cause = runErr
		return "", env
	}

	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			return "", context.Canceled
		}
		if errors.Is(runErr, context.DeadlineExceeded) {
			return "", context.DeadlineExceeded
		}
		if agent.IsExecNotFoundErr(runErr) {
			return "", &agent.TextGenError{
				Kind:     agent.TextGenErrorCLIMissing,
				Provider: agent.AgentNameClaudeCode,
				Cause:    runErr,
			}
		}
		stderr := agent.TruncateStderr(string(res.Stderr))
		kind := agent.ClassifyStderrHTTPStatus(stderr)
		if kind == agent.TextGenErrorUnknown && containsAuthPhrase(stderr) {
			// Claude's CLI sometimes exits non-zero with auth failure text on
			// stderr before any envelope is produced (e.g. "Invalid API key"
			// with exit 2). The phrase list matches envelopeAuthPhrases in
			// envelope_parser.go; both are from 963.
			kind = agent.TextGenErrorAuth
		}
		return "", &agent.TextGenError{
			Kind:     kind,
			Provider: agent.AgentNameClaudeCode,
			Message:  stderr,
			ExitCode: res.ExitCode,
			Cause:    runErr,
		}
	}

	// Success path. Envelope was nil (stdout empty) or envelope.IsError was false.
	if len(res.Stdout) == 0 {
		return "", &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameClaudeCode,
			Message:  "claude CLI returned empty output",
		}
	}
	result, _, parseErr := parseGenerateTextResponse(res.Stdout)
	if parseErr != nil {
		return "", &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameClaudeCode,
			Message:  fmt.Sprintf("unexpected parse failure on success path: %v", parseErr),
			Cause:    parseErr,
		}
	}
	return result, nil
}
