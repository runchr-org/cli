package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/llmcli"
)

func TestAnalyzeCommands_BuildsCandidatesAcrossAgents(t *testing.T) {
	t.Parallel()

	input := AnalyzeCommands([]ObservedCommand{
		{Agent: "Claude Code", CheckpointID: "cp-1", SessionID: "s-1", Command: "mise run test"},
		{Agent: "Claude Code", CheckpointID: "cp-2", SessionID: "s-2", Command: "mise run test TestPermissions"},
		{Agent: "Claude Code", CheckpointID: "cp-3", SessionID: "s-3", Command: "mise run fmt"},
		{Agent: "Gemini CLI", CheckpointID: "cp-4", SessionID: "s-4", Command: "go test ./..."},
		{Agent: "Gemini CLI", CheckpointID: "cp-5", SessionID: "s-5", Command: "go test ./..."},
	}, RepoFacts{
		MiseTasks: map[string]TaskDefinition{
			"test": {Name: "test", SourcePath: "mise.toml", Content: "go test ./..."},
			"fmt":  {Name: "fmt", SourcePath: "mise.toml", Content: "gofmt -s -w ."},
		},
	}, []string{"Claude Code", "Gemini CLI"})

	if input.SessionsAnalyzed != 5 {
		t.Fatalf("expected 5 analyzed sessions, got %d", input.SessionsAnalyzed)
	}
	if input.CommandsObserved != 5 {
		t.Fatalf("expected 5 observed commands, got %d", input.CommandsObserved)
	}
	if len(input.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(input.Agents))
	}

	if input.Agents[0].Agent != "Claude Code" {
		t.Fatalf("unexpected first agent: %+v", input.Agents[0])
	}
	if len(input.Agents[0].Candidates) != 1 {
		t.Fatalf("expected 1 Claude candidate, got %+v", input.Agents[0].Candidates)
	}
	if input.Agents[0].Candidates[0].Rule != "mise run test" {
		t.Fatalf("expected normalized mise candidate, got %+v", input.Agents[0].Candidates[0])
	}

	if input.Agents[1].Agent != "Gemini CLI" {
		t.Fatalf("unexpected second agent: %+v", input.Agents[1])
	}
	if len(input.Agents[1].Candidates) != 1 || input.Agents[1].Candidates[0].Rule != "go test ./..." {
		t.Fatalf("unexpected Gemini candidates: %+v", input.Agents[1].Candidates)
	}
}

func TestReviewer_Review_UsesClaudeSelection(t *testing.T) {
	t.Parallel()

	response := reviewerCLIResponse(`{
		"agents": [
			{
				"agent": "Claude Code",
				"suggestions": [
					{
						"rule": "mise run test",
						"reason": "The repo task runs unit tests only and does not mutate tracked files."
					}
				]
			},
			{
				"agent": "Gemini CLI",
				"suggestions": []
			}
		]
	}`)

	reviewer := &Reviewer{
		Runner: &llmcli.Runner{
			CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+response+"'")
			},
		},
	}

	report, _, err := reviewer.Review(context.Background(), ReviewInput{
		SessionsAnalyzed: 4,
		CommandsObserved: 4,
		Agents: []AgentReview{
			{
				Agent:            "Claude Code",
				SessionsAnalyzed: 2,
				Candidates: []Candidate{
					{
						Rule:       "mise run test",
						Count:      2,
						SessionIDs: []string{"s-1", "s-2"},
						Examples:   []string{"mise run test", "mise run test TestPermissions"},
					},
					{
						Rule:       "mise run fmt",
						Count:      2,
						SessionIDs: []string{"s-3", "s-4"},
						Examples:   []string{"mise run fmt"},
					},
				},
			},
			{
				Agent:            "Gemini CLI",
				SessionsAnalyzed: 2,
				Candidates: []Candidate{
					{
						Rule:       "go test ./...",
						Count:      2,
						SessionIDs: []string{"s-5", "s-6"},
						Examples:   []string{"go test ./..."},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Agents) != 2 {
		t.Fatalf("expected 2 agent reports, got %d", len(report.Agents))
	}
	if len(report.Agents[0].Suggestions) != 1 {
		t.Fatalf("expected 1 Claude suggestion, got %+v", report.Agents[0].Suggestions)
	}
	got := report.Agents[0].Suggestions[0]
	if got.Rule != "mise run test" {
		t.Fatalf("unexpected suggested rule: %+v", got)
	}
	if got.Count != 2 {
		t.Fatalf("expected count 2, got %+v", got)
	}
	if !slices.Equal(got.SessionIDs, []string{"s-1", "s-2"}) {
		t.Fatalf("unexpected session IDs: %+v", got.SessionIDs)
	}
	if got.Reason == "" {
		t.Fatalf("expected Claude reason, got %+v", got)
	}
	if len(report.Agents[1].Suggestions) != 0 {
		t.Fatalf("expected Gemini suggestions to be empty, got %+v", report.Agents[1].Suggestions)
	}
}

func TestExtractShellCommands_FindsOnlyShellToolEntries(t *testing.T) {
	t.Parallel()

	commands := ExtractShellCommands([]TranscriptToolUse{
		{ToolName: "Bash", Detail: "mise run test"},
		{ToolName: "Read", Detail: "AGENTS.md"},
		{ToolName: "bash", Detail: "go test ./..."},
		{ToolName: "run_command", Detail: "go test -tags=integration ./cmd/entire/cli/integration_test/..."},
	})

	want := []string{
		"mise run test",
		"go test ./...",
		"go test -tags=integration ./cmd/entire/cli/integration_test/...",
	}
	if !slices.Equal(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func reviewerCLIResponse(inner string) string {
	b, err := json.Marshal(inner)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal result: %v", err))
	}
	return fmt.Sprintf(`{"result":%s}`, string(b))
}
