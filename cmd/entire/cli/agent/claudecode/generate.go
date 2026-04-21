package claudecode

import (
	"context"
	"fmt"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// GenerateText sends a prompt to the Claude CLI and returns the raw text response.
// Implements the agent.TextGenerator interface.
//
// Model defaults to "haiku" for fast, cheap generation (the summarize package
// overrides to "sonnet" via ResolveModel for quality). Classification is
// delegated to the shared engine via Classifier; Claude's envelope-based
// semantics are expressed through the ParseEnvelope hook.
func (c *ClaudeCodeAgent) GenerateText(ctx context.Context, prompt string, model string) (string, error) {
	if model == "" {
		model = "haiku"
	}
	args := []string{
		"--print", "--output-format", "json",
		"--model", model, "--setting-sources", "",
	}
	res, runErr := agent.RunIsolatedTextGeneratorCLIRaw(ctx, c.CommandRunner, "claude", args, prompt)
	if err := Classifier.Classify(ctx, res, runErr); err != nil {
		return "", err //nolint:wrapcheck // preserve *agent.TextGenError / ctx sentinel for errors.As at the explain layer
	}
	// Empty stdout on exit 0: parseClaudeEnvelope has a len(stdout)==0 carveout
	// that lets empty stdout fall through to stderr/CLIMissing classification.
	// When runErr is also nil (clean exit 0, no stderr), Classify returns nil
	// and we land here with nothing to parse. Return the same typed error the
	// other four summary providers return in this shape.
	if len(res.Stdout) == 0 {
		return "", &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameClaudeCode,
			Message:  "claude CLI returned empty output",
		}
	}
	// Success path: envelope parsed cleanly with is_error:false. Result is
	// extracted inside parseGenerateTextResponse; caller wants the payload.
	result, _, parseErr := parseGenerateTextResponse(res.Stdout)
	if parseErr != nil {
		// Defensive: non-empty stdout whose envelope parser said "success"
		// (envelope==nil || !IsError) but which fails to re-parse here would
		// mean parseClaudeEnvelope's and parseGenerateTextResponse's notions
		// of "valid" disagree. Return a typed error so callers can still use
		// errors.As uniformly.
		return "", &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameClaudeCode,
			Message:  fmt.Sprintf("unexpected parse failure on success path: %v", parseErr),
			Cause:    parseErr,
		}
	}
	return result, nil
}
