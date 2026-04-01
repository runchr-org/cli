package memorylooptui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

//nolint:recvcheck // bubbletea pattern: pointer receivers for mutation, value for update/view
type historyModel struct {
	state  *memoryloop.State
	styles tuiStyles
	width  int
	height int
	table  table.Model
}

func newHistoryModel(s tuiStyles) historyModel {
	columns := []table.Column{
		{Title: "Time", Width: 12},
		{Title: "Scope", Width: 14},
		{Title: "Generated", Width: 9},
		{Title: "Activated", Width: 9},
		{Title: "Candidate", Width: 9},
		{Title: "Window", Width: 7},
		{Title: "Tokens", Width: 8},
		{Title: "Cost", Width: 9},
	}
	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
	)
	st := table.DefaultStyles()
	st.Header = st.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		Bold(false).
		Faint(true)
	st.Selected = st.Selected.
		Foreground(lipgloss.Color("214")).
		Bold(false)
	t.SetStyles(st)

	return historyModel{
		styles: s,
		table:  t,
	}
}

func (m *historyModel) setState(state *memoryloop.State) {
	m.state = state
	m.rebuildTable()
}

func (m *historyModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.table.SetWidth(w)
	m.table.SetHeight(h - 6)
}

func (m *historyModel) rebuildTable() {
	if m.state == nil || m.state.Store == nil {
		m.table.SetRows(nil)
		return
	}
	history := m.state.Store.RefreshHistory
	rows := make([]table.Row, len(history))
	for i, h := range history {
		scope := h.Scope
		if h.ScopeValue != "" {
			scope += ":" + truncate(h.ScopeValue, 10)
		}
		tokens := "-"
		if h.InputTokens > 0 || h.OutputTokens > 0 {
			tokens = strconv.Itoa(h.InputTokens + h.OutputTokens)
		}
		cost := "-"
		if h.TotalCostUSD > 0 {
			cost = fmt.Sprintf("$%.4f", h.TotalCostUSD)
		}
		rows[i] = table.Row{
			timeAgo(h.At),
			scope,
			fmt.Sprintf("+%d", h.GeneratedCount),
			strconv.Itoa(h.ActivatedCount),
			strconv.Itoa(h.CandidateCount),
			strconv.Itoa(h.SourceWindow),
			tokens,
			cost,
		}
	}
	m.table.SetRows(rows)
}

func (m historyModel) update(msg tea.Msg) (historyModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if key.Matches(keyMsg, historyKeyMap.Refresh) {
			// Refresh is an async operation handled by root model.
			// For now, emit refreshStartedMsg so root can orchestrate.
			return m, func() tea.Msg { return refreshStartedMsg{} }
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m historyModel) view() string {
	var b strings.Builder

	// Section description
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.sectionHeader, "REFRESH HISTORY"))
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.dim,
		"Each refresh analyzes recent sessions to generate, update, and prune memories."))
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.dim,
		"Run: entire memory-loop refresh"))
	b.WriteString("\n\n")

	if m.state == nil || m.state.Store == nil || len(m.state.Store.RefreshHistory) == 0 {
		b.WriteString("  No refresh history yet. Press R to run your first refresh.\n")
		return b.String()
	}

	// Cost summary line if any refresh has cost data.
	var totalCost float64
	var totalTokens int
	var costEntries int
	for _, h := range m.state.Store.RefreshHistory {
		if h.TotalCostUSD > 0 || h.InputTokens > 0 || h.OutputTokens > 0 {
			totalCost += h.TotalCostUSD
			totalTokens += h.InputTokens + h.OutputTokens
			costEntries++
		}
	}
	if costEntries > 0 {
		b.WriteString("  ")
		b.WriteString(m.styles.render(m.styles.dim,
			fmt.Sprintf("Total: $%.4f across %d refreshes (%s tokens)",
				totalCost, costEntries, formatTokenCount(totalTokens))))
		b.WriteString("\n\n")
	}

	b.WriteString(m.table.View())
	return b.String()
}
