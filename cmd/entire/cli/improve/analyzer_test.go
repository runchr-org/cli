package improve_test

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/improve"
)

func TestAnalyzePatterns_EmptySummaries(t *testing.T) {
	t.Parallel()

	result := improve.AnalyzePatterns(nil)

	if result.SessionCount != 0 {
		t.Errorf("expected SessionCount=0, got %d", result.SessionCount)
	}
	if len(result.RepeatedFriction) != 0 {
		t.Errorf("expected no repeated friction, got %d", len(result.RepeatedFriction))
	}
	if len(result.RepoLearnings) != 0 {
		t.Errorf("expected no repo learnings, got %d", len(result.RepoLearnings))
	}
}

func TestAnalyzePatterns_SingleSession(t *testing.T) {
	t.Parallel()

	summaries := []improve.SessionSummaryData{
		{
			CheckpointID: "abc123",
			Friction:     []string{"Lint errors not caught by agent"},
			Learnings: []improve.LearningEntry{
				{Scope: "repo", Finding: "Uses golangci-lint"},
			},
		},
	}

	result := improve.AnalyzePatterns(summaries)

	if result.SessionCount != 1 {
		t.Errorf("expected SessionCount=1, got %d", result.SessionCount)
	}
	// Single occurrence should NOT be repeated friction
	if len(result.RepeatedFriction) != 0 {
		t.Errorf("expected no repeated friction from single session, got %d", len(result.RepeatedFriction))
	}
	if len(result.RepoLearnings) != 1 {
		t.Errorf("expected 1 repo learning, got %d", len(result.RepoLearnings))
	}
}

func TestAnalyzePatterns_RepeatedFrictionGroupsByTheme(t *testing.T) {
	t.Parallel()

	summaries := []improve.SessionSummaryData{
		{
			CheckpointID: "aaa111",
			Friction:     []string{"Lint errors not caught", "Had to fix golangci-lint errors manually"},
		},
		{
			CheckpointID: "bbb222",
			Friction:     []string{"lint check failed again"},
		},
	}

	result := improve.AnalyzePatterns(summaries)

	if result.SessionCount != 2 {
		t.Errorf("expected SessionCount=2, got %d", result.SessionCount)
	}

	// All three should group under "lint" theme
	if len(result.RepeatedFriction) == 0 {
		t.Fatal("expected at least one repeated friction pattern")
	}

	var lintPattern *improve.FrictionPattern
	for i := range result.RepeatedFriction {
		if result.RepeatedFriction[i].Theme == "lint" {
			lintPattern = &result.RepeatedFriction[i]
			break
		}
	}
	if lintPattern == nil {
		t.Fatal("expected lint theme in repeated friction")
	}
	if lintPattern.Count != 3 {
		t.Errorf("expected lint count=3, got %d", lintPattern.Count)
	}
	if len(lintPattern.Examples) != 3 {
		t.Errorf("expected 3 examples, got %d", len(lintPattern.Examples))
	}
	if len(lintPattern.AffectedSessions) != 2 {
		t.Errorf("expected 2 affected sessions, got %d", len(lintPattern.AffectedSessions))
	}
}

func TestAnalyzePatterns_FrictionThresholdIsTwo(t *testing.T) {
	t.Parallel()

	summaries := []improve.SessionSummaryData{
		{
			CheckpointID: "s1",
			Friction:     []string{"test runner timeout once"},
		},
		{
			CheckpointID: "s2",
			Friction:     []string{"test runner timeout again"},
		},
	}

	result := improve.AnalyzePatterns(summaries)

	// "test" theme appears 2 times — must show up as repeated
	found := false
	for _, p := range result.RepeatedFriction {
		if p.Theme == "test" {
			found = true
			if p.Count < 2 {
				t.Errorf("expected count >= 2 for test theme, got %d", p.Count)
			}
		}
	}
	if !found {
		t.Error("expected 'test' theme with 2 occurrences to appear in repeated friction")
	}
}

func TestAnalyzePatterns_DeduplicatesLearnings(t *testing.T) {
	t.Parallel()

	summaries := []improve.SessionSummaryData{
		{
			CheckpointID: "s1",
			Learnings: []improve.LearningEntry{
				{Scope: "repo", Finding: "Uses golangci-lint"},
				{Scope: "workflow", Finding: "Run tests before committing"},
			},
		},
		{
			CheckpointID: "s2",
			Learnings: []improve.LearningEntry{
				{Scope: "repo", Finding: "Uses golangci-lint"}, // duplicate
				{Scope: "workflow", Finding: "Push to feature branch"},
			},
		},
	}

	result := improve.AnalyzePatterns(summaries)

	// "Uses golangci-lint" should appear only once
	repoCount := 0
	for _, l := range result.RepoLearnings {
		if l == "Uses golangci-lint" {
			repoCount++
		}
	}
	if repoCount != 1 {
		t.Errorf("expected deduplicated repo learning count=1, got %d", repoCount)
	}

	if len(result.WorkflowLearnings) != 2 {
		t.Errorf("expected 2 workflow learnings, got %d", len(result.WorkflowLearnings))
	}
}

func TestAnalyzePatterns_KnownThemes(t *testing.T) {
	t.Parallel()

	// Verify each known theme keyword is recognized
	tests := []struct {
		frictionText  string
		expectedTheme string
	}{
		{"import cycle detected", "import"},
		{"compile error in main.go", "compile"},
		{"format check failed", "format"},
		{"permission denied reading file", "permission"},
		{"request timeout after 30s", "timeout"},
		{"retry attempt 3 failed", "retry"},
		{"type assertion failed", "type"},
	}

	for _, tt := range tests {
		t.Run(tt.expectedTheme, func(t *testing.T) {
			t.Parallel()

			// Need 2+ to trigger repeated friction
			summaries := []improve.SessionSummaryData{
				{CheckpointID: "s1", Friction: []string{tt.frictionText}},
				{CheckpointID: "s2", Friction: []string{tt.frictionText + " again"}},
			}

			result := improve.AnalyzePatterns(summaries)

			found := false
			for _, p := range result.RepeatedFriction {
				if p.Theme == tt.expectedTheme {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected theme %q for friction %q", tt.expectedTheme, tt.frictionText)
			}
		})
	}
}

func TestAnalyzePatterns_OpenItems(t *testing.T) {
	t.Parallel()

	summaries := []improve.SessionSummaryData{
		{
			CheckpointID: "s1",
			OpenItems:    []string{"TODO: add authentication", "Fix flaky test"},
		},
		{
			CheckpointID: "s2",
			OpenItems:    []string{"TODO: add authentication"}, // duplicate
		},
	}

	result := improve.AnalyzePatterns(summaries)

	// Deduplication: "TODO: add authentication" appears once, "Fix flaky test" once
	if len(result.OpenItems) != 2 {
		t.Errorf("expected 2 deduplicated open items, got %d: %v", len(result.OpenItems), result.OpenItems)
	}
}

func TestAnalyzePatterns_MultipleFrictionThemes(t *testing.T) {
	t.Parallel()

	summaries := []improve.SessionSummaryData{
		{
			CheckpointID: "s1",
			Friction:     []string{"lint error", "test failure"},
		},
		{
			CheckpointID: "s2",
			Friction:     []string{"lint warning", "import error"},
		},
	}

	result := improve.AnalyzePatterns(summaries)

	themeSet := make(map[string]bool)
	for _, p := range result.RepeatedFriction {
		themeSet[p.Theme] = true
	}

	// "lint" appears twice (threshold=2), "test" and "import" appear once
	if !themeSet["lint"] {
		t.Error("expected lint in repeated friction themes")
	}
	if themeSet["test"] {
		t.Error("test appears once only, should not be repeated")
	}
	if themeSet["import"] {
		t.Error("import appears once only, should not be repeated")
	}
}

func TestAnalyzePatterns_UsesStructuredFacetsForRecurringSignals(t *testing.T) {
	t.Parallel()

	summaries := []improve.SessionSummaryData{
		{
			CheckpointID: "s1",
			Facets: facets.SessionFacets{
				RepeatedUserInstructions: []facets.RepeatedInstruction{
					{Instruction: "Run golangci-lint before committing", Evidence: []string{"User repeated lint expectation"}},
				},
				MissingContext: []facets.MissingContextSignal{
					{Item: "Repo requires gofmt after edits", Evidence: []string{"Agent skipped fmt"}},
				},
				FailureLoops: []facets.FailureLoop{
					{Description: "Lint issue returned after fmt", Count: 2, Evidence: []string{"reappeared after formatting"}},
				},
			},
		},
		{
			CheckpointID: "s2",
			Facets: facets.SessionFacets{
				RepeatedUserInstructions: []facets.RepeatedInstruction{
					{Instruction: "Run golangci-lint before committing", Evidence: []string{"Lint request repeated"}},
				},
				MissingContext: []facets.MissingContextSignal{
					{Item: "Repo requires gofmt after edits", Evidence: []string{"Formatting missed again"}},
				},
				FailureLoops: []facets.FailureLoop{
					{Description: "Lint issue returned after fmt", Count: 2, Evidence: []string{"same loop happened again"}},
				},
			},
		},
	}

	result := improve.AnalyzePatterns(summaries)

	if len(result.RepeatedInstructions) != 1 {
		t.Fatalf("expected 1 repeated instruction, got %d", len(result.RepeatedInstructions))
	}
	if result.RepeatedInstructions[0].Value != "Run golangci-lint before committing" {
		t.Fatalf("unexpected instruction value: %q", result.RepeatedInstructions[0].Value)
	}
	if len(result.MissingContextSignals) != 1 {
		t.Fatalf("expected 1 missing context signal, got %d", len(result.MissingContextSignals))
	}
	if len(result.FailureLoops) != 1 {
		t.Fatalf("expected 1 failure loop, got %d", len(result.FailureLoops))
	}
}

func TestAnalyzePatterns_CreatesSkillOpportunities(t *testing.T) {
	t.Parallel()

	summaries := []improve.SessionSummaryData{
		{
			CheckpointID: "s1",
			Facets: facets.SessionFacets{
				SkillSignals: []facets.SkillSignal{
					{
						SkillName:          "project:go-linting",
						SkillPath:          ".codex/skills/go-linting/SKILL.md",
						Friction:           []string{"Skill did not warn about gofmt removing inline nolint comments"},
						MissingInstruction: "Warn about trailing nolint comments on function signatures",
					},
				},
			},
		},
		{
			CheckpointID: "s2",
			Facets: facets.SessionFacets{
				SkillSignals: []facets.SkillSignal{
					{
						SkillName:          "project:go-linting",
						SkillPath:          ".codex/skills/go-linting/SKILL.md",
						Friction:           []string{"Same lint issue came back after following the skill"},
						MissingInstruction: "Warn about trailing nolint comments on function signatures",
					},
				},
			},
		},
	}

	result := improve.AnalyzePatterns(summaries)

	if len(result.SkillOpportunities) != 1 {
		t.Fatalf("expected 1 skill opportunity, got %d", len(result.SkillOpportunities))
	}
	if result.SkillOpportunities[0].SkillName != "project:go-linting" {
		t.Fatalf("unexpected skill opportunity: %+v", result.SkillOpportunities[0])
	}
	if result.SkillOpportunities[0].Count != 2 {
		t.Fatalf("expected skill opportunity count 2, got %d", result.SkillOpportunities[0].Count)
	}
}
