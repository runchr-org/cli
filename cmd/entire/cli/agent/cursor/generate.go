package cursor

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// GenerateText sends a prompt to the Cursor agent CLI and returns the raw text response.
//
// The prompt is piped via stdin rather than as a positional argument, avoiding
// argv size limits. --print triggers non-interactive mode; --force --trust
// are required for headless operation per cursor-agent --help.
func (c *CursorAgent) GenerateText(ctx context.Context, prompt string, model string) (string, error) {
	args := []string{"--print", "--force", "--trust", "--workspace", os.TempDir()}
	if model != "" {
		args = append(args, "--model", model)
	}
	res, runErr := agent.RunIsolatedTextGeneratorCLIRaw(ctx, c.CommandRunner, "agent", args, prompt)
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
				Provider: agent.AgentNameCursor,
				Cause:    runErr,
			}
		}
		stderr := agent.TruncateStderr(string(res.Stderr))
		return "", &agent.TextGenError{
			Kind:     agent.ClassifyStderrHTTPStatus(stderr),
			Provider: agent.AgentNameCursor,
			Message:  stderr,
			ExitCode: res.ExitCode,
			Cause:    runErr,
		}
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return "", &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameCursor,
			Message:  "cursor CLI returned empty output",
		}
	}
	return out, nil
}
