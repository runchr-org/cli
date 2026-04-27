package geminicli

import (
	"context"
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
//
// Binary lookup is deferred to *exec.Cmd.Start: passing the bare name
// "gemini" lets exec.CommandContext stash an "executable not found"
// error on cmd.Err that surfaces when the orchestrator runs the
// command. This keeps construction infallible so unit tests can verify
// argv shape without requiring the gemini binary on PATH.
//
//nolint:unparam // interface signature requires (*exec.Cmd, error); construction is infallible by design (binary lookup deferred to Cmd.Start).
func (g *GeminiCLIAgent) LaunchHeadlessCmd(ctx context.Context, initialPrompt string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "gemini", "-p", " ")
	cmd.Stdin = strings.NewReader(initialPrompt)
	return cmd, nil
}
