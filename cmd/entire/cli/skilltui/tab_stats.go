package skilltui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/skilldb"
)

//nolint:recvcheck // bubbletea pattern: pointer receivers for mutation, value for update/view
type statsModel struct {
	stats    *skilldb.SkillStatsResult
	sessions []skilldb.SkillSessionRow
	agents   []skilldb.AgentBreakdownRow
	styles   tuiStyles
	width    int
	height   int
}

func (m *statsModel) setData(stats *skilldb.SkillStatsResult, sessions []skilldb.SkillSessionRow, agents []skilldb.AgentBreakdownRow) {
	m.stats = stats
	m.sessions = sessions
	m.agents = agents
}

func (m *statsModel) setSize(w, h int) { m.width = w; m.height = h }

func (m statsModel) update(_ tea.Msg) (statsModel, tea.Cmd) {
	return m, nil
}

func (m statsModel) view() string {
	if m.stats == nil {
		return "  Loading stats..."
	}

	var b strings.Builder
	cardWidth := m.width - 4
	if cardWidth < 40 {
		cardWidth = 40
	}

	// Card 1: Usage Overview
	b.WriteString(m.renderUsageOverview(cardWidth))
	b.WriteString("\n")

	// Card 2: Success Rate
	b.WriteString(m.renderSuccessRate(cardWidth))
	b.WriteString("\n")

	// Card 3: Agent Breakdown
	if len(m.agents) > 0 {
		b.WriteString(m.renderAgentBreakdown(cardWidth))
		b.WriteString("\n")
	}

	// Card 4: Recent Sessions
	if len(m.sessions) > 0 {
		b.WriteString(m.renderRecentSessions(cardWidth))
	}

	return b.String()
}

func (m statsModel) renderUsageOverview(cardWidth int) string {
	st := m.stats
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(cardWidth).
		Padding(0, 1)

	var content strings.Builder
	content.WriteString(m.styles.render(m.styles.sectionHeader, "Usage Overview"))
	content.WriteString("\n")
	fmt.Fprintf(&content, "Total Sessions: %-8d First Used: %-14s Avg Score: %.1f\n",
		st.TotalSessions, formatDate(st.FirstUsed), st.AvgScore)
	fmt.Fprintf(&content, "Total Tokens:   %-8s Last Used:  %-14s Frequency: %.1f/wk",
		formatTokens64(st.TotalTokens), formatDate(st.LastUsed), st.SessionsPerWeek)

	return border.Render(content.String())
}

func (m statsModel) renderSuccessRate(cardWidth int) string {
	st := m.stats
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(cardWidth).
		Padding(0, 1)

	successCount := st.TotalSessions - st.TotalFriction
	if successCount < 0 {
		successCount = 0
	}

	var content strings.Builder
	content.WriteString(m.styles.render(m.styles.sectionHeader, "Success Rate"))
	content.WriteString("\n")

	barWidth := 25
	if st.TotalSessions > 0 {
		successPct := float64(successCount) / float64(st.TotalSessions) * 100
		frictionPct := float64(st.TotalFriction) / float64(st.TotalSessions) * 100
		successFill := min(int(successPct/100*float64(barWidth)), barWidth)
		frictionFill := min(int(frictionPct/100*float64(barWidth)), barWidth)

		successBar := m.styles.render(m.styles.success, strings.Repeat("\u2588", successFill)) +
			strings.Repeat("\u2591", barWidth-successFill)
		frictionBar := m.styles.render(m.styles.friction, strings.Repeat("\u2588", frictionFill)) +
			strings.Repeat("\u2591", barWidth-frictionFill)

		fmt.Fprintf(&content, "Success  %s  %3.0f%%  (%d sessions)\n",
			successBar, successPct, successCount)
		fmt.Fprintf(&content, "Friction %s  %3.0f%%  (%d sessions)",
			frictionBar, frictionPct, st.TotalFriction)
	} else {
		content.WriteString("No session data available")
	}

	return border.Render(content.String())
}

func (m statsModel) renderAgentBreakdown(cardWidth int) string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(cardWidth).
		Padding(0, 1)

	var content strings.Builder
	content.WriteString(m.styles.render(m.styles.sectionHeader, "Agent Breakdown"))
	content.WriteString("\n")
	fmt.Fprintf(&content, "%-16s %8s %9s %8s\n",
		"Agent", "Sessions", "Avg Score", "Tokens")

	for _, a := range m.agents {
		fmt.Fprintf(&content, "%-16s %8d %9.1f %8s\n",
			a.Agent, a.SessionCount, a.AvgScore, formatTokens64(a.TotalTokens))
	}

	return border.Render(strings.TrimRight(content.String(), "\n"))
}

func (m statsModel) renderRecentSessions(cardWidth int) string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(cardWidth).
		Padding(0, 1)

	var content strings.Builder
	content.WriteString(m.styles.render(m.styles.sectionHeader, "Recent Sessions"))
	content.WriteString("\n")

	limit := 5
	if len(m.sessions) < limit {
		limit = len(m.sessions)
	}

	for _, sess := range m.sessions[:limit] {
		outcomeStr := sess.Outcome
		switch outcomeStr {
		case "success":
			outcomeStr = m.styles.render(m.styles.success, "success")
		case "friction":
			outcomeStr = m.styles.render(m.styles.friction, "friction")
		}

		fmt.Fprintf(&content, "%s  %-14s %-10s Score: %-4.0f Tokens: %s\n",
			formatDate(sess.CreatedAt),
			sess.Agent,
			outcomeStr,
			sess.OverallScore,
			formatTokens(sess.TotalTokens))
	}

	return border.Render(strings.TrimRight(content.String(), "\n"))
}
