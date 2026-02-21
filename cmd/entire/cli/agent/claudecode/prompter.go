package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Ensure ClaudeCodeAgent implements Prompter at compile time.
var _ agent.Prompter = (*ClaudeCodeAgent)(nil)

// cliResponse represents the JSON response from the Claude CLI --output-format json.
type cliResponse struct {
	Result string `json:"result"`
}

// CLICommand returns the CLI executable name for Claude Code.
func (c *ClaudeCodeAgent) CLICommand() string {
	return "claude"
}

// Prompt sends a prompt to the Claude CLI and returns the text response.
func (c *ClaudeCodeAgent) Prompt(ctx context.Context, prompt string, opts agent.PromptOptions) (*agent.PromptResult, error) {
	args := []string{"--print"}

	// Add model if specified
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}

	// Add output format
	outputFormat := opts.OutputFormat
	if outputFormat == "" {
		outputFormat = "json"
	}
	args = append(args, "--output-format", outputFormat)

	// Add allowed tools
	if opts.AllowedTools != "" {
		args = append(args, "--allowedTools", opts.AllowedTools)
	}

	// Add permission mode
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}

	// Disable settings sources to prevent hook loops
	args = append(args, "--setting-sources", "")

	//nolint:gosec // args are constructed from trusted config
	cmd := exec.CommandContext(ctx, c.CLICommand(), args...)

	// Set working directory
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	// Set up environment
	env := os.Environ()
	// Strip GIT_* and CLAUDECODE env vars to prevent interference
	env = stripGitEnv(env)
	// Add any extra env vars
	env = append(env, opts.ExtraEnv...)
	cmd.Env = env

	// Pass prompt via stdin
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := &agent.PromptResult{
		ExitCode: 0,
	}

	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return nil, fmt.Errorf("claude CLI not found: %w", err)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, fmt.Errorf("claude CLI failed (exit %d): %s", exitErr.ExitCode(), stderr.String())
		}
		return nil, fmt.Errorf("failed to run claude CLI: %w", err)
	}

	// Parse JSON response if using json output format
	if outputFormat == "json" {
		var cliResp cliResponse
		if jsonErr := json.Unmarshal(stdout.Bytes(), &cliResp); jsonErr == nil {
			result.Text = cliResp.Result
		} else {
			// Fall back to raw output if JSON parsing fails
			result.Text = stdout.String()
		}
	} else {
		result.Text = stdout.String()
	}

	return result, nil
}

// stripGitEnv removes GIT_* and CLAUDECODE env vars to prevent interference
// with the parent's git state and Claude's nested-session detection.
func stripGitEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_") || strings.HasPrefix(e, "CLAUDECODE=") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}
