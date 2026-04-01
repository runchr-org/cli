package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

type saveResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

func handleSaveMemory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	state, err := checkModeGate(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	kind, kindErr := request.RequireString("kind")
	if kindErr != nil {
		return mcp.NewToolResultError("kind is required"), nil //nolint:nilerr // MCP tool errors are returned in the result, not as Go errors
	}
	title, titleErr := request.RequireString("title")
	if titleErr != nil {
		return mcp.NewToolResultError("title is required"), nil //nolint:nilerr // MCP tool errors are returned in the result, not as Go errors
	}
	body, bodyErr := request.RequireString("body")
	if bodyErr != nil {
		return mcp.NewToolResultError("body is required"), nil //nolint:nilerr // MCP tool errors are returned in the result, not as Go errors
	}
	why := request.GetString("why", "")
	scope := request.GetString("scope", "me")

	// Check for duplicates by fingerprint.
	fp := memoryloop.FingerprintForRecord(memoryloop.Kind(kind), title, body)
	for _, r := range state.Store.Records {
		if r.Fingerprint == fp {
			resp := saveResponse{
				ID:        r.ID,
				Status:    string(r.Status),
				Message:   "A similar memory already exists (status: " + string(r.Status) + "). No duplicate created.",
				Duplicate: true,
			}
			data, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return nil, fmt.Errorf("failed to marshal duplicate response: %w", marshalErr)
			}
			return mcp.NewToolResultText(string(data)), nil
		}
	}

	scopeKind := memoryloop.ScopeKindMe
	if scope == "repo" {
		scopeKind = memoryloop.ScopeKindRepo
	}

	now := time.Now().UTC()
	id := memoryloop.MakeRecordID(memoryloop.Kind(kind), title)
	record := memoryloop.MemoryRecord{
		ID:          id,
		Kind:        memoryloop.Kind(kind),
		Title:       strings.TrimSpace(title),
		Body:        strings.TrimSpace(body),
		Why:         strings.TrimSpace(why),
		Fingerprint: fp,
		ScopeKind:   scopeKind,
		Origin:      memoryloop.OriginManual,
		Confidence:  "medium",
		Strength:    3,
		Status:      memoryloop.StatusCandidate,
		CreatedAt:   now,
		UpdatedAt:   now,
		History: []memoryloop.HistoryEvent{
			{Type: "mcp-created", At: now},
		},
	}

	state.Store.Records = append(state.Store.Records, record)
	if saveErr := memoryloop.SaveState(ctx, state); saveErr != nil {
		return mcp.NewToolResultError("failed to save memory: " + saveErr.Error()), nil //nolint:nilerr // MCP tool errors are returned in the result, not as Go errors
	}

	resp := saveResponse{
		ID:      record.ID,
		Status:  string(record.Status),
		Message: "Memory saved as candidate. Use `entire memory-loop activate " + record.ID + "` or the TUI to activate it.",
	}
	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return nil, fmt.Errorf("failed to marshal save response: %w", marshalErr)
	}
	return mcp.NewToolResultText(string(data)), nil
}
