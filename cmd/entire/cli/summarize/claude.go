package summarize

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
)

// summarizationPromptTemplate is the prompt used to generate summaries via the Claude CLI.
//
// Security note: The transcript is wrapped in <transcript> tags to provide clear boundary
// markers. This helps contain any potentially malicious content within the transcript
// (e.g., prompt injection attempts in user messages or file contents) by giving the LLM
// a clear structural signal about where the untrusted content begins and ends.
const summarizationPromptTemplate = `Analyze this development session transcript and generate a structured summary.

<transcript>
%s
</transcript>

Return a JSON object with this exact structure:
{
  "intent": "What the user was trying to accomplish (1-2 sentences)",
  "outcome": "What was actually achieved (1-2 sentences)",
  "learnings": {
    "repo": ["Codebase-specific patterns, conventions, or gotchas discovered"],
    "code": [{"path": "file/path.go", "line": 42, "end_line": 56, "finding": "What was learned"}],
    "workflow": ["General development practices or tool usage insights"]
  },
  "friction": ["Problems, blockers, or annoyances encountered"],
  "open_items": ["Tech debt, unfinished work, or things to revisit later"]
}

Guidelines:
- Be concise but specific
- Include line numbers for code learnings when the transcript references specific lines
- Friction should capture both blockers and minor annoyances
- Open items are things intentionally deferred, not failures
- Empty arrays are fine if a category doesn't apply
- Return ONLY the JSON object, no markdown formatting or explanation`

// DefaultModel is the default model used for summarization.
// Sonnet provides a good balance of quality and cost, with 1M context window
// to handle long transcripts without truncation.
const DefaultModel = "sonnet"

// PrompterGenerator generates summaries using any agent that implements agent.Prompter.
type PrompterGenerator struct {
	// Prompter is the agent's Prompter interface to invoke.
	Prompter agent.Prompter

	// Model overrides the default model for summarization.
	// If empty, defaults to DefaultModel ("sonnet").
	Model string
}

// Generate creates a summary by calling the agent's Prompter interface.
func (g *PrompterGenerator) Generate(ctx context.Context, input Input) (*checkpoint.Summary, error) {
	transcriptText := FormatCondensedTranscript(input)
	prompt := buildSummarizationPrompt(transcriptText)

	model := g.Model
	if model == "" {
		model = DefaultModel
	}

	result, err := g.Prompter.Prompt(ctx, prompt, agent.PromptOptions{
		Model:        model,
		OutputFormat: "json",
		// WorkDir defaults to os.TempDir() in the Prompter implementation,
		// isolating the subprocess from the user's git repo.
	})
	if err != nil {
		return nil, fmt.Errorf("summarize prompt failed: %w", err)
	}

	return parseSummaryFromResult(result.Text)
}

// ClaudeGenerator generates summaries using the Claude CLI directly.
// Kept for backward compatibility and test injection via CommandRunner.
type ClaudeGenerator struct {
	// ClaudePath is the path to the claude CLI executable.
	// If empty, defaults to "claude" (expects it to be in PATH).
	ClaudePath string

	// Model is the Claude model to use for summarization.
	// If empty, defaults to DefaultModel ("sonnet").
	Model string

	// CommandRunner allows injection of the command execution for testing.
	// If nil, uses exec.CommandContext directly.
	CommandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// Generate creates a summary from checkpoint data by calling the Claude CLI.
func (g *ClaudeGenerator) Generate(ctx context.Context, input Input) (*checkpoint.Summary, error) {
	// Format the transcript for the prompt
	transcriptText := FormatCondensedTranscript(input)

	// Build the prompt
	prompt := buildSummarizationPrompt(transcriptText)

	// Execute the Claude CLI
	runner := g.CommandRunner
	if runner == nil {
		runner = exec.CommandContext
	}

	claudePath := g.ClaudePath
	if claudePath == "" {
		claudePath = "claude"
	}

	model := g.Model
	if model == "" {
		model = DefaultModel
	}

	// Use empty --setting-sources to skip all settings (user, project, local).
	// This avoids loading MCP servers, hooks, or other config that could interfere
	// with a simple --print summarization call.
	cmd := runner(ctx, claudePath, "--print", "--output-format", "json", "--model", model, "--setting-sources", "")

	// Fully isolate the subprocess from the user's git repo (ENT-242).
	// Claude Code performs internal git operations (plugin cache, context gathering)
	// that pollute the worktree index with phantom entries from its plugin cache.
	// We must both change the working directory AND strip GIT_* env vars, because
	// git hooks set GIT_DIR which lets Claude Code find the repo regardless of cwd.
	// This also prevents recursive triggering of Entire's own git hooks.
	cmd.Dir = os.TempDir()
	cmd.Env = agent.StripGitEnv(os.Environ())

	// Pass prompt via stdin
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("summarize CLI failed: %w", agent.FormatExecError(err, "claude", stderr.String()))
	}

	// Parse the CLI response
	var cliResponse agent.CLIResponse
	if err := json.Unmarshal(stdout.Bytes(), &cliResponse); err != nil {
		return nil, fmt.Errorf("failed to parse claude CLI response: %w", err)
	}

	return parseSummaryFromResult(cliResponse.Result)
}

// parseSummaryFromResult parses the JSON summary from an agent's text result.
func parseSummaryFromResult(resultText string) (*checkpoint.Summary, error) {
	// Try to extract JSON if it's wrapped in markdown code blocks
	resultJSON := extractJSONFromMarkdown(resultText)

	// Parse the summary from the result
	var summary checkpoint.Summary
	if err := json.Unmarshal([]byte(resultJSON), &summary); err != nil {
		return nil, fmt.Errorf("failed to parse summary JSON: %w (response: %s)", err, resultJSON)
	}

	return &summary, nil
}

// buildSummarizationPrompt creates the prompt for the Claude CLI.
func buildSummarizationPrompt(transcriptText string) string {
	return fmt.Sprintf(summarizationPromptTemplate, transcriptText)
}

// extractJSONFromMarkdown attempts to extract JSON from markdown code blocks.
// If the input is not wrapped in code blocks, it returns the input unchanged.
func extractJSONFromMarkdown(s string) string {
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
