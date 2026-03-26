package facets

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/entireio/cli/cmd/entire/cli/llmcli"
)

// RepeatedInstruction captures an instruction the user had to restate.
type RepeatedInstruction struct {
	Instruction string   `json:"instruction"`
	Evidence    []string `json:"evidence,omitempty"`
}

// MissingContextSignal captures a repo or workflow rule the agent likely lacked.
type MissingContextSignal struct {
	Item     string   `json:"item"`
	Evidence []string `json:"evidence,omitempty"`
}

// FailureLoop captures a repeated failure/retry pattern within a session.
type FailureLoop struct {
	Description string   `json:"description"`
	Count       int      `json:"count"`
	Evidence    []string `json:"evidence,omitempty"`
}

// SkillSignal captures friction tied to a specific skill invocation.
type SkillSignal struct {
	SkillName          string   `json:"skill_name"`
	SkillPath          string   `json:"skill_path,omitempty"`
	Friction           []string `json:"friction,omitempty"`
	MissingInstruction string   `json:"missing_instruction,omitempty"`
}

// SessionFacets is the structured output extracted from a single session.
type SessionFacets struct {
	RepeatedUserInstructions []RepeatedInstruction  `json:"repeated_user_instructions,omitempty"`
	MissingContext           []MissingContextSignal `json:"missing_context,omitempty"`
	FailureLoops             []FailureLoop          `json:"failure_loops,omitempty"`
	SkillSignals             []SkillSignal          `json:"skill_signals,omitempty"`
	RepoGotchas              []string               `json:"repo_gotchas,omitempty"`
	WorkflowGaps             []string               `json:"workflow_gaps,omitempty"`
}

// Extractor extracts session facets using the shared LLM CLI runner.
type Extractor struct {
	Runner *llmcli.Runner
}

// Extract builds a facet prompt from transcript text and parses the JSON response.
func (e *Extractor) Extract(ctx context.Context, transcriptText string) (*SessionFacets, *llmcli.UsageInfo, error) {
	if e.Runner == nil {
		e.Runner = &llmcli.Runner{}
	}

	raw, usage, err := e.Runner.Execute(ctx, BuildPrompt(transcriptText))
	if err != nil {
		return nil, nil, fmt.Errorf("execute facets prompt: %w", err)
	}

	var result SessionFacets
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, nil, fmt.Errorf("parse facets JSON: %w", err)
	}

	return &result, usage, nil
}

// BuildPrompt constructs the extraction prompt.
func BuildPrompt(transcriptText string) string {
	return fmt.Sprintf(`Analyze this development session transcript and extract structured facets.

<transcript>
%s
</transcript>

Return a JSON object with this exact structure:
{
  "repeated_user_instructions": [{"instruction": "instruction text", "evidence": ["short quote"]}],
  "missing_context": [{"item": "missing rule or repo fact", "evidence": ["short quote"]}],
  "failure_loops": [{"description": "repeat failure pattern", "count": 2, "evidence": ["short quote"]}],
  "skill_signals": [{
    "skill_name": "skill identifier",
    "skill_path": "optional/path/to/SKILL.md",
    "friction": ["what went wrong after using the skill"],
    "missing_instruction": "what the skill should add next time"
  }],
  "repo_gotchas": ["repo-specific gotcha"],
  "workflow_gaps": ["workflow gap or missing step"]
}

Guidelines:
- Focus on actionable repeated signals, not full summaries
- Prefer short evidence snippets
- Use empty arrays when a category is absent
- Return ONLY the JSON object`, transcriptText)
}
