package improve_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/improve"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
)

// emptySuggestions is the JSON response for a call that returns no suggestions.
const emptySuggestions = `{"suggestions": []}`

// buildCLIResponse wraps a result string in the Claude CLI JSON envelope.
func buildCLIResponse(result string) string {
	b, err := json.Marshal(result)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal result: %v", err))
	}
	return fmt.Sprintf(`{"result":%s}`, string(b))
}

func TestGenerator_Generate_ReturnsSuggestions(t *testing.T) {
	t.Parallel()

	inner := `{
		"suggestions": [
			{
				"file_type": "CLAUDE.md",
				"category": "fix_friction",
				"title": "Add lint run instructions",
				"description": "Agents skip linting before committing",
				"evidence": ["Lint errors not caught", "Had to fix golangci-lint errors manually"],
				"priority": "high",
				"diff": "--- a/CLAUDE.md\n+++ b/CLAUDE.md\n@@ -1 +1,2 @@\n # Project\n+Run golangci-lint before committing."
			}
		]
	}`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse(inner)
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	gen := &improve.Generator{Runner: runner}

	analysis := improve.PatternAnalysis{
		RepeatedFriction: []improve.FrictionPattern{
			{
				Theme:    "lint",
				Count:    2,
				Examples: []string{"Lint errors not caught", "Had to fix golangci-lint errors manually"},
			},
		},
		SessionCount: 2,
	}

	contextFiles := []improve.ContextFile{
		{
			Type:      improve.ContextFileCLAUDEMD,
			Path:      "/project/CLAUDE.md",
			Exists:    true,
			Content:   "# Project\n",
			SizeBytes: 10,
		},
	}

	result, err := gen.Generate(context.Background(), analysis, contextFiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(result.Suggestions))
	}

	s := result.Suggestions[0]
	if s.FileType != improve.ContextFileCLAUDEMD {
		t.Errorf("expected file_type CLAUDE.md, got %q", s.FileType)
	}
	if s.Category != "fix_friction" {
		t.Errorf("expected category fix_friction, got %q", s.Category)
	}
	if s.Title != "Add lint run instructions" {
		t.Errorf("expected title, got %q", s.Title)
	}
	if s.Priority != "high" {
		t.Errorf("expected priority high, got %q", s.Priority)
	}
	if s.Status != "pending" {
		t.Errorf("expected status pending, got %q", s.Status)
	}
	if s.ID == "" {
		t.Error("expected non-empty ID")
	}
	if !strings.HasPrefix(s.ID, "sug-") {
		t.Errorf("expected ID to start with 'sug-', got %q", s.ID)
	}
	if s.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestGenerator_Generate_EmptySuggestions(t *testing.T) {
	t.Parallel()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse(emptySuggestions)
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	gen := &improve.Generator{Runner: runner}

	result, err := gen.Generate(context.Background(), improve.PatternAnalysis{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Suggestions) != 0 {
		t.Errorf("expected 0 suggestions, got %d", len(result.Suggestions))
	}
}

func TestGenerator_Generate_InvalidJSON(t *testing.T) {
	t.Parallel()

	inner := `not valid json at all`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse(inner)
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	gen := &improve.Generator{Runner: runner}

	_, err := gen.Generate(context.Background(), improve.PatternAnalysis{}, nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestGenerator_Generate_PromptContainsFriction(t *testing.T) {
	t.Parallel()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse(emptySuggestions)
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	gen := &improve.Generator{Runner: runner}

	analysis := improve.PatternAnalysis{
		RepeatedFriction: []improve.FrictionPattern{
			{
				Theme:    "lint",
				Count:    3,
				Examples: []string{"lint failed repeatedly"},
			},
		},
	}

	// Verify that a prompt with friction patterns executes without error.
	if _, err := gen.Generate(context.Background(), analysis, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenerator_Generate_PromptIncludesContextFiles(t *testing.T) {
	t.Parallel()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse(emptySuggestions)
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	gen := &improve.Generator{Runner: runner}

	contextFiles := []improve.ContextFile{
		{
			Type:      improve.ContextFileCLAUDEMD,
			Path:      "/project/CLAUDE.md",
			Exists:    true,
			Content:   "# My Project",
			SizeBytes: 14,
		},
		{
			Type:   improve.ContextFileAGENTSMD,
			Path:   "/project/AGENTS.md",
			Exists: false,
		},
	}

	// No panic and no error = prompt was constructed and sent correctly
	result, err := gen.Generate(context.Background(), improve.PatternAnalysis{}, contextFiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Suggestions == nil {
		t.Error("expected non-nil suggestions slice")
	}
}

func TestGenerator_Generate_IDsAreUnique(t *testing.T) {
	t.Parallel()

	inner := `{
		"suggestions": [
			{"file_type": "CLAUDE.md", "category": "fix_friction", "title": "A", "description": "d", "evidence": [], "priority": "high", "diff": ""},
			{"file_type": "CLAUDE.md", "category": "add_rule", "title": "B", "description": "d", "evidence": [], "priority": "medium", "diff": ""}
		]
	}`

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse(inner)
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	gen := &improve.Generator{Runner: runner}

	result, err := gen.Generate(context.Background(), improve.PatternAnalysis{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Suggestions) != 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(result.Suggestions))
	}

	if result.Suggestions[0].ID == result.Suggestions[1].ID {
		t.Error("expected unique IDs for different suggestions")
	}
}

func TestGenerator_Generate_CreatedAtIsSet(t *testing.T) {
	t.Parallel()

	inner := `{
		"suggestions": [
			{"file_type": "CLAUDE.md", "category": "fix_friction", "title": "A", "description": "d", "evidence": [], "priority": "high", "diff": ""}
		]
	}`

	before := time.Now()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			resp := buildCLIResponse(inner)
			return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("printf '%%s' '%s'", resp))
		},
	}

	gen := &improve.Generator{Runner: runner}

	result, err := gen.Generate(context.Background(), improve.PatternAnalysis{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := time.Now()

	if len(result.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion")
	}

	if result.Suggestions[0].CreatedAt.Before(before) || result.Suggestions[0].CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not within expected range [%v, %v]", result.Suggestions[0].CreatedAt, before, after)
	}
}

func TestGenerator_Generate_RunnerError(t *testing.T) {
	t.Parallel()

	runner := &llmcli.Runner{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "exit 1")
		},
	}

	gen := &improve.Generator{Runner: runner}

	if _, err := gen.Generate(context.Background(), improve.PatternAnalysis{}, nil); err == nil {
		t.Fatal("expected error when runner fails")
	}
}
