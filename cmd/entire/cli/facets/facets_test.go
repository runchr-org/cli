package facets_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
)

func buildCLIResponse(result string) string {
	b, err := json.Marshal(result)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal result: %v", err))
	}
	return fmt.Sprintf(`{"result":%s}`, string(b))
}

func TestExtractor_Extract_ReturnsStructuredFacets(t *testing.T) {
	t.Parallel()

	inner := `{
		"repeated_user_instructions": [
			{"instruction": "Run golangci-lint before committing", "evidence": ["User repeated lint expectation"]}
		],
		"missing_context": [
			{"item": "Repo requires gofmt to preserve nolint placement", "evidence": ["Agent missed repo-specific formatting rule"]}
		],
		"failure_loops": [
			{"description": "Lint fix was applied and re-broken after fmt", "count": 2, "evidence": ["The same lint issue returned after formatting"]}
		],
		"skill_signals": [
			{
				"skill_name": "project:go-linting",
				"skill_path": ".codex/skills/go-linting/SKILL.md",
				"friction": ["Skill did not mention gofmt removing inline nolint comments"],
				"missing_instruction": "Add a warning about trailing nolint comments on function signatures"
			}
		],
		"repo_gotchas": ["Go 1.26 gofmt strips trailing //nolint comments on signatures"],
		"workflow_gaps": ["Agent should run fmt and lint as a paired verification step"]
	}`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse(inner)
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	extractor := &facets.Extractor{Runner: runner}

	got, _, err := extractor.Extract(context.Background(), "session transcript")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got.RepeatedUserInstructions) != 1 {
		t.Fatalf("expected 1 repeated instruction, got %d", len(got.RepeatedUserInstructions))
	}
	if got.RepeatedUserInstructions[0].Instruction != "Run golangci-lint before committing" {
		t.Fatalf("unexpected instruction: %q", got.RepeatedUserInstructions[0].Instruction)
	}
	if len(got.MissingContext) != 1 || got.MissingContext[0].Item == "" {
		t.Fatalf("expected missing context signal, got %+v", got.MissingContext)
	}
	if len(got.FailureLoops) != 1 || got.FailureLoops[0].Count != 2 {
		t.Fatalf("expected one failure loop with count 2, got %+v", got.FailureLoops)
	}
	if len(got.SkillSignals) != 1 {
		t.Fatalf("expected 1 skill signal, got %d", len(got.SkillSignals))
	}
	if got.SkillSignals[0].SkillName != "project:go-linting" {
		t.Fatalf("unexpected skill name: %q", got.SkillSignals[0].SkillName)
	}
}

func TestExtractor_Extract_InvalidJSON(t *testing.T) {
	t.Parallel()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse("not valid json")
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	extractor := &facets.Extractor{Runner: runner}

	if _, _, err := extractor.Extract(context.Background(), "session transcript"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBuildPrompt_IncludesFacetSchema(t *testing.T) {
	t.Parallel()

	prompt := facets.BuildPrompt("Skill: project:go-linting")

	for _, want := range []string{
		"<transcript>",
		"repeated_user_instructions",
		"missing_context",
		"failure_loops",
		"skill_signals",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildPromptWithSkills_IncludesKnownSkills(t *testing.T) {
	t.Parallel()

	prompt := facets.BuildPromptWithSkills("some transcript", []string{"e2e", "dev", "reviewer"})

	for _, want := range []string{
		"<transcript>",
		"<known_skills>",
		"e2e, dev, reviewer",
		"canonical skill names",
		"skill_signals",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestBuildPromptWithSkills_IncludesTranscript(t *testing.T) {
	t.Parallel()

	prompt := facets.BuildPromptWithSkills("my test transcript content", []string{"e2e"})

	if !strings.Contains(prompt, "my test transcript content") {
		t.Fatal("prompt missing transcript content")
	}
}
