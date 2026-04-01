package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

// NewServer creates an MCP server with memory loop tools registered.
func NewServer() *server.MCPServer {
	s := server.NewMCPServer(
		"Entire Memory Loop",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	searchTool := mcp.NewTool("search_memories",
		mcp.WithDescription(
			"Search the memory store for this repository. Returns memories (rules, patterns, anti-patterns) "+
				"relevant to your current task. Use this when starting work on a new area of the codebase, "+
				"when you encounter unfamiliar patterns, or when you want to check if there are known conventions "+
				"before making changes.",
		),
		mcp.WithString("query",
			mcp.Description("Free-text search query. Empty returns all active memories."),
		),
		mcp.WithString("kind",
			mcp.Description("Filter by kind."),
			mcp.Enum("repo_rule", "workflow_rule", "agent_instruction", "skill_patch", "anti_pattern"),
		),
		mcp.WithString("scope",
			mcp.Description("Filter by scope."),
			mcp.Enum("me", "repo"),
		),
		mcp.WithString("status",
			mcp.Description("Filter by status. Defaults to active."),
			mcp.Enum("active", "candidate"),
		),
	)
	s.AddTool(searchTool, handleSearchMemories)

	saveTool := mcp.NewTool("save_memory",
		mcp.WithDescription(
			"Save a new insight about this repository as a memory candidate. Use this when you discover "+
				"a pattern, convention, or anti-pattern during the session that would be valuable for future work. "+
				"The memory is saved as a candidate for the user to review and activate.",
		),
		mcp.WithString("kind",
			mcp.Required(),
			mcp.Description("Memory kind."),
			mcp.Enum("repo_rule", "workflow_rule", "agent_instruction", "skill_patch", "anti_pattern"),
		),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Short descriptive title (max 80 chars)."),
		),
		mcp.WithString("body",
			mcp.Required(),
			mcp.Description("One actionable sentence (max 200 chars)."),
		),
		mcp.WithString("why",
			mcp.Description("Reasoning or evidence for this memory."),
		),
		mcp.WithString("scope",
			mcp.Description("Scope: me (personal, default) or repo (shared)."),
			mcp.Enum("me", "repo"),
		),
	)
	s.AddTool(saveTool, handleSaveMemory)

	return s
}

// checkModeGate loads memory loop state and returns an error if mode is off.
func checkModeGate(ctx context.Context) (*memoryloop.State, error) {
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load memory loop state: %w", err)
	}

	mode := memoryloop.ModeOff
	if state.Store != nil {
		mode = state.Store.Mode
	}

	if mode == memoryloop.ModeOff || mode == "" {
		return nil, errors.New(
			"Memory loop is disabled. Run `entire memory-loop mode auto` or " +
				"`entire memory-loop mode manual` to enable.",
		)
	}

	return state, nil
}

func handleSaveMemory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("not implemented"), nil
}
