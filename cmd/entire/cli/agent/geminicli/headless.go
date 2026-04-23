package geminicli

import (
	"context"
	"fmt"
	"os/exec"
)

// LaunchHeadlessCmd builds an *exec.Cmd for `gemini -p "<initialPrompt>"`.
// Non-interactive mode — Gemini processes the prompt and exits.
func (g *GeminiCLIAgent) LaunchHeadlessCmd(ctx context.Context, initialPrompt string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("gemini")
	if err != nil {
		return nil, fmt.Errorf("gemini binary not on PATH: %w", err)
	}
	return exec.CommandContext(ctx, bin, "-p", initialPrompt), nil
}
