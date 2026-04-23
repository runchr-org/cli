package codex

import (
	"context"
	"fmt"
	"os/exec"
)

// LaunchHeadlessCmd builds an *exec.Cmd for `codex exec "<initialPrompt>"`.
// Non-interactive mode — Codex processes the prompt and exits. Hooks fire
// during the run. Stdio is left unwired for the caller to assign.
func (c *CodexAgent) LaunchHeadlessCmd(ctx context.Context, initialPrompt string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex binary not on PATH: %w", err)
	}
	return exec.CommandContext(ctx, bin, "exec", initialPrompt), nil
}
