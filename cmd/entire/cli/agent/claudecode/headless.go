package claudecode

import (
	"context"
	"os/exec"
)

// LaunchHeadlessCmd builds an *exec.Cmd for `claude --print "<initialPrompt>"`.
// Non-interactive mode — Claude processes the prompt, prints the final
// response to stdout, and exits. The UserPromptSubmit hook fires during
// the run so pending-review marker adoption works. Stdio is left unwired
// for the caller to assign (orchestrator uses teeWriters to capture
// stdout into both a final-response buffer and a preview channel).
//
// Binary lookup is deferred to *exec.Cmd.Start: passing the bare name
// "claude" lets exec.CommandContext stash an "executable not found"
// error on cmd.Err that surfaces when the orchestrator runs the
// command. This keeps construction infallible so unit tests can verify
// argv shape without requiring the claude binary on PATH.
//
//nolint:unparam // interface signature requires (*exec.Cmd, error); construction is infallible by design (binary lookup deferred to Cmd.Start).
func (c *ClaudeCodeAgent) LaunchHeadlessCmd(ctx context.Context, initialPrompt string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "claude", "--print", initialPrompt), nil
}
