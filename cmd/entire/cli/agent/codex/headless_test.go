package codex_test

import (
	"context"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/codex"
)

var _ agent.HeadlessLauncher = (*codex.CodexAgent)(nil)

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
	if !strings.Contains(joined, "review please") {
		t.Errorf("expected prompt in args; got %q", joined)
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Error("stdio pipes should be left nil for caller")
	}
}
