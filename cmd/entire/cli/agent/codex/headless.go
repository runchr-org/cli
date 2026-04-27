package codex

import (
	"context"
	"os/exec"
	"strings"
)

// LaunchHeadlessCmd builds an *exec.Cmd for
// `codex exec --skip-git-repo-check -`, mirroring the args+stdin pattern
// that GenerateText uses. The trailing "-" tells codex to read the prompt
// from stdin (argv cannot carry large prompts).
//
// Unlike GenerateText (which isolates the subprocess via
// RunIsolatedTextGeneratorCLI to prevent hooks from firing for meta
// calls), review MUST fire the agent's UserPromptSubmit hook so the
// pending-review marker gets adopted. So cmd.Dir and cmd.Env are left
// unset — the subprocess inherits the user's repo CWD and env. Stdout
// and Stderr are left nil for the caller to wire.
//
// Binary lookup is deferred to *exec.Cmd.Start: passing the bare name
// "codex" lets exec.CommandContext stash an "executable not found"
// error on cmd.Err that surfaces when the orchestrator runs the
// command. This keeps construction infallible so unit tests can verify
// argv shape without requiring the codex binary on PATH.
//
//nolint:unparam // interface signature requires (*exec.Cmd, error); construction is infallible by design (binary lookup deferred to Cmd.Start).
func (c *CodexAgent) LaunchHeadlessCmd(ctx context.Context, initialPrompt string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "codex", "exec", "--skip-git-repo-check", "-")
	cmd.Stdin = strings.NewReader(initialPrompt)
	return cmd, nil
}
