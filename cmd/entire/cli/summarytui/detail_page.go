package summarytui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
)

// renderMetadataHeader renders the fixed metadata header with two side-by-side boxes.
func renderMetadataHeader(s styles, row insightsdb.SessionRow, width int) string {
	tokensWidth := 14
	sessionWidth := max(0, width-tokensWidth-3) // gap between boxes

	// SESSION box content — two lines for readability
	sep := s.render(s.dim, " · ")

	var line1 []string
	if row.CheckpointID != "" {
		line1 = append(line1, s.render(s.detailLabel, "checkpoint:")+s.render(s.detailValue, " "+row.CheckpointID))
	}
	if row.Agent != "" {
		line1 = append(line1, s.render(s.detailLabel, "agent:")+s.render(s.detailValue, " "+row.Agent))
	}
	author := row.OwnerName
	if author == "" {
		author = row.OwnerID
	}
	if author != "" {
		line1 = append(line1, s.render(s.detailLabel, "author:")+s.render(s.detailValue, " "+author))
	}

	var line2 []string
	if row.Branch != "" {
		line2 = append(line2, s.render(s.detailLabel, "branch:")+s.render(s.detailValue, " "+row.Branch))
	}
	if row.Model != "" {
		line2 = append(line2, s.render(s.detailLabel, "model:")+s.render(s.detailValue, " "+row.Model))
	}
	line2 = append(line2, s.render(s.detailLabel, "turns:")+s.render(s.detailValue, " "+strconv.Itoa(row.TurnCount)))

	var lines []string
	if len(line1) > 0 {
		lines = append(lines, strings.Join(line1, sep))
	}
	if len(line2) > 0 {
		lines = append(lines, strings.Join(line2, sep))
	}
	sessionContent := strings.Join(lines, "\n")
	sessionBox := s.renderBox("SESSION", sessionContent, sessionWidth)

	// TOKENS box content
	tokensContent := s.render(s.detailValue, formatTokensForDetail(row.TotalTokens))
	tokensBox := s.renderBox("TOKENS", tokensContent, tokensWidth)

	return lipgloss.JoinHorizontal(lipgloss.Top, sessionBox, " ", tokensBox)
}

// renderDetailContent renders the scrollable detail content with bordered boxes.
func renderDetailContent(s styles, row insightsdb.SessionRow, width int) string {
	var sections []string

	// Summary section
	if row.HasSummary {
		sections = append(sections, renderSummaryBox(s, row, width))
	}

	// Friction section
	if row.HasSummary && len(row.Friction) > 0 {
		sections = append(sections, renderFrictionBox(s, row, width))
	}

	// Learnings section
	if row.HasSummary && len(row.Learnings) > 0 {
		sections = append(sections, renderLearningsBox(s, row, width))
	}

	// Insights section (merged facets)
	if row.HasFacets {
		if box := renderInsightsBox(s, row, width); box != "" {
			sections = append(sections, box)
		}
	}

	if len(sections) == 0 {
		return s.render(s.emptyState, "  No summary or insights cached. Press g to generate.")
	}

	return strings.Join(sections, "\n\n")
}

// renderSubSection renders a dim uppercase sub-section header followed by content lines.
// Used by Tasks 3 and 4 (renderCodeBox, renderSignalsBox).
//
//nolint:unused // forward-declared; used by renderCodeBox and renderSignalsBox in Tasks 3-4
func renderSubSection(s styles, title string, lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	result := []string{s.render(s.dim, strings.ToUpper(title))}
	result = append(result, lines...)
	return result
}

func renderCodeBox(s styles, row insightsdb.SessionRow, width int) string {
	var allLines []string

	// Files touched
	var fileLines []string
	for _, f := range row.FilesTouched {
		fileLines = append(fileLines, s.render(s.bullet, "• ")+f)
	}
	allLines = append(allLines, renderSubSection(s, "Files Touched", fileLines)...)

	// Tool usage — sorted by count descending, compact single line
	if len(row.ToolCounts) > 0 {
		type toolCount struct {
			name  string
			count int
		}
		sorted := make([]toolCount, 0, len(row.ToolCounts))
		for name, count := range row.ToolCounts {
			sorted = append(sorted, toolCount{name, count})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].count > sorted[j].count
		})
		var parts []string
		for _, tc := range sorted {
			parts = append(parts, fmt.Sprintf("%s (%d)", tc.name, tc.count))
		}
		toolLine := s.render(s.bullet, "• ") + strings.Join(parts, " · ")
		allLines = append(allLines, renderSubSection(s, "Tool Usage", []string{toolLine})...)
	}

	// Implementation rationale
	var rationaleLines []string
	for _, item := range row.ImplementationRationale {
		rationaleLines = append(rationaleLines, s.render(s.bullet, "• ")+item)
	}
	allLines = append(allLines, renderSubSection(s, "Implementation Rationale", rationaleLines)...)

	// Tradeoffs
	var tradeoffLines []string
	for _, item := range row.Tradeoffs {
		tradeoffLines = append(tradeoffLines, s.render(s.bullet, "• ")+item)
	}
	allLines = append(allLines, renderSubSection(s, "Tradeoffs", tradeoffLines)...)

	// Codebase patterns
	var patternLines []string
	for _, item := range row.CodebasePatterns {
		patternLines = append(patternLines, s.render(s.bullet, "• ")+item)
	}
	allLines = append(allLines, renderSubSection(s, "Codebase Patterns", patternLines)...)

	if len(allLines) == 0 {
		return ""
	}
	return s.renderBox("CODE", strings.Join(allLines, "\n"), width)
}

func renderSummaryBox(s styles, row insightsdb.SessionRow, width int) string {
	var lines []string
	if row.Intent != "" {
		lines = append(lines, s.render(s.detailLabel, "Intent: ")+s.render(s.detailValue, row.Intent))
	}
	if row.Outcome != "" {
		lines = append(lines, s.render(s.detailLabel, "Outcome: ")+s.render(s.detailValue, row.Outcome))
	}

	// Score line
	if row.OverallScore > 0 {
		scoreLine := s.render(s.detailLabel, "Score: ") +
			s.render(s.detailValue, fmt.Sprintf("%.1f", row.OverallScore)) + " " +
			s.render(s.dim, fmt.Sprintf("— tok:%.1f · 1st:%.1f · fric:%.1f · foc:%.1f",
				row.ScoreTokenEff, row.ScoreFirstPass, row.ScoreFriction, row.ScoreFocus))
		lines = append(lines, scoreLine)
	}

	// Stats line
	hasStats := row.InputTokens > 0 || row.OutputTokens > 0 || row.APICallCount > 0 || row.DurationMs > 0
	if hasStats {
		var parts []string
		parts = append(parts, s.render(s.detailLabel, "Tokens: ")+
			s.render(s.detailValue, formatTokensForDetail(row.InputTokens)+" in · "+
				formatTokensForDetail(row.CacheTokens)+" cache · "+
				formatTokensForDetail(row.OutputTokens)+" out"))
		if row.APICallCount > 0 {
			parts = append(parts, s.render(s.detailLabel, "API: ")+
				s.render(s.detailValue, fmt.Sprintf("%d calls", row.APICallCount)))
		}
		if row.DurationMs > 0 {
			parts = append(parts, s.render(s.detailLabel, "Time: ")+
				s.render(s.detailValue, formatDuration(row.DurationMs)))
		}
		if row.AgentPct > 0 {
			parts = append(parts, s.render(s.detailLabel, "Agent: ")+
				s.render(s.detailValue, fmt.Sprintf("%d%%", int(row.AgentPct))))
		}
		lines = append(lines, strings.Join(parts, "   "))
	}

	if len(lines) == 0 {
		return ""
	}
	return s.renderBox("SUMMARY", strings.Join(lines, "\n"), width)
}

func renderFrictionBox(s styles, row insightsdb.SessionRow, width int) string {
	var lines []string
	for _, item := range row.Friction {
		lines = append(lines, s.render(s.bullet, "• ")+item)
	}
	return s.renderBox("FRICTION", strings.Join(lines, "\n"), width)
}

func renderLearningsBox(s styles, row insightsdb.SessionRow, width int) string {
	var lines []string
	for _, item := range row.Learnings {
		text := item.Finding
		if item.Scope != "" {
			text += s.render(s.dim, " ("+item.Scope+")")
		}
		lines = append(lines, s.render(s.bullet, "• ")+text)
	}
	return s.renderBox("LEARNINGS", strings.Join(lines, "\n"), width)
}

func renderInsightsBox(s styles, row insightsdb.SessionRow, width int) string {
	var lines []string

	for _, item := range row.Facets.RepoGotchas {
		lines = append(lines, s.render(s.bullet, "• ")+s.render(s.detailLabel, "Repo Gotcha: ")+item)
	}
	for _, item := range row.Facets.WorkflowGaps {
		lines = append(lines, s.render(s.bullet, "• ")+s.render(s.detailLabel, "Workflow Gap: ")+item)
	}
	for _, item := range row.Facets.FailureLoops {
		lines = append(lines, s.render(s.bullet, "• ")+s.render(s.detailLabel, "Failure Loop: ")+fmt.Sprintf("%s (%d)", item.Description, item.Count))
	}
	for _, item := range row.Facets.MissingContext {
		lines = append(lines, s.render(s.bullet, "• ")+s.render(s.detailLabel, "Missing Context: ")+item.Item)
	}
	for _, item := range row.Facets.RepeatedUserInstructions {
		lines = append(lines, s.render(s.bullet, "• ")+s.render(s.detailLabel, "Repeated: ")+item.Instruction)
	}
	for _, item := range row.Facets.SkillSignals {
		lines = append(lines, s.render(s.bullet, "• ")+s.render(s.detailLabel, "Skill: ")+item.SkillName)
	}
	for _, item := range row.Facets.ReviewDerivedRules {
		lines = append(lines, s.render(s.bullet, "• ")+s.render(s.detailLabel, "Rule: ")+item.Rule)
	}

	if len(lines) == 0 {
		return ""
	}
	return s.renderBox("INSIGHTS", strings.Join(lines, "\n"), width)
}

func formatTokensForDetail(tokens int) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fk", float64(tokens)/1_000)
	default:
		return strconv.Itoa(tokens)
	}
}

func formatDuration(ms int64) string {
	totalSec := ms / 1000
	switch {
	case totalSec >= 3600:
		h := totalSec / 3600
		m := (totalSec % 3600) / 60
		return fmt.Sprintf("%dh %dm", h, m)
	case totalSec >= 60:
		m := totalSec / 60
		s := totalSec % 60
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", totalSec)
	}
}
