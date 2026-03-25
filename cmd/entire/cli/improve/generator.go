package improve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/llmcli"
)

// Generator generates improvement suggestions using the Claude CLI.
type Generator struct {
	// Runner is the shared Claude CLI execution runner.
	Runner *llmcli.Runner
}

// suggestionResponse is the expected JSON structure returned by the Claude CLI.
type suggestionResponse struct {
	Suggestions []suggestionJSON `json:"suggestions"`
}

// suggestionJSON is the per-suggestion JSON structure in the Claude CLI response.
type suggestionJSON struct {
	FileType    ContextFileType `json:"file_type"`
	Category    string          `json:"category"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Evidence    []string        `json:"evidence"`
	Priority    string          `json:"priority"`
	Diff        string          `json:"diff"`
}

// GenerateResult holds the suggestions and usage info from a Generate call.
type GenerateResult struct {
	Suggestions []Suggestion
	Usage       *llmcli.UsageInfo
}

// Generate produces context file improvement suggestions.
// analysis contains friction patterns and transcript excerpts.
// contextFiles contains the current context file contents.
func (g *Generator) Generate(ctx context.Context, analysis PatternAnalysis, contextFiles []ContextFile) (*GenerateResult, error) {
	prompt := buildPrompt(analysis, contextFiles)

	raw, usage, err := g.Runner.Execute(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to execute improvement prompt: %w", err)
	}

	var resp suggestionResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse improvement suggestions: %w", err)
	}

	now := time.Now()
	suggestions := make([]Suggestion, 0, len(resp.Suggestions))
	for i, s := range resp.Suggestions {
		suggestions = append(suggestions, Suggestion{
			ID:          fmt.Sprintf("sug-%d-%d", now.Unix(), i),
			FileType:    s.FileType,
			Category:    s.Category,
			Title:       s.Title,
			Description: s.Description,
			Evidence:    s.Evidence,
			Priority:    s.Priority,
			Diff:        s.Diff,
			CreatedAt:   now,
			Status:      "pending",
		})
	}

	return &GenerateResult{Suggestions: suggestions, Usage: usage}, nil
}

// buildPrompt constructs the prompt for the Claude CLI.
// All untrusted content (friction text, learnings, context file content) is wrapped
// in XML tags to prevent prompt injection.
func buildPrompt(analysis PatternAnalysis, contextFiles []ContextFile) string {
	var sb strings.Builder

	sb.WriteString(`Analyze recurring patterns from recent AI coding sessions and suggest
improvements to the project's context files.

`)

	// Repeated friction section
	sb.WriteString("<repeated_friction>\n")
	if len(analysis.RepeatedFriction) == 0 {
		sb.WriteString("(no repeated friction patterns found)\n")
	} else {
		for _, p := range analysis.RepeatedFriction {
			fmt.Fprintf(&sb, "Theme: %s issues (occurred %d times)\n", p.Theme, p.Count)
			for _, ex := range p.Examples {
				fmt.Fprintf(&sb, "  - %q\n", ex)
			}
			if p.TranscriptExcerpt != "" {
				fmt.Fprintf(&sb, "  Excerpt: %q\n", p.TranscriptExcerpt)
			}
		}
	}
	sb.WriteString("</repeated_friction>\n\n")

	// Transcript excerpts section
	sb.WriteString("<transcript_excerpts>\n")
	hasExcerpts := false
	for _, p := range analysis.RepeatedFriction {
		if p.TranscriptExcerpt != "" {
			hasExcerpts = true
			break
		}
	}
	if !hasExcerpts {
		sb.WriteString("(transcript excerpts go here when available — may be empty for dry-run)\n")
	}
	sb.WriteString("</transcript_excerpts>\n\n")

	// Learnings section
	sb.WriteString("<learnings>\n")
	for _, l := range analysis.RepoLearnings {
		fmt.Fprintf(&sb, "Repo: %s\n", l)
	}
	for _, l := range analysis.WorkflowLearnings {
		fmt.Fprintf(&sb, "Workflow: %s\n", l)
	}
	if len(analysis.RepoLearnings) == 0 && len(analysis.WorkflowLearnings) == 0 {
		sb.WriteString("(no learnings recorded)\n")
	}
	sb.WriteString("</learnings>\n\n")

	// Current context files section
	sb.WriteString("<current_context_files>\n")
	for _, cf := range contextFiles {
		if cf.Exists {
			fmt.Fprintf(&sb, "--- %s (%d bytes) ---\n", cf.Type, cf.SizeBytes)
			sb.WriteString(cf.Content)
			sb.WriteString("\n--- end ---\n\n")
		} else {
			fmt.Fprintf(&sb, "--- %s (does not exist) ---\n\n", cf.Type)
		}
	}
	if len(contextFiles) == 0 {
		sb.WriteString("(no context files provided)\n")
	}
	sb.WriteString("</current_context_files>\n\n")

	sb.WriteString(`Return a JSON object with this structure:
{
  "suggestions": [{
    "file_type": "CLAUDE.md",
    "category": "fix_friction|add_rule|add_pattern",
    "title": "Short title",
    "description": "Why this helps",
    "evidence": ["friction quote 1"],
    "priority": "high|medium|low",
    "diff": "unified diff"
  }]
}

Guidelines:
- Focus on REPEATED friction (2+ occurrences)
- Include learnings NOT already in context files
- Each suggestion must have a unified diff
- Priority: high for 3+ occurrences, medium for 2, low for learnings
- Do NOT remove existing content unless it contradicts patterns
`)

	return sb.String()
}
