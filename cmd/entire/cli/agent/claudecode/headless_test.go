package claudecode_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/claudecode"
)

// Compile-time pin — ClaudeCodeAgent must satisfy HeadlessLauncher.
var _ agent.HeadlessLauncher = (*claudecode.ClaudeCodeAgent)(nil)

// LaunchHeadlessCmd must be infallible at construction time even when
// the claude binary isn't on PATH — missing-binary errors surface at
// cmd.Run() instead, so unit tests and CI runners without claude
// installed still verify the argv contract. Regression: an earlier
// implementation called exec.LookPath up front, breaking CI.
func TestClaudeCodeAgent_LaunchHeadlessCmd_NoBinaryOnPath(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	if _, err := os.Stat("/nonexistent/claude"); err == nil {
		t.Skip("PATH-scrub didn't take effect on this runner")
	}

	a := &claudecode.ClaudeCodeAgent{}
	cmd, err := a.LaunchHeadlessCmd(context.Background(), "x")
	if err != nil {
		t.Fatalf("LaunchHeadlessCmd must not return an error when binary is missing; got %v", err)
	}
	if cmd == nil {
		t.Fatal("returned nil cmd")
	}
}

func TestClaudeCodeAgent_LaunchHeadlessCmd(t *testing.T) {
	t.Parallel()
	a := &claudecode.ClaudeCodeAgent{}
	cmd, err := a.LaunchHeadlessCmd(context.Background(), "review please")
	if err != nil {
		t.Fatalf("LaunchHeadlessCmd: %v", err)
	}
	if cmd == nil {
		t.Fatal("returned nil cmd")
	}
	// Program path is resolved via exec.LookPath("claude"); we can't assert
	// on that (binary may not be installed in CI). But we can assert the
	// --print flag and prompt arg are present in order.
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--print") {
		t.Errorf("expected --print in args; got %q", joined)
	}
	if !strings.Contains(joined, "review please") {
		t.Errorf("expected prompt in args; got %q", joined)
	}
	// Stdio MUST NOT be wired to os.Stdin/Stdout/Stderr — caller wires it.
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Errorf("Stdio pipes should be left nil for caller to wire; got stdin=%v stdout=%v stderr=%v",
			cmd.Stdin, cmd.Stdout, cmd.Stderr)
	}
}
