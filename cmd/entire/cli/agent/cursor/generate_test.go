package cursor

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

func TestGenerateText_CLIMissingReturnsTextGenError(t *testing.T) {
	t.Parallel()
	ag := &CursorAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "/nonexistent/binary/that/does/not/exist")
		},
	}
	_, err := ag.GenerateText(context.Background(), "prompt", "")
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("err = %v; want *agent.TextGenError", err)
	}
	if tge.Kind != agent.TextGenErrorCLIMissing {
		t.Errorf("Kind = %q; want cli_missing", tge.Kind)
	}
	if tge.Provider != agent.AgentNameCursor {
		t.Errorf("Provider = %q; want cursor", tge.Provider)
	}
}

func TestGenerateText_AuthFromCapturedStderr(t *testing.T) {
	t.Parallel()
	ag := &CursorAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c",
				`printf '%s' 'ERROR: upstream request failed: 401 Unauthorized' 1>&2; exit 1`)
		},
	}
	_, err := ag.GenerateText(context.Background(), "prompt", "")
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("err = %v; want *agent.TextGenError", err)
	}
	if tge.Kind != agent.TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth", tge.Kind)
	}
}

func TestGenerateText_EmptyStdoutReturnsUnknown(t *testing.T) {
	t.Parallel()
	ag := &CursorAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "true")
		},
	}
	_, err := ag.GenerateText(context.Background(), "prompt", "")
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("err = %v; want *agent.TextGenError", err)
	}
	if tge.Kind != agent.TextGenErrorUnknown {
		t.Errorf("Kind = %q; want unknown", tge.Kind)
	}
}

func TestGenerateText_SuccessReturnsTrimmedStdout(t *testing.T) {
	t.Parallel()
	ag := &CursorAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", `printf '  final generated text  \n'`)
		},
	}
	out, err := ag.GenerateText(context.Background(), "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "final generated text") {
		t.Errorf("output = %q; want to contain 'final generated text'", out)
	}
}
