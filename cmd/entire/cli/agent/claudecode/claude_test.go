package claudecode

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

func TestResolveSessionFile(t *testing.T) {
	t.Parallel()
	ag := &ClaudeCodeAgent{}
	result := ag.ResolveSessionFile("/home/user/.claude/projects/foo", "abc-123-def")
	expected := "/home/user/.claude/projects/foo/abc-123-def.jsonl"
	if result != expected {
		t.Errorf("ResolveSessionFile() = %q, want %q", result, expected)
	}
}

func TestProtectedDirs(t *testing.T) {
	t.Parallel()
	ag := &ClaudeCodeAgent{}
	dirs := ag.ProtectedDirs()
	if len(dirs) != 1 || dirs[0] != ".claude" {
		t.Errorf("ProtectedDirs() = %v, want [.claude]", dirs)
	}
}

func TestGenerateText_ArrayResponse(t *testing.T) {
	t.Parallel()
	ag := &ClaudeCodeAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			response := `[{"type":"system","subtype":"init"},{"type":"assistant","message":"Working on it"},{"type":"result","result":"final generated text"}]`
			return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
		},
	}

	result, err := ag.GenerateText(context.Background(), "prompt", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "final generated text" {
		t.Fatalf("GenerateText() = %q, want %q", result, "final generated text")
	}
}

func TestGenerateText_EnvelopeErrorReturnsTextGenError(t *testing.T) {
	t.Parallel()
	ag := &ClaudeCodeAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			response := `{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"Auth required"}`
			return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
		},
	}
	_, err := ag.GenerateText(context.Background(), "prompt", "")
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("err = %v; want *agent.TextGenError", err)
	}
	if tge.Kind != agent.TextGenErrorAuth {
		t.Fatalf("Kind = %v; want %v", tge.Kind, agent.TextGenErrorAuth)
	}
}

func TestGenerateText_CLIMissing(t *testing.T) {
	t.Parallel()
	ag := &ClaudeCodeAgent{
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
		t.Fatalf("Kind = %v; want %v", tge.Kind, agent.TextGenErrorCLIMissing)
	}
}

func TestGenerateText_StderrAuthFallback(t *testing.T) {
	t.Parallel()
	ag := &ClaudeCodeAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "printf 'Invalid API key' 1>&2; exit 2")
		},
	}
	_, err := ag.GenerateText(context.Background(), "prompt", "")
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("err = %v; want *agent.TextGenError", err)
	}
	if tge.Kind != agent.TextGenErrorAuth {
		t.Fatalf("Kind = %v; want %v", tge.Kind, agent.TextGenErrorAuth)
	}
}
