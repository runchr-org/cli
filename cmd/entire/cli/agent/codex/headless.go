package codex

import (
	"context"
	"fmt"
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
func (c *CodexAgent) LaunchHeadlessCmd(ctx context.Context, initialPrompt string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex binary not on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, "exec", "--skip-git-repo-check", "-")
	cmd.Stdin = strings.NewReader(initialPrompt)
	return cmd, nil
}
