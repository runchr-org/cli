package llmcli_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/llmcli"
)

func TestStripGitEnv(t *testing.T) {
	t.Parallel()

	env := []string{
		"HOME=/Users/test",
		"GIT_DIR=/repo/.git",
		"PATH=/usr/bin",
		"GIT_WORK_TREE=/repo",
		"GIT_INDEX_FILE=/repo/.git/index",
		"SHELL=/bin/zsh",
	}

	filtered := llmcli.StripGitEnv(env)

	expected := []string{
		"HOME=/Users/test",
		"PATH=/usr/bin",
		"SHELL=/bin/zsh",
	}

	if len(filtered) != len(expected) {
		t.Fatalf("got %d entries, want %d", len(filtered), len(expected))
	}

	for i, e := range filtered {
		if e != expected[i] {
			t.Errorf("filtered[%d] = %q, want %q", i, e, expected[i])
		}
	}
}

func TestStripGitEnv_Empty(t *testing.T) {
	t.Parallel()

	result := llmcli.StripGitEnv([]string{})
	if len(result) != 0 {
		t.Errorf("expected empty slice, got %v", result)
	}
}

func TestStripGitEnv_NoGitVars(t *testing.T) {
	t.Parallel()

	env := []string{"HOME=/Users/test", "PATH=/usr/bin"}
	result := llmcli.StripGitEnv(env)
	if len(result) != 2 {
		t.Errorf("expected 2 entries, got %d", len(result))
	}
}

func TestExtractJSONFromMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain JSON",
			input:    `{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "json code block",
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "plain code block",
			input:    "```\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "with whitespace",
			input:    "  \n```json\n{\"key\": \"value\"}\n```  \n",
			expected: `{"key": "value"}`,
		},
		{
			name:     "unclosed block",
			input:    "```json\n{\"key\": \"value\"}",
			expected: `{"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := llmcli.ExtractJSONFromMarkdown(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestRunner_Execute_GitIsolation(t *testing.T) {
	var capturedCmd *exec.Cmd

	response := `{"result": "hello world"}`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
			capturedCmd = cmd
			return cmd
		},
	}

	t.Setenv("GIT_DIR", "/some/repo/.git")
	t.Setenv("GIT_WORK_TREE", "/some/repo")
	t.Setenv("GIT_INDEX_FILE", "/some/repo/.git/index")

	_, _, err := runner.Execute(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedCmd == nil {
		t.Fatal("command was not captured")
	}

	if capturedCmd.Dir != os.TempDir() {
		t.Errorf("cmd.Dir = %q, want %q", capturedCmd.Dir, os.TempDir())
	}

	for _, env := range capturedCmd.Env {
		if strings.HasPrefix(env, "GIT_") {
			t.Errorf("found GIT_* env var in subprocess: %s", env)
		}
	}
}

func TestRunner_Execute_ValidResponse(t *testing.T) {
	t.Parallel()

	response := `{"result": "the actual result text"}`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
		},
	}

	result, _, err := runner.Execute(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "the actual result text" {
		t.Errorf("expected %q, got %q", "the actual result text", result)
	}
}

func TestRunner_Execute_MarkdownResult(t *testing.T) {
	t.Parallel()

	// result field contains JSON wrapped in markdown code block
	innerJSON := "```json\\n{\\\"key\\\":\\\"value\\\"}\\n```"
	response := `{"result":"` + innerJSON + `"}`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
		},
	}

	result, _, err := runner.Execute(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != `{"key":"value"}` {
		t.Errorf("expected extracted JSON, got %q", result)
	}
}

func TestRunner_Execute_CommandNotFound(t *testing.T) {
	t.Parallel()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "nonexistent-command-that-should-not-exist-12345")
		},
	}

	_, _, err := runner.Execute(context.Background(), "test prompt")
	if err == nil {
		t.Fatal("expected error when command not found")
	}

	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestRunner_Execute_NonZeroExit(t *testing.T) {
	t.Parallel()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'error message' >&2; exit 1")
		},
	}

	_, _, err := runner.Execute(context.Background(), "test prompt")
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}

	if !strings.Contains(err.Error(), "exit 1") {
		t.Errorf("expected exit code in error, got: %v", err)
	}
}

func TestRunner_Execute_InvalidJSONResponse(t *testing.T) {
	t.Parallel()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "echo", "not valid json")
		},
	}

	_, _, err := runner.Execute(context.Background(), "test prompt")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}

	if !strings.Contains(err.Error(), "parse claude CLI response") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestRunner_Defaults(t *testing.T) {
	t.Parallel()

	var capturedName string
	var capturedArgs []string

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			capturedName = name
			capturedArgs = args
			response := `{"result": "ok"}`
			return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
		},
	}

	_, _, err := runner.Execute(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedName != "claude" {
		t.Errorf("expected default claude path 'claude', got %q", capturedName)
	}

	// Check that --model defaults to "sonnet"
	foundModel := false
	for i, arg := range capturedArgs {
		if arg == "--model" && i+1 < len(capturedArgs) && capturedArgs[i+1] == llmcli.DefaultModel {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Errorf("expected --model %s in args, got %v", llmcli.DefaultModel, capturedArgs)
	}
}

func TestRunner_Execute_ParsesUsage(t *testing.T) {
	t.Parallel()

	response := `{"result":"ok","total_cost_usd":0.0123,"usage":{"input_tokens":100,"cache_creation_input_tokens":50,"cache_read_input_tokens":25,"output_tokens":200}}`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
		},
	}

	result, usage, err := runner.Execute(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected result 'ok', got %q", result)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.TotalCostUSD != 0.0123 {
		t.Errorf("expected TotalCostUSD=0.0123, got %f", usage.TotalCostUSD)
	}
	if usage.InputTokens != 175 { // 100 + 50 + 25
		t.Errorf("expected InputTokens=175, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 200 {
		t.Errorf("expected OutputTokens=200, got %d", usage.OutputTokens)
	}
}

func TestRunner_Execute_UsageMissing(t *testing.T) {
	t.Parallel()

	response := `{"result":"ok"}`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
		},
	}

	_, usage, err := runner.Execute(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	// Zero values when usage field is absent
	if usage.InputTokens != 0 {
		t.Errorf("expected InputTokens=0, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 0 {
		t.Errorf("expected OutputTokens=0, got %d", usage.OutputTokens)
	}
}

func TestRunner_CustomClaudePathAndModel(t *testing.T) {
	t.Parallel()

	var capturedName string
	var capturedArgs []string

	runner := &llmcli.Runner{
		ClaudePath: "/custom/claude",
		Model:      "opus",
		CommandRunner: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			capturedName = name
			capturedArgs = args
			response := `{"result": "ok"}`
			return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
		},
	}

	_, _, err := runner.Execute(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedName != "/custom/claude" {
		t.Errorf("expected /custom/claude, got %q", capturedName)
	}

	foundModel := false
	for i, arg := range capturedArgs {
		if arg == "--model" && i+1 < len(capturedArgs) && capturedArgs[i+1] == "opus" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Errorf("expected --model opus in args, got %v", capturedArgs)
	}
}
