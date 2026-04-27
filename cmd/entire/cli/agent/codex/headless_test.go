package codex_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/codex"
)

var _ agent.HeadlessLauncher = (*codex.CodexAgent)(nil)

// LaunchHeadlessCmd must be infallible at construction time even when
// the codex binary isn't on PATH — missing-binary errors surface at
// cmd.Run() instead, so unit tests and CI runners without codex
// installed still verify the argv contract. Regression: an earlier
// implementation called exec.LookPath up front, breaking CI.
func TestCodexAgent_LaunchHeadlessCmd_NoBinaryOnPath(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	// Defensive: confirm the binary really isn't reachable in the
	// scrubbed PATH so a polluted runner can't make this test pass for
	// the wrong reason.
	if _, err := os.Stat("/nonexistent/codex"); err == nil {
		t.Skip("PATH-scrub didn't take effect on this runner")
	}

	a := &codex.CodexAgent{}
	cmd, err := a.LaunchHeadlessCmd(context.Background(), "x")
	if err != nil {
		t.Fatalf("LaunchHeadlessCmd must not return an error when binary is missing; got %v", err)
	}
	if cmd == nil {
		t.Fatal("returned nil cmd")
	}
}

func TestCodexAgent_LaunchHeadlessCmd(t *testing.T) {
	t.Parallel()
	a := &codex.CodexAgent{}
	cmd, err := a.LaunchHeadlessCmd(context.Background(), "review please")
	if err != nil {
		t.Fatalf("LaunchHeadlessCmd: %v", err)
	}
	if cmd == nil {
		t.Fatal("returned nil cmd")
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "exec") {
		t.Errorf("expected 'exec' subcommand in args; got %q", joined)
	}
	if !strings.Contains(joined, "--skip-git-repo-check") {
		t.Errorf("expected --skip-git-repo-check flag in args; got %q", joined)
	}
	// Trailing "-" tells codex to read prompt from stdin.
	hasStdinMarker := false
	for _, a := range cmd.Args {
		if a == "-" {
			hasStdinMarker = true
			break
		}
	}
	if !hasStdinMarker {
		t.Errorf("expected trailing '-' stdin marker in args; got %q", joined)
	}
	// Prompt must NOT appear in argv — it travels via stdin.
	if strings.Contains(joined, "review please") {
		t.Errorf("prompt should not appear in argv (piped via stdin); got %q", joined)
	}
	// Stdin MUST be wired (the prompt pipe); Stdout and Stderr stay nil
	// for the caller to assign.
	if cmd.Stdin == nil {
		t.Error("expected Stdin to be wired with the prompt reader")
	}
	if cmd.Stdout != nil || cmd.Stderr != nil {
		t.Errorf("Stdout/Stderr should be left nil for caller; got stdout=%v stderr=%v",
			cmd.Stdout, cmd.Stderr)
	}
}
