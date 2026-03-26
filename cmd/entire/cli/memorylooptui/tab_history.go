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
		{Title: "Scope", Width: 8},
		{Title: "Generated", Width: 9},
		{Title: "Activated", Width: 9},
		{Title: "Candidate", Width: 9},
		{Title: "Window", Width: 7},
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
		Foreground(lipgloss.Color("6")).
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
	m.table.SetHeight(h - 2)
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
			scope += ":" + truncate(h.ScopeValue, 6)
		}
		rows[i] = table.Row{
			timeAgo(h.At),
			scope,
			fmt.Sprintf("+%d", h.GeneratedCount),
			strconv.Itoa(h.ActivatedCount),
			strconv.Itoa(h.CandidateCount),
			strconv.Itoa(h.SourceWindow),
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
	if m.state == nil || m.state.Store == nil || len(m.state.Store.RefreshHistory) == 0 {
		var b strings.Builder
		b.WriteString("\n  No refresh history yet.\n")
		b.WriteString("  Press R to run your first refresh.\n")
		return b.String()
	}

	return m.table.View()
}
