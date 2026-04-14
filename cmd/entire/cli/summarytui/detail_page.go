package summarytui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderMetadataHeader renders the fixed metadata header with two side-by-side boxes.
func renderMetadataHeader(s styles, row SessionData, width int) string {
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

	var line2 []string
	if row.Branch != "" {
		line2 = append(line2, s.render(s.detailLabel, "branch:")+s.render(s.detailValue, " "+row.Branch))
	}
	if row.Model != "" {
		line2 = append(line2, s.render(s.detailLabel, "model:")+s.render(s.detailValue, " "+row.Model))
	}
	if row.TurnCount > 0 {
		line2 = append(line2, s.render(s.detailLabel, "turns:")+s.render(s.detailValue, " "+strconv.Itoa(row.TurnCount)))
	}

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
func renderDetailContent(s styles, row SessionData, width int) string {
	var sections []string

	// Box 1: SUMMARY (intent, outcome, token stats)
	if box := renderSummaryBox(s, row, width); box != "" {
		sections = append(sections, box)
	}

	// Box 2: CODE (files touched)
	if box := renderCodeBox(s, row, width); box != "" {
		sections = append(sections, box)
	}

	// Box 3: LEARNINGS
	if row.Summary != nil {
		if box := renderLearningsBox(s, row, width); box != "" {
			sections = append(sections, box)
		}
	}

	// Box 4: SIGNALS (friction, open items)
	if box := renderSignalsBox(s, row, width); box != "" {
		sections = append(sections, box)
	}

	if len(sections) == 0 {
		return s.render(s.emptyState, "  No summary or insights cached. Press g to generate.")
	}

	return strings.Join(sections, "\n\n")
}

// renderSubSection renders a dim uppercase sub-section header followed by content lines.
// Returns nil if lines is empty. Prepends a blank line separator when appending to
// existing content (caller passes current length so first sub-section has no leading gap).
func renderSubSection(s styles, currentLen int, title string, lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	var result []string
	if currentLen > 0 {
		result = append(result, "") // blank line separator between sub-sections
	}
	result = append(result, s.render(s.dim, strings.ToUpper(title)))
	result = append(result, lines...)
	return result
}

func renderCodeBox(s styles, row SessionData, width int) string {
	var allLines []string

	// Files touched
	var fileLines []string
	for _, f := range row.FilesTouched {
		fileLines = append(fileLines, s.render(s.bullet, "• ")+f)
	}
	allLines = append(allLines, renderSubSection(s, len(allLines), "Files Touched", fileLines)...)

	if len(allLines) == 0 {
		return ""
	}
	return s.renderBox("CODE", strings.Join(allLines, "\n"), width)
}

func renderSummaryBox(s styles, row SessionData, width int) string {
	var lines []string

	if row.Summary != nil {
		if row.Summary.Intent != "" {
			lines = append(lines, s.render(s.detailLabel, "Intent: ")+s.render(s.detailValue, row.Summary.Intent))
		}
		if row.Summary.Outcome != "" {
			lines = append(lines, s.render(s.detailLabel, "Outcome: ")+s.render(s.detailValue, row.Summary.Outcome))
		}
	}

	// Stats line
	hasStats := row.InputTokens > 0 || row.OutputTokens > 0 || row.DurationMs > 0
	if hasStats {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		var parts []string
		parts = append(parts, s.render(s.detailLabel, "Tokens: ")+
			s.render(s.detailValue, formatTokensForDetail(row.InputTokens)+" in · "+
				formatTokensForDetail(row.CacheTokens)+" cache · "+
				formatTokensForDetail(row.OutputTokens)+" out"))
		if row.DurationMs > 0 {
			parts = append(parts, s.render(s.detailLabel, "Time: ")+
				s.render(s.detailValue, formatDuration(row.DurationMs)))
		}
		lines = append(lines, strings.Join(parts, "   "))
	}

	if len(lines) == 0 {
		return ""
	}
	return s.renderBox("SUMMARY", strings.Join(lines, "\n"), width)
}

func renderLearningsBox(s styles, row SessionData, width int) string {
	if row.Summary == nil {
		return ""
	}
	var lines []string

	// Repo learnings
	for _, item := range row.Summary.Learnings.Repo {
		lines = append(lines, s.render(s.bullet, "• ")+item+s.render(s.dim, " (repo)"))
	}

	// Code learnings
	for _, item := range row.Summary.Learnings.Code {
		text := item.Finding
		if item.Path != "" {
			text += s.render(s.dim, " ("+item.Path+")")
		}
		lines = append(lines, s.render(s.bullet, "• ")+text)
	}

	// Workflow learnings
	for _, item := range row.Summary.Learnings.Workflow {
		lines = append(lines, s.render(s.bullet, "• ")+item+s.render(s.dim, " (workflow)"))
	}

	if len(lines) == 0 {
		return ""
	}
	return s.renderBox("LEARNINGS", strings.Join(lines, "\n"), width)
}

func renderSignalsBox(s styles, row SessionData, width int) string {
	if row.Summary == nil {
		return ""
	}

	var allLines []string

	// Friction
	var frictionLines []string
	for _, item := range row.Summary.Friction {
		frictionLines = append(frictionLines, s.render(s.bullet, "• ")+item)
	}
	allLines = append(allLines, renderSubSection(s, len(allLines), "Friction", frictionLines)...)

	// Open items
	var openLines []string
	for _, item := range row.Summary.OpenItems {
		openLines = append(openLines, s.render(s.bullet, "• ")+item)
	}
	allLines = append(allLines, renderSubSection(s, len(allLines), "Open Items", openLines)...)

	if len(allLines) == 0 {
		return ""
	}
	return s.renderBox("SIGNALS", strings.Join(allLines, "\n"), width)
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
