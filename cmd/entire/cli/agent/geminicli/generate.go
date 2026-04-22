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
	return agent.HandleTextGenResult(res, runErr, agent.AgentNameGemini, "gemini CLI returned empty output", classifyGeminiAuthPhrase) //nolint:wrapcheck // preserve *agent.TextGenError / ctx sentinel for errors.As at the explain layer
}

// classifyGeminiAuthPhrase is the extraClassify hook for gemini-cli: its
// auth-failure stderr (from the 2026-04-20 research pass) does NOT contain an
// HTTP status, so the shared baseline misses it. These phrases are verbatim
// from the captured fixture.
func classifyGeminiAuthPhrase(stderr string) agent.TextGenErrorKind {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "please set an auth method") || strings.Contains(lower, "gemini_api_key") {
		return agent.TextGenErrorAuth
	}
	return agent.TextGenErrorUnknown
}
