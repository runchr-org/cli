package codex

import (
	"context"
	"errors"
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
				Provider: agent.AgentNameCodex,
				Cause:    runErr,
			}
		}
		stderr := agent.TruncateStderr(string(res.Stderr))
		return "", &agent.TextGenError{
			Kind:     agent.ClassifyStderrHTTPStatus(stderr),
			Provider: agent.AgentNameCodex,
			Message:  stderr,
			ExitCode: res.ExitCode,
			Cause:    runErr,
		}
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
