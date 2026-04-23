package claudecode_test

import (
	"context"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/claudecode"
)

// Compile-time pin — ClaudeCodeAgent must satisfy HeadlessLauncher.
var _ agent.HeadlessLauncher = (*claudecode.ClaudeCodeAgent)(nil)

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
