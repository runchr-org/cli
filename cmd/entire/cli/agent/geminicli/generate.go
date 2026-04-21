package geminicli

import (
	"context"
	"errors"
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
				Provider: agent.AgentNameGemini,
				Cause:    runErr,
			}
		}
		stderr := agent.TruncateStderr(string(res.Stderr))
		kind := agent.ClassifyStderrHTTPStatus(stderr)
		if kind == agent.TextGenErrorUnknown {
			// Inline phrase heuristic — gemini-cli's auth-failure stderr
			// (captured from the 2026-04-20 research pass) does NOT contain
			// an HTTP status, so the shared baseline misses it. These two
			// phrases are verbatim from the captured fixture.
			lower := strings.ToLower(stderr)
			if strings.Contains(lower, "please set an auth method") || strings.Contains(lower, "gemini_api_key") {
				kind = agent.TextGenErrorAuth
			}
		}
		return "", &agent.TextGenError{
			Kind:     kind,
			Provider: agent.AgentNameGemini,
			Message:  stderr,
			ExitCode: res.ExitCode,
			Cause:    runErr,
		}
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
