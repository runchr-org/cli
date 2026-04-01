package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

func TestHandleSaveMemory_CreatesCandidate(t *testing.T) {
	setupTestRepo(t)
	setupTestState(t, memoryloop.ModeManual, nil)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"kind":  "repo_rule",
		"title": "Always check error returns",
		"body":  "Every error return must be checked explicitly.",
		"why":   "Unchecked errors cause silent failures.",
		"scope": "me",
	}

	result, err := handleSaveMemory(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error")
	}

	var resp saveResponse
	text, _ := mcp.AsTextContent(result.Content[0])
	if err := json.Unmarshal([]byte(text.Text), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Status != "candidate" {
		t.Errorf("status = %s, want candidate", resp.Status)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
	if resp.Duplicate {
		t.Error("expected duplicate=false")
	}

	// Verify persisted.
	state, loadErr := memoryloop.LoadState(context.Background())
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if len(state.Store.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(state.Store.Records))
	}
	r := state.Store.Records[0]
	if r.Origin != memoryloop.OriginManual {
		t.Errorf("origin = %s, want manual", r.Origin)
	}
	if r.Status != memoryloop.StatusCandidate {
		t.Errorf("status = %s, want candidate", r.Status)
	}
	if r.Why != "Unchecked errors cause silent failures." {
		t.Errorf("why not preserved: %s", r.Why)
	}
}

func TestHandleSaveMemory_DetectsDuplicate(t *testing.T) {
	setupTestRepo(t)
	fp := memoryloop.FingerprintForRecord(memoryloop.KindRepoRule, "Always check error returns", "Every error return must be checked explicitly.")
	existing := []memoryloop.MemoryRecord{
		{
			ID:          "repo_rule-always-check-error-returns",
			Title:       "Always check error returns",
			Body:        "Every error return must be checked explicitly.",
			Kind:        memoryloop.KindRepoRule,
			Status:      memoryloop.StatusActive,
			Strength:    4,
			Fingerprint: fp,
		},
	}
	setupTestState(t, memoryloop.ModeManual, existing)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"kind":  "repo_rule",
		"title": "Always check error returns",
		"body":  "Every error return must be checked explicitly.",
	}

	result, err := handleSaveMemory(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp saveResponse
	text, _ := mcp.AsTextContent(result.Content[0])
	if unmarshalErr := json.Unmarshal([]byte(text.Text), &resp); unmarshalErr != nil {
		t.Fatalf("parse: %v", unmarshalErr)
	}

	if !resp.Duplicate {
		t.Error("expected duplicate=true")
	}
	if resp.Status != "active" {
		t.Errorf("status = %s, want active", resp.Status)
	}
}

func TestHandleSaveMemory_MissingRequired(t *testing.T) {
	setupTestRepo(t)
	setupTestState(t, memoryloop.ModeManual, nil)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"kind": "repo_rule",
	}

	result, err := handleSaveMemory(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing required fields")
	}
}

func TestHandleSaveMemory_ModeOff_ReturnsError(t *testing.T) {
	setupTestRepo(t)
	setupTestState(t, memoryloop.ModeOff, nil)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"kind":  "repo_rule",
		"title": "Test",
		"body":  "Test body",
	}

	result, err := handleSaveMemory(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for mode=off")
	}
}

func TestHandleSaveMemory_DefaultsToScopeMe(t *testing.T) {
	setupTestRepo(t)
	setupTestState(t, memoryloop.ModeManual, nil)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"kind":  "workflow_rule",
		"title": "Run lint before commit",
		"body":  "Always run mise run lint before committing.",
	}

	result, err := handleSaveMemory(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error")
	}

	state, loadErr := memoryloop.LoadState(context.Background())
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if state.Store.Records[0].ScopeKind != memoryloop.ScopeKindMe {
		t.Errorf("scope = %s, want me", state.Store.Records[0].ScopeKind)
	}
}
