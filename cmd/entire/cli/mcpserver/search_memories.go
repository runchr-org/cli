package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

var searchStopWords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "are": {}, "but": {}, "not": {},
	"you": {}, "all": {}, "can": {}, "has": {}, "her": {}, "was": {},
	"one": {}, "our": {}, "out": {}, "use": {}, "this": {}, "that": {},
	"with": {}, "have": {}, "from": {}, "they": {}, "been": {}, "said": {},
	"each": {}, "will": {}, "when": {}, "what": {}, "your": {}, "also": {},
	"into": {}, "just": {}, "like": {}, "about": {}, "before": {}, "does": {},
	"how": {}, "its": {}, "may": {},
}

type searchResponse struct {
	Memories    []memoryResult `json:"memories"`
	TotalActive int            `json:"total_active"`
	Mode        string         `json:"mode"`
}

type memoryResult struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Why      string `json:"why,omitempty"`
	Strength int    `json:"strength"`
	Scope    string `json:"scope"`
	Outcome  string `json:"outcome,omitempty"`
	Score    int    `json:"score,omitempty"`
}

const maxSearchResults = 10

// handleSearchMemories handles the search_memories MCP tool call.
func handleSearchMemories(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	state, err := checkModeGate(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	query := request.GetString("query", "")
	kindFilter := request.GetString("kind", "")
	scopeFilter := request.GetString("scope", "")
	statusFilter := request.GetString("status", "active")

	records := state.Store.Records

	totalActive := countActive(records)

	var results []memoryResult
	if query != "" {
		results = scoreAndRank(query, records, statusFilter, kindFilter, scopeFilter)
	} else {
		results = filterAndSort(records, statusFilter, kindFilter, scopeFilter)
	}

	if len(results) > maxSearchResults {
		results = results[:maxSearchResults]
	}

	resp := searchResponse{
		Memories:    results,
		TotalActive: totalActive,
		Mode:        string(state.Store.Mode),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search response: %w", err)
	}

	return mcp.NewToolResultText(string(data)), nil
}

func countActive(records []memoryloop.MemoryRecord) int {
	count := 0
	for _, r := range records {
		if r.Status == memoryloop.StatusActive {
			count++
		}
	}
	return count
}

// tokenize splits text into lowercase tokens of 3+ chars, excluding stop words.
func tokenize(text string) map[string]struct{} {
	tokens := make(map[string]struct{})
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return 'a' > r || r > 'z'
	})
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		if _, isStop := searchStopWords[w]; isStop {
			continue
		}
		tokens[w] = struct{}{}
	}
	return tokens
}

// matchesFilters returns true if the record matches the given status/kind/scope filters.
func matchesFilters(r memoryloop.MemoryRecord, statusFilter, kindFilter, scopeFilter string) bool {
	if statusFilter != "" && string(r.Status) != statusFilter {
		return false
	}
	if kindFilter != "" && string(r.Kind) != kindFilter {
		return false
	}
	if scopeFilter != "" && string(r.ScopeKind) != scopeFilter {
		return false
	}
	return true
}

func toMemoryResult(r memoryloop.MemoryRecord, score int) memoryResult {
	return memoryResult{
		ID:       r.ID,
		Kind:     string(r.Kind),
		Title:    r.Title,
		Body:     r.Body,
		Why:      r.Why,
		Strength: r.Strength,
		Scope:    string(r.ScopeKind),
		Outcome:  string(r.Outcome),
		Score:    score,
	}
}

// scoreAndRank scores records by token overlap with query, applies filters, and returns sorted results.
func scoreAndRank(query string, records []memoryloop.MemoryRecord, statusFilter, kindFilter, scopeFilter string) []memoryResult {
	queryTokens := tokenize(query)

	type scored struct {
		result   memoryResult
		score    int
		strength int
	}

	var candidates []scored
	for _, r := range records {
		if !matchesFilters(r, statusFilter, kindFilter, scopeFilter) {
			continue
		}
		recordTokens := tokenize(r.Title + " " + r.Body)
		overlap := 0
		for t := range queryTokens {
			if _, ok := recordTokens[t]; ok {
				overlap++
			}
		}
		candidates = append(candidates, scored{
			result:   toMemoryResult(r, overlap),
			score:    overlap,
			strength: r.Strength,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].strength > candidates[j].strength
	})

	results := make([]memoryResult, 0, len(candidates))
	for _, c := range candidates {
		results = append(results, c.result)
	}
	return results
}

// filterAndSort filters records by status/kind/scope and sorts by strength desc then title asc.
func filterAndSort(records []memoryloop.MemoryRecord, statusFilter, kindFilter, scopeFilter string) []memoryResult {
	var filtered []memoryloop.MemoryRecord
	for _, r := range records {
		if matchesFilters(r, statusFilter, kindFilter, scopeFilter) {
			filtered = append(filtered, r)
		}
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Strength != filtered[j].Strength {
			return filtered[i].Strength > filtered[j].Strength
		}
		return filtered[i].Title < filtered[j].Title
	})

	results := make([]memoryResult, 0, len(filtered))
	for _, r := range filtered {
		results = append(results, toMemoryResult(r, 0))
	}
	return results
}
