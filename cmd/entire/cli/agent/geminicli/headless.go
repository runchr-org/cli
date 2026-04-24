package geminicli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// LaunchHeadlessCmd builds an *exec.Cmd for `gemini -p " "`, mirroring the
// args+stdin pattern that GenerateText uses. Per `gemini --help`, the
// -p/--prompt flag is appended to any input read from stdin; we pass a
// single-space placeholder to trigger headless (non-interactive) mode and
// let stdin carry the actual prompt, avoiding argv size limits.
//
// Unlike GenerateText (which isolates the subprocess via
// RunIsolatedTextGeneratorCLI to prevent hooks from firing for meta
// calls), review MUST fire the agent's UserPromptSubmit hook so the
// pending-review marker gets adopted. So cmd.Dir and cmd.Env are left
// unset — the subprocess inherits the user's repo CWD and env. Stdout
// and Stderr are left nil for the caller to wire.
func (g *GeminiCLIAgent) LaunchHeadlessCmd(ctx context.Context, initialPrompt string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("gemini")
	if err != nil {
		return nil, fmt.Errorf("gemini binary not on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, "-p", " ")
	cmd.Stdin = strings.NewReader(initialPrompt)
	return cmd, nil
}
