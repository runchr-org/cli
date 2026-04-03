package improve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
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
	TargetKind           string          `json:"target_kind"`
	FileType             ContextFileType `json:"file_type"`
	FilePath             string          `json:"file_path"`
	SkillName            string          `json:"skill_name"`
	Category             string          `json:"category"`
	Title                string          `json:"title"`
	Description          string          `json:"description"`
	Evidence             []string        `json:"evidence"`
	Priority             string          `json:"priority"`
	CopyablePrompt       string          `json:"copyable_prompt"`
	SuggestedInstruction string          `json:"suggested_instruction"`
	Diff                 string          `json:"diff"`
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

	if g.Runner == nil {
		g.Runner = &llmcli.Runner{}
	}

	raw, usage, err := g.Runner.Execute(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to execute improvement prompt: %w", err)
	}

	var resp suggestionResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		if extracted := jsonutil.ExtractJSONObject(raw); extracted != "" {
			if err2 := json.Unmarshal([]byte(extracted), &resp); err2 != nil {
				return nil, fmt.Errorf("failed to parse improvement suggestions: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to parse improvement suggestions: %w", err)
		}
	}

	now := time.Now()
	suggestions := make([]Suggestion, 0, len(resp.Suggestions))
	for i, s := range resp.Suggestions {
		suggestions = append(suggestions, Suggestion{
			ID:                   fmt.Sprintf("sug-%d-%d", now.Unix(), i),
			TargetKind:           s.TargetKind,
			FileType:             s.FileType,
			FilePath:             s.FilePath,
			SkillName:            s.SkillName,
			Category:             s.Category,
			Title:                s.Title,
			Description:          s.Description,
			Evidence:             s.Evidence,
			Priority:             s.Priority,
			CopyablePrompt:       s.CopyablePrompt,
			SuggestedInstruction: s.SuggestedInstruction,
			Diff:                 s.Diff,
			CreatedAt:            now,
			Status:               "pending",
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
improvements to prompts, repo instructions, and project skills.

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

	sb.WriteString("<repeated_instructions>\n")
	if len(analysis.RepeatedInstructions) == 0 {
		sb.WriteString("(no repeated user instructions found)\n")
	} else {
		for _, signal := range analysis.RepeatedInstructions {
			fmt.Fprintf(&sb, "Instruction: %s (occurred %d times)\n", signal.Value, signal.Count)
			for _, evidence := range signal.Evidence {
				fmt.Fprintf(&sb, "  - %q\n", evidence)
			}
		}
	}
	sb.WriteString("</repeated_instructions>\n\n")

	sb.WriteString("<missing_context>\n")
	if len(analysis.MissingContextSignals) == 0 {
		sb.WriteString("(no missing context patterns found)\n")
	} else {
		for _, signal := range analysis.MissingContextSignals {
			fmt.Fprintf(&sb, "Missing: %s (occurred %d times)\n", signal.Value, signal.Count)
			for _, evidence := range signal.Evidence {
				fmt.Fprintf(&sb, "  - %q\n", evidence)
			}
		}
	}
	sb.WriteString("</missing_context>\n\n")

	sb.WriteString("<failure_loops>\n")
	if len(analysis.FailureLoops) == 0 {
		sb.WriteString("(no failure loops found)\n")
	} else {
		for _, signal := range analysis.FailureLoops {
			fmt.Fprintf(&sb, "Loop: %s (score %d)\n", signal.Value, signal.Count)
			for _, evidence := range signal.Evidence {
				fmt.Fprintf(&sb, "  - %q\n", evidence)
			}
		}
	}
	sb.WriteString("</failure_loops>\n\n")

	sb.WriteString("<skill_opportunities>\n")
	if len(analysis.SkillOpportunities) == 0 {
		sb.WriteString("(no skill-related opportunities found)\n")
	} else {
		for _, skill := range analysis.SkillOpportunities {
			fmt.Fprintf(&sb, "Skill: %s\n", skill.SkillName)
			if skill.SkillPath != "" {
				fmt.Fprintf(&sb, "  Path: %s\n", skill.SkillPath)
			}
			fmt.Fprintf(&sb, "  Count: %d\n", skill.Count)
			if skill.MissingInstruction != "" {
				fmt.Fprintf(&sb, "  Missing instruction: %s\n", skill.MissingInstruction)
			}
			for _, friction := range skill.Friction {
				fmt.Fprintf(&sb, "  Friction: %q\n", friction)
			}
		}
	}
	sb.WriteString("</skill_opportunities>\n\n")

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
    "target_kind": "prompt_recommendation|skill_recommendation|workflow_recommendation",
    "file_type": "CLAUDE.md",
    "file_path": "/abs/path/to/file",
    "skill_name": "optional skill name",
    "category": "missing_context|skill_gap|workflow_gap|fix_friction",
    "title": "Short title",
    "description": "Why this helps",
    "evidence": ["friction quote 1"],
    "priority": "high|medium|low",
    "copyable_prompt": "Optional prompt text the developer can reuse",
    "suggested_instruction": "Optional instruction block or skill change",
    "diff": "Optional unified diff"
  }]
}

Guidelines:
- Focus on repeated structured signals, not one-off complaints
- Prefer prompt recommendations and skill updates over raw diagnostics
- Include concrete wording the developer can copy into prompts or skill files
- Unified diffs are optional; only include them when clearly helpful
- Priority: high for 3+ occurrences, medium for 2, low for learnings
`)

	return sb.String()
}
