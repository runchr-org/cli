package copilotcli

import (
	"context"
	"errors"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// GenerateText sends a prompt to the Copilot CLI and returns the raw text response.
//
// The prompt is piped via stdin rather than -p to avoid argv size limits.
// --allow-all-tools is required for non-interactive mode; --disable-builtin-mcps
// skips loading the GitHub MCP server that isn't needed for summary text
// generation and reduces per-call input tokens.
func (c *CopilotCLIAgent) GenerateText(ctx context.Context, prompt string, model string) (string, error) {
	args := []string{"--allow-all-tools", "--disable-builtin-mcps"}
	if model != "" {
		args = append(args, "--model", model)
	}
	res, runErr := agent.RunIsolatedTextGeneratorCLIRaw(ctx, c.CommandRunner, "copilot", args, prompt)
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
				Provider: agent.AgentNameCopilotCLI,
				Cause:    runErr,
			}
		}
		stderr := agent.TruncateStderr(string(res.Stderr))
		return "", &agent.TextGenError{
			Kind:     agent.ClassifyStderrHTTPStatus(stderr),
			Provider: agent.AgentNameCopilotCLI,
			Message:  stderr,
			ExitCode: res.ExitCode,
			Cause:    runErr,
		}
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return "", &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameCopilotCLI,
			Message:  "copilot CLI returned empty output",
		}
	}
	return out, nil
}
