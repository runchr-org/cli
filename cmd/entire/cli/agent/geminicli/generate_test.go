package geminicli

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
	ag := &GeminiCLIAgent{
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
	if tge.Provider != agent.AgentNameGemini {
		t.Errorf("Provider = %q; want gemini", tge.Provider)
	}
}

func TestGenerateText_AuthFromCapturedStderr(t *testing.T) {
	t.Parallel()
	ag := &GeminiCLIAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c",
				`printf '%s' 'Please set an Auth method in your settings.json or specify one of: GEMINI_API_KEY' 1>&2; exit 41`)
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
	ag := &GeminiCLIAgent{
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
	ag := &GeminiCLIAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", `printf 'hello world\n'`)
		},
	}
	out, err := ag.GenerateText(context.Background(), "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("output = %q; want to contain 'hello world'", out)
	}
}
