package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/improve"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
)

func TestSessionRowsToSummaries_CopiesStructuredFacets(t *testing.T) {
	t.Parallel()

	rows := []insightsdb.SessionRow{
		{
			CheckpointID: "cp-1",
			Facets: facets.SessionFacets{
				RepeatedUserInstructions: []facets.RepeatedInstruction{
					{Instruction: "Run golangci-lint before committing"},
				},
				SkillSignals: []facets.SkillSignal{
					{SkillName: "project:go-linting", MissingInstruction: "Warn about trailing nolint comments"},
				},
			},
		},
	}

	got := sessionRowsToSummaries(rows)

	if len(got) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(got))
	}
	if len(got[0].Facets.RepeatedUserInstructions) != 1 {
		t.Fatalf("expected repeated instructions to be copied, got %+v", got[0].Facets)
	}
	if got[0].Facets.SkillSignals[0].SkillName != "project:go-linting" {
		t.Fatalf("unexpected copied skill signal: %+v", got[0].Facets.SkillSignals[0])
	}
}

func TestRenderImproveTerminalDryRun_ShowsStructuredSignals(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	renderImproveTerminalDryRun(&buf, improve.PatternAnalysis{
		RepeatedInstructions: []improve.RecurringSignal{
			{Value: "Run golangci-lint before committing", Count: 2},
		},
		MissingContextSignals: []improve.RecurringSignal{
			{Value: "Repo requires canary after prompt changes", Count: 2},
		},
		SkillOpportunities: []improve.SkillOpportunity{
			{SkillName: "project:go-linting", Count: 2},
		},
		RepeatedFriction: []improve.FrictionPattern{
			{Theme: "lint", Count: 2},
		},
	}, 3, 6)

	out := buf.String()
	for _, want := range []string{
		"Repeated Instructions",
		"Missing Context",
		"Project Skill Updates",
		"Run golangci-lint before committing",
		"project:go-linting",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestRenderImproveTerminal_ShowsRecommendationSections(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	renderImproveTerminal(&buf, improve.ImprovementReport{
		Suggestions: []improve.Suggestion{
			{
				Title:                "Add lint verification reminder",
				TargetKind:           "prompt_recommendation",
				Category:             "missing_context",
				Priority:             "high",
				CopyablePrompt:       "Before finishing, run gofmt and golangci-lint.",
				SuggestedInstruction: "Always verify fmt and lint before claiming completion.",
			},
			{
				Title:                "Tighten go-linting skill",
				TargetKind:           "skill_recommendation",
				SkillName:            "project:go-linting",
				Category:             "skill_gap",
				Priority:             "high",
				SuggestedInstruction: "Warn about trailing nolint comments on signatures.",
			},
		},
	})

	out := buf.String()
	for _, want := range []string{
		"Top Recommendations",
		"Prompt Changes",
		"Project Skill Updates",
		"Add lint verification reminder",
		"Tighten go-linting skill",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}
}
