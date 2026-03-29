package facets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
// knownSkillNames is an optional list of canonical skill names to guide extraction.
func (e *Extractor) Extract(ctx context.Context, transcriptText string, knownSkillNames ...[]string) (*SessionFacets, *llmcli.UsageInfo, error) {
	if e.Runner == nil {
		e.Runner = &llmcli.Runner{}
	}

	var prompt string
	if len(knownSkillNames) > 0 && len(knownSkillNames[0]) > 0 {
		prompt = BuildPromptWithSkills(transcriptText, knownSkillNames[0])
	} else {
		prompt = BuildPrompt(transcriptText)
	}

	raw, usage, err := e.Runner.Execute(ctx, prompt)
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

// BuildPromptWithSkills constructs the extraction prompt with a list of known skill names
// so the LLM uses canonical names instead of inventing its own.
func BuildPromptWithSkills(transcriptText string, skillNames []string) string {
	skillList := strings.Join(skillNames, ", ")
	return fmt.Sprintf(`Analyze this development session transcript and extract structured facets.

<transcript>
%s
</transcript>

<known_skills>
The following are the canonical skill names in this project: %s
When extracting skill_signals, use these exact names. If a skill in the transcript
matches one of these (even as a sub-skill like "e2e:triage" for "e2e"), use the
canonical name. Only use a non-canonical name if the skill clearly does not match
any of the known skills.
</known_skills>

Return a JSON object with this exact structure:
{
  "repeated_user_instructions": [{"instruction": "instruction text", "evidence": ["short quote"]}],
  "missing_context": [{"item": "missing rule or repo fact", "evidence": ["short quote"]}],
  "failure_loops": [{"description": "repeat failure pattern", "count": 2, "evidence": ["short quote"]}],
  "skill_signals": [{
    "skill_name": "skill identifier (use canonical name from known_skills when possible)",
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
- For skill_signals, prefer canonical names from the known_skills list
- Return ONLY the JSON object`, transcriptText, skillList)
}
