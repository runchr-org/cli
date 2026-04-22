package agent_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/codex"
	"github.com/entireio/cli/cmd/entire/cli/agent/copilotcli"
	"github.com/entireio/cli/cmd/entire/cli/agent/cursor"
	"github.com/entireio/cli/cmd/entire/cli/agent/geminicli"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
)

// TestGenerateText_Matrix exercises each non-Claude summary provider against
// the four canonical failure + success scenarios. Claude has its own tests in
// claudecode/ because its classification order (envelope first) differs.
// Gemini's provider-specific phrase heuristic is covered separately in
// geminicli/ since it is the only agent with an extraClassify hook.
func TestGenerateText_Matrix(t *testing.T) {
	t.Parallel()

	type textGenerator interface {
		GenerateText(ctx context.Context, prompt, model string) (string, error)
	}
	type agentCase struct {
		name     string
		provider types.AgentName
		make     func(runner agent.TextCommandRunner) textGenerator
	}
	agents := []agentCase{
		{"cursor", agent.AgentNameCursor, func(r agent.TextCommandRunner) textGenerator {
			return &cursor.CursorAgent{CommandRunner: r}
		}},
		{"codex", agent.AgentNameCodex, func(r agent.TextCommandRunner) textGenerator {
			return &codex.CodexAgent{CommandRunner: r}
		}},
		{"copilotcli", agent.AgentNameCopilotCLI, func(r agent.TextCommandRunner) textGenerator {
			return &copilotcli.CopilotCLIAgent{CommandRunner: r}
		}},
		{"geminicli", agent.AgentNameGemini, func(r agent.TextCommandRunner) textGenerator {
			return &geminicli.GeminiCLIAgent{CommandRunner: r}
		}},
	}

	missing := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "/nonexistent/binary/that/does/not/exist")
	}
	auth401 := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf 'ERROR: 401 Unauthorized' 1>&2; exit 1`)
	}
	empty := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	}
	success := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `printf 'hello world\n'`)
	}

	for _, a := range agents {
		t.Run(a.name+"/CLIMissing", func(t *testing.T) {
			t.Parallel()
			_, err := a.make(missing).GenerateText(context.Background(), "prompt", "")
			var tge *agent.TextGenError
			if !errors.As(err, &tge) || tge.Kind != agent.TextGenErrorCLIMissing || tge.Provider != a.provider {
				t.Errorf("CLIMissing: got %#v", tge)
			}
		})
		t.Run(a.name+"/AuthFrom401", func(t *testing.T) {
			t.Parallel()
			_, err := a.make(auth401).GenerateText(context.Background(), "prompt", "")
			var tge *agent.TextGenError
			if !errors.As(err, &tge) || tge.Kind != agent.TextGenErrorAuth {
				t.Errorf("AuthFrom401: got %#v", tge)
			}
		})
		t.Run(a.name+"/EmptyStdout", func(t *testing.T) {
			t.Parallel()
			_, err := a.make(empty).GenerateText(context.Background(), "prompt", "")
			var tge *agent.TextGenError
			if !errors.As(err, &tge) || tge.Kind != agent.TextGenErrorUnknown {
				t.Errorf("EmptyStdout: got %#v", tge)
			}
		})
		t.Run(a.name+"/Success", func(t *testing.T) {
			t.Parallel()
			out, err := a.make(success).GenerateText(context.Background(), "prompt", "")
			if err != nil {
				t.Fatalf("Success: unexpected error: %v", err)
			}
			if !strings.Contains(out, "hello world") {
				t.Errorf("Success: out = %q; want to contain 'hello world'", out)
			}
		})
	}
}
