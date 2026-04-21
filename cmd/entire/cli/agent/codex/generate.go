package codex

import (
	"context"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// GenerateText sends a prompt to the Codex CLI and returns the raw text response.
func (c *CodexAgent) GenerateText(ctx context.Context, prompt, model string) (string, error) {
	args := []string{"exec", "--skip-git-repo-check"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "-")

	res, runErr := agent.RunIsolatedTextGeneratorCLIRaw(ctx, c.CommandRunner, "codex", args, prompt)
	if err := Classifier.Classify(ctx, res, runErr); err != nil {
		return "", err //nolint:wrapcheck // preserve *agent.TextGenError / ctx sentinel for errors.As at the explain layer
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return "", &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameCodex,
			Message:  "codex CLI returned empty output",
		}
	}
	return out, nil
}
