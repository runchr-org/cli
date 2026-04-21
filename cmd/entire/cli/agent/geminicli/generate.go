package geminicli

import (
	"context"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// GenerateText sends a prompt to the Gemini CLI and returns the raw text response.
//
// The prompt is piped to the Gemini CLI via stdin rather than embedded in argv.
// Per gemini --help, the -p/--prompt flag is appended to any input read from
// stdin; we pass a single-space placeholder to trigger headless (non-interactive)
// mode and let stdin carry the actual content, avoiding argv size limits.
func (g *GeminiCLIAgent) GenerateText(ctx context.Context, prompt, model string) (string, error) {
	args := []string{"-p", " "}
	if model != "" {
		args = append(args, "--model", model)
	}
	res, runErr := agent.RunIsolatedTextGeneratorCLIRaw(ctx, g.CommandRunner, "gemini", args, prompt)
	if err := Classifier.Classify(ctx, res, runErr); err != nil {
		return "", err //nolint:wrapcheck // preserve *agent.TextGenError / ctx sentinel for errors.As at the explain layer
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return "", &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameGemini,
			Message:  "gemini CLI returned empty output",
		}
	}
	return out, nil
}
