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
	// Success path: envelope parsed cleanly with is_error:false. Result is
	// extracted inside parseGenerateTextResponse; caller wants the payload.
	// Classify() already ran parseClaudeEnvelope, which gates on non-empty
	// stdout and returns a structured error on malformed JSON — so reaching
	// here implies the parse succeeded with is_error:false. We re-parse only
	// to extract the Result field; any residual error is impossible.
	result, _, parseErr := parseGenerateTextResponse(res.Stdout)
	if parseErr != nil {
		// Defensive: if this ever triggers the envelope-parser gate was bypassed.
		return "", fmt.Errorf("unexpected parse failure on success path: %w", parseErr)
	}
	return result, nil
}
