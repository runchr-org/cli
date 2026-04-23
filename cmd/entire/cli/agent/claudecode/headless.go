package claudecode

import (
	"context"
	"fmt"
	"os/exec"
)

// LaunchHeadlessCmd builds an *exec.Cmd for `claude --print "<initialPrompt>"`.
// Non-interactive mode — Claude processes the prompt, prints the final
// response to stdout, and exits. The UserPromptSubmit hook fires during
// the run so pending-review marker adoption works. Stdio is left unwired
// for the caller to assign (orchestrator uses teeWriters to capture
// stdout into both a final-response buffer and a preview channel).
func (c *ClaudeCodeAgent) LaunchHeadlessCmd(ctx context.Context, initialPrompt string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude binary not on PATH: %w", err)
	}
	return exec.CommandContext(ctx, bin, "--print", initialPrompt), nil
}
