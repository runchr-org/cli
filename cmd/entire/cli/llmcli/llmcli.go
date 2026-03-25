// Package llmcli provides a shared runner for executing prompts via the Claude CLI.
package llmcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DefaultModel is the default Claude model used when Model is not set.
// Sonnet provides a good balance of quality and cost, with 1M context window
// to handle long transcripts without truncation.
const DefaultModel = "sonnet"

// Runner executes prompts via the Claude CLI.
type Runner struct {
	// ClaudePath is the path to the claude CLI executable.
	// If empty, defaults to "claude".
	ClaudePath string

	// Model is the Claude model to use.
	// If empty, defaults to DefaultModel ("sonnet").
	Model string

	// CommandRunner allows injection of the command execution for testing.
	// If nil, uses exec.CommandContext directly.
	CommandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// UsageInfo contains token usage and cost data from a Claude CLI invocation.
type UsageInfo struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// claudeCLIResponse represents the JSON response from the Claude CLI.
type claudeCLIResponse struct {
	Result       string          `json:"result"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	Usage        json.RawMessage `json:"usage"`
}

// claudeCLIUsage represents the usage field in the Claude CLI JSON response.
type claudeCLIUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

// Execute runs a prompt through the Claude CLI and returns the raw text result
// along with token usage and cost information.
//
// It handles:
//   - CLI invocation with --print --output-format json --model --setting-sources ""
//   - Git isolation (TempDir cwd, strip GIT_* env vars)
//   - Response parsing ({"result": "string"} format)
//   - Markdown code block extraction from the result field
func (r *Runner) Execute(ctx context.Context, prompt string) (string, *UsageInfo, error) {
	runner := r.CommandRunner
	if runner == nil {
		runner = exec.CommandContext
	}

	claudePath := r.ClaudePath
	if claudePath == "" {
		claudePath = "claude"
	}

	model := r.Model
	if model == "" {
		model = DefaultModel
	}

	// Use empty --setting-sources to skip all settings (user, project, local).
	// This avoids loading MCP servers, hooks, or other config that could interfere
	// with a simple --print call.
	cmd := runner(ctx, claudePath, "--print", "--output-format", "json", "--model", model, "--setting-sources", "")

	// Fully isolate the subprocess from the user's git repo (ENT-242).
	// Claude Code performs internal git operations (plugin cache, context gathering)
	// that pollute the worktree index with phantom entries from its plugin cache.
	// We must both change the working directory AND strip GIT_* env vars, because
	// git hooks set GIT_DIR which lets Claude Code find the repo regardless of cwd.
	// This also prevents recursive triggering of Entire's own git hooks.
	cmd.Dir = os.TempDir()
	cmd.Env = StripGitEnv(os.Environ())

	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", nil, fmt.Errorf("claude CLI not found: %w", err)
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil, fmt.Errorf("claude CLI failed (exit %d): %s", exitErr.ExitCode(), stderr.String())
		}

		return "", nil, fmt.Errorf("failed to run claude CLI: %w", err)
	}

	var cliResponse claudeCLIResponse
	if err := json.Unmarshal(stdout.Bytes(), &cliResponse); err != nil {
		return "", nil, fmt.Errorf("failed to parse claude CLI response: %w", err)
	}

	// Extract JSON if it's wrapped in markdown code blocks.
	result := ExtractJSONFromMarkdown(cliResponse.Result)

	usage := &UsageInfo{TotalCostUSD: cliResponse.TotalCostUSD}
	if len(cliResponse.Usage) > 0 {
		var u claudeCLIUsage
		if err := json.Unmarshal(cliResponse.Usage, &u); err == nil {
			usage.InputTokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
			usage.OutputTokens = u.OutputTokens
		}
	}

	return result, usage, nil
}

// StripGitEnv returns a copy of env with all GIT_* variables removed.
// This prevents a subprocess from discovering or modifying the parent's git repo.
func StripGitEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "GIT_") {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// ExtractJSONFromMarkdown attempts to extract JSON from markdown code blocks.
// If the input is not wrapped in code blocks, it returns the input unchanged.
func ExtractJSONFromMarkdown(s string) string {
	s = strings.TrimSpace(s)

	// Check for ```json ... ``` blocks
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		return strings.TrimSpace(s)
	}

	// Check for ``` ... ``` blocks
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		return strings.TrimSpace(s)
	}

	return s
}
