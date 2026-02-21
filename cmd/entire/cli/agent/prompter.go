package agent

import "context"

// PromptOptions configures how an agent CLI is invoked for a prompt.
type PromptOptions struct {
	// Model overrides the default model for this invocation.
	// If empty, uses the agent's default.
	Model string

	// WorkDir is the working directory for the CLI subprocess.
	// If empty, uses the current directory.
	WorkDir string

	// AllowedTools restricts which tools the agent can use (e.g., "Read,Glob,Grep").
	// If empty, no tool restriction is applied.
	AllowedTools string

	// PermissionMode controls the agent's permission behavior.
	// Values: "bypassPermissions", "acceptEdits", etc.
	// If empty, uses the agent's default.
	PermissionMode string

	// OutputFormat controls the output format (e.g., "json", "text").
	// If empty, defaults to agent's standard output.
	OutputFormat string

	// ExtraEnv adds additional environment variables to the subprocess.
	ExtraEnv []string
}

// PromptResult contains the response from an agent prompt invocation.
type PromptResult struct {
	// Text is the extracted text response from the agent.
	Text string

	// ExitCode is the exit code from the CLI subprocess.
	ExitCode int
}

// Prompter is an optional interface for agents that support CLI-based prompting.
// Agents implementing this interface can be invoked with a prompt string and return
// a text response. This powers features like trail runner and automated review.
//
// This follows the same optional interface pattern as TranscriptAnalyzer,
// TokenCalculator, etc. Use a type assertion to check if an agent supports it:
//
//	if prompter, ok := ag.(agent.Prompter); ok {
//	    result, err := prompter.Prompt(ctx, prompt, opts)
//	}
type Prompter interface {
	Agent

	// Prompt sends a prompt to the agent CLI and returns the text response.
	// The implementation handles CLI invocation, environment isolation,
	// output parsing, and error handling.
	Prompt(ctx context.Context, prompt string, opts PromptOptions) (*PromptResult, error)

	// CLICommand returns the CLI executable name for this agent (e.g., "claude").
	// Used for error messages and logging.
	CLICommand() string
}
