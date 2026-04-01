package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

func TestHandleSearchMemories_EmptyQuery_ReturnsAllActive(t *testing.T) {
	setupTestRepo(t)
	records := []memoryloop.MemoryRecord{
		{ID: "a1", Title: "Active rule", Body: "body active", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusActive, Strength: 5},
		{ID: "c1", Title: "Candidate rule", Body: "body candidate", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusCandidate, Strength: 3},
		{ID: "s1", Title: "Suppressed rule", Body: "body suppressed", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusSuppressed, Strength: 2},
	}
	setupTestState(t, memoryloop.ModeAuto, records)

	req := mcp.CallToolRequest{}
	result, err := handleSearchMemories(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error")
	}

	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatal("expected text content")
	}

	var resp searchResponse
	if err := json.Unmarshal([]byte(text.Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(resp.Memories))
	}
	if resp.Memories[0].ID != "a1" {
		t.Errorf("expected ID=a1, got %s", resp.Memories[0].ID)
	}
	if resp.TotalActive != 1 {
		t.Errorf("expected TotalActive=1, got %d", resp.TotalActive)
	}
}

func TestHandleSearchMemories_WithQuery_ScoresResults(t *testing.T) {
	setupTestRepo(t)
	records := []memoryloop.MemoryRecord{
		{ID: "r1", Title: "Database connection pool", Body: "always use connection pooling for database access", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusActive, Strength: 4},
		{ID: "r2", Title: "Error handling pattern", Body: "wrap errors with context using fmt.Errorf", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusActive, Strength: 5},
	}
	setupTestState(t, memoryloop.ModeAuto, records)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query": "database connection",
	}

	result, err := handleSearchMemories(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error")
	}

	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatal("expected text content")
	}

	var resp searchResponse
	if err := json.Unmarshal([]byte(text.Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Memories) == 0 {
		t.Fatal("expected at least one result")
	}
	if resp.Memories[0].ID != "r1" {
		t.Errorf("expected r1 to rank first (database/connection match), got %s", resp.Memories[0].ID)
	}
}

func TestHandleSearchMemories_StatusFilterCandidate(t *testing.T) {
	setupTestRepo(t)
	records := []memoryloop.MemoryRecord{
		{ID: "a1", Title: "Active rule", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusActive, Strength: 5},
		{ID: "c1", Title: "Candidate rule", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusCandidate, Strength: 3},
	}
	setupTestState(t, memoryloop.ModeAuto, records)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"status": "candidate",
	}

	result, err := handleSearchMemories(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error")
	}

	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatal("expected text content")
	}

	var resp searchResponse
	if err := json.Unmarshal([]byte(text.Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(resp.Memories))
	}
	if resp.Memories[0].ID != "c1" {
		t.Errorf("expected c1, got %s", resp.Memories[0].ID)
	}
}

func TestHandleSearchMemories_KindFilter(t *testing.T) {
	setupTestRepo(t)
	records := []memoryloop.MemoryRecord{
		{ID: "r1", Title: "Repo rule", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusActive, Strength: 5},
		{ID: "w1", Title: "Workflow rule", Body: "body", Kind: memoryloop.KindWorkflowRule, Status: memoryloop.StatusActive, Strength: 4},
	}
	setupTestState(t, memoryloop.ModeAuto, records)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"kind": "workflow_rule",
	}

	result, err := handleSearchMemories(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error")
	}

	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatal("expected text content")
	}

	var resp searchResponse
	if err := json.Unmarshal([]byte(text.Text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(resp.Memories))
	}
	if resp.Memories[0].ID != "w1" {
		t.Errorf("expected w1, got %s", resp.Memories[0].ID)
	}
	if resp.TotalActive != 2 {
		t.Errorf("expected TotalActive=2 (unfiltered count), got %d", resp.TotalActive)
	}
}

func TestHandleSearchMemories_ModeOff_ReturnsError(t *testing.T) {
	setupTestRepo(t)
	setupTestState(t, memoryloop.ModeOff, nil)

	req := mcp.CallToolRequest{}
	result, err := handleSearchMemories(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for mode=off")
	}
}
