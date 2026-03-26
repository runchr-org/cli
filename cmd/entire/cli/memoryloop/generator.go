package memoryloop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/improve"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
)

const defaultMemorySlug = "memory"

const DefaultMaxRecords = 20

type Generator struct {
	Runner *llmcli.Runner
}

type GenerateInput struct {
	Analysis     improve.PatternAnalysis
	Sessions     []insightsdb.SessionRow
	SourceWindow int
	MaxRecords   int
}

type generateResponse struct {
	Records []generateRecord `json:"records"`
}

type generateRecord struct {
	Kind             Kind     `json:"kind"`
	Title            string   `json:"title"`
	Body             string   `json:"body"`
	Why              string   `json:"why"`
	Evidence         []string `json:"evidence"`
	SourceSessionIDs []string `json:"source_session_ids"`
	Confidence       string   `json:"confidence"`
	Strength         int      `json:"strength"`
}

func (g *Generator) Generate(ctx context.Context, input GenerateInput) ([]MemoryRecord, *llmcli.UsageInfo, error) {
	if g.Runner == nil {
		g.Runner = &llmcli.Runner{}
	}

	if input.MaxRecords <= 0 {
		input.MaxRecords = DefaultMaxRecords
	}

	raw, usage, err := g.Runner.Execute(ctx, BuildPrompt(input))
	if err != nil {
		return nil, nil, fmt.Errorf("execute memory-loop prompt: %w", err)
	}

	var resp generateResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, nil, fmt.Errorf("parse memory-loop JSON: %w", err)
	}

	return buildGeneratedRecords(resp, input, time.Now().UTC()), usage, nil
}

func buildGeneratedRecords(resp generateResponse, input GenerateInput, now time.Time) []MemoryRecord {
	seen := make(map[string]struct{})
	records := make([]MemoryRecord, 0, len(resp.Records))
	for _, item := range resp.Records {
		title := strings.TrimSpace(item.Title)
		body := strings.TrimSpace(item.Body)
		if title == "" || body == "" {
			continue
		}
		kind := item.Kind
		if kind == "" {
			kind = KindRepoRule
		}
		key := strings.ToLower(string(kind) + "|" + title + "|" + body)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		record := MemoryRecord{
			ID:               makeRecordID(kind, title),
			Kind:             kind,
			Title:            title,
			Body:             body,
			Why:              strings.TrimSpace(item.Why),
			Evidence:         trimSlice(item.Evidence, 3),
			SourceSessionIDs: trimSlice(item.SourceSessionIDs, input.SourceWindow),
			Confidence:       normalizeConfidence(item.Confidence),
			Strength:         clamp(item.Strength, 1, 5),
			Status:           StatusCandidate,
			Origin:           OriginGenerated,
			Fingerprint:      fingerprintForRecord(kind, title, body),
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		records = append(records, record)
		if len(records) >= input.MaxRecords {
			break
		}
	}

	return records
}

func BuildPrompt(input GenerateInput) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Build a compact repo memory snapshot from the last %d AI coding sessions.\n\n", input.SourceWindow)

	sb.WriteString("<analysis>\n")
	fmt.Fprintf(&sb, "sessions: %d\n", input.Analysis.SessionCount)
	for _, signal := range input.Analysis.RepeatedInstructions {
		fmt.Fprintf(&sb, "repeated_instruction: %s (%d)\n", signal.Value, signal.Count)
		for _, evidence := range trimSlice(signal.Evidence, 3) {
			fmt.Fprintf(&sb, "  evidence: %q\n", evidence)
		}
	}
	for _, signal := range input.Analysis.MissingContextSignals {
		fmt.Fprintf(&sb, "missing_context: %s (%d)\n", signal.Value, signal.Count)
		for _, evidence := range trimSlice(signal.Evidence, 3) {
			fmt.Fprintf(&sb, "  evidence: %q\n", evidence)
		}
	}
	for _, signal := range input.Analysis.FailureLoops {
		fmt.Fprintf(&sb, "failure_loop: %s (%d)\n", signal.Value, signal.Count)
		for _, evidence := range trimSlice(signal.Evidence, 3) {
			fmt.Fprintf(&sb, "  evidence: %q\n", evidence)
		}
	}
	for _, opportunity := range input.Analysis.SkillOpportunities {
		fmt.Fprintf(&sb, "skill_opportunity: %s (%d)\n", opportunity.SkillName, opportunity.Count)
		if opportunity.SkillPath != "" {
			fmt.Fprintf(&sb, "  path: %s\n", opportunity.SkillPath)
		}
		if opportunity.MissingInstruction != "" {
			fmt.Fprintf(&sb, "  missing_instruction: %s\n", opportunity.MissingInstruction)
		}
		for _, friction := range trimSlice(opportunity.Friction, 3) {
			fmt.Fprintf(&sb, "  friction: %q\n", friction)
		}
	}
	for _, learning := range trimSlice(input.Analysis.RepoLearnings, 5) {
		fmt.Fprintf(&sb, "repo_learning: %s\n", learning)
	}
	for _, learning := range trimSlice(input.Analysis.WorkflowLearnings, 5) {
		fmt.Fprintf(&sb, "workflow_learning: %s\n", learning)
	}
	sb.WriteString("</analysis>\n\n")

	sb.WriteString("<recent_sessions>\n")
	for _, session := range input.Sessions {
		fmt.Fprintf(&sb, "session: %s agent=%s model=%s\n", session.SessionID, session.Agent, session.Model)
		for _, friction := range trimSlice(session.Friction, 3) {
			fmt.Fprintf(&sb, "  friction: %q\n", friction)
		}
		for _, instruction := range session.Facets.RepeatedUserInstructions {
			fmt.Fprintf(&sb, "  repeated_instruction: %q\n", instruction.Instruction)
		}
		for _, signal := range session.Facets.MissingContext {
			fmt.Fprintf(&sb, "  missing_context: %q\n", signal.Item)
		}
		for _, signal := range session.Facets.SkillSignals {
			fmt.Fprintf(&sb, "  skill_signal: %s | %s\n", signal.SkillName, signal.MissingInstruction)
		}
	}
	sb.WriteString("</recent_sessions>\n\n")

	fmt.Fprintf(&sb, `Return ONLY JSON with this structure:
{
  "records": [{
    "kind": "repo_rule|workflow_rule|agent_instruction|skill_patch|anti_pattern",
    "title": "short title",
    "body": "one actionable sentence",
    "why": "why this memory matters for later sessions",
    "evidence": ["short quote"],
    "source_session_ids": ["session-id"],
    "confidence": "high|medium|low",
    "strength": 1
  }]
}

Guidelines:
- Distill stable repo-specific lessons, not generic coding advice
- Prefer rules the developer would want injected into future Claude sessions
- Convert skill-related friction into skill_patch memories when appropriate
- Avoid duplicates and avoid one-off incidents without repeated evidence
- Return at most %d records
`, input.MaxRecords)

	return sb.String()
}

func makeRecordID(kind Kind, title string) string {
	base := strings.ToLower(strings.TrimSpace(title))
	var b strings.Builder
	lastDash := false
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = defaultMemorySlug
	}
	return fmt.Sprintf("%s-%s", kind, slug)
}

func trimSlice[T any](items []T, limit int) []T {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func normalizeConfidence(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "medium"
	}
}

func clamp(value, lo, hi int) int {
	if value < lo {
		return lo
	}
	if value > hi {
		return hi
	}
	return value
}
