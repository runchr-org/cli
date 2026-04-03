package memorylooptui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

//nolint:recvcheck // bubbletea pattern: pointer receivers for mutation, value for update/view
type injectionModel struct {
	state      *memoryloop.State
	styles     tuiStyles
	width      int
	height     int
	logTable   table.Model
	input      textinput.Model
	inputFocus bool
	report     memoryloop.SelectionReport
}

func newInjectionModel(s tuiStyles) injectionModel {
	columns := []table.Column{
		{Title: "Time", Width: 10},
		{Title: "Session", Width: 10},
		{Title: "Count", Width: 5},
		{Title: "Prompt Preview", Width: 40},
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

	ti := textinput.New()
	ti.Placeholder = "type a prompt to test memory matching..."
	ti.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("\u276f") + " "
	ti.Width = 80

	return injectionModel{
		styles:   s,
		logTable: t,
		input:    ti,
	}
}

func (m *injectionModel) setState(state *memoryloop.State) {
	m.state = state
	m.rebuildLogTable()
}

func (m *injectionModel) setSize(w, h int) {
	m.width = w
	m.height = h
	// Reserve: prompt tester (3) + section label (2) + detail card (12) + matches (6)
	tableH := h - 23
	if tableH < 3 {
		tableH = 3
	}
	if tableH > 10 {
		tableH = 10 // Cap to avoid pushing detail off screen
	}
	m.logTable.SetWidth(w)
	m.logTable.SetHeight(tableH)
	m.input.Width = w - 4
}

func (m *injectionModel) rebuildLogTable() {
	if m.state == nil {
		m.logTable.SetRows(nil)
		return
	}
	logs := m.state.InjectionLogs
	rows := make([]table.Row, len(logs))
	for i, l := range logs {
		rows[i] = table.Row{
			timeAgo(l.InjectedAt),
			truncate(l.SessionID, 10),
			strconv.Itoa(len(l.InjectedMemoryIDs)),
			truncate(l.PromptPreview, 40),
		}
	}
	m.logTable.SetRows(rows)
}

func (m injectionModel) update(msg tea.Msg) (injectionModel, tea.Cmd) {
	switch msg := msg.(type) {
	case testPromptResultMsg:
		m.report = msg.report
		return m, nil

	case tea.KeyMsg:
		if m.inputFocus {
			switch {
			case key.Matches(msg, injectionKeyMap.Escape):
				m.inputFocus = false
				m.input.Blur()
				return m, nil
			case key.Matches(msg, injectionKeyMap.Enter):
				prompt := m.input.Value()
				if prompt != "" {
					return m, func() tea.Msg { return testPromptMsg{prompt: prompt} }
				}
				return m, nil
			}
			// Delegate to text input
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		if key.Matches(msg, injectionKeyMap.Focus) {
			m.inputFocus = true
			m.input.Focus()
			return m, textinput.Blink
		}
	}

	if !m.inputFocus {
		var cmd tea.Cmd
		m.logTable, cmd = m.logTable.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m injectionModel) selectedLog() *memoryloop.InjectionLog {
	cursor := m.logTable.Cursor()
	if m.state == nil || cursor < 0 || cursor >= len(m.state.InjectionLogs) {
		return nil
	}
	return &m.state.InjectionLogs[cursor]
}

func (m injectionModel) view() string {
	var b strings.Builder

	// Prompt tester section
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.sectionHeader, "PROMPT TESTER"))
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(m.input.View())
	b.WriteString("\n")

	// Results card showing all evaluated memories
	totalResults := len(m.report.Matches) + len(m.report.Skipped)
	if totalResults > 0 {
		var mb strings.Builder
		mb.WriteString(m.styles.render(m.styles.bold,
			fmt.Sprintf("%d would inject, %d skipped", len(m.report.Matches), len(m.report.Skipped))))
		mb.WriteString("\n")

		for i, match := range m.report.Matches {
			mb.WriteString("\n")
			fmt.Fprintf(&mb, "%s  %s",
				m.styles.render(m.styles.active, "INJECT"),
				m.styles.render(m.styles.title, match.Record.Title))
			if match.Reason != "" {
				fmt.Fprintf(&mb, "\n  %s", m.styles.render(m.styles.dim, match.Reason))
			}
			if i < len(m.report.Matches)-1 {
				mb.WriteString("\n")
			}
		}

		if len(m.report.Matches) > 0 && len(m.report.Skipped) > 0 {
			mb.WriteString("\n\n")
			mb.WriteString(m.styles.render(m.styles.dim, strings.Repeat("-", 40)))
		}

		for i, skipped := range m.report.Skipped {
			mb.WriteString("\n")
			fmt.Fprintf(&mb, "%s  %s",
				m.styles.render(m.styles.dim, "SKIP"),
				m.styles.render(m.styles.dim, skipped.Record.Title))
			if skipped.Reason != "" {
				fmt.Fprintf(&mb, "  %s", m.styles.render(m.styles.dim, "("+skipped.Reason+")"))
			}
			if i < len(m.report.Skipped)-1 {
				mb.WriteString("\n")
			}
		}

		cardStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("245")).
			Padding(1, 2)
		if m.width > 4 {
			cardStyle = cardStyle.Width(m.width - 4)
		}
		b.WriteString("\n")
		b.WriteString(cardStyle.Render(mb.String()))
		b.WriteString("\n")
	}

	// Injection logs
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.sectionHeader, "RECENT INJECTIONS"))
	b.WriteString("\n\n")

	if m.state == nil || len(m.state.InjectionLogs) == 0 {
		b.WriteString("  No injection logs yet. Memories inject when mode is auto.\n")
	} else {
		b.WriteString(m.logTable.View())

		// Detail view for selected log entry
		if log := m.selectedLog(); log != nil {
			var db strings.Builder
			db.WriteString(m.styles.render(m.styles.title, "Injection Detail"))
			db.WriteString("\n\n")
			fmt.Fprintf(&db, "%s  %s",
				m.styles.render(m.styles.dim, "Session:"),
				log.SessionID)
			fmt.Fprintf(&db, "\n%s     %s",
				m.styles.render(m.styles.dim, "Time:"),
				timeAgo(log.InjectedAt))
			if len(log.InjectedMemoryIDs) > 0 {
				fmt.Fprintf(&db, "\n%s  %s",
					m.styles.render(m.styles.dim, "Memories:"),
					strings.Join(log.InjectedMemoryIDs, ", "))
			}
			if log.Reason != "" {
				fmt.Fprintf(&db, "\n%s   %s",
					m.styles.render(m.styles.dim, "Reason:"),
					log.Reason)
			}
			if log.PromptPreview != "" {
				fmt.Fprintf(&db, "\n\n%s\n%s",
					m.styles.render(m.styles.dim, "Prompt:"),
					log.PromptPreview)
			}
			detailCard := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("245")).
				Padding(1, 2)
			if m.width > 4 {
				detailCard = detailCard.Width(m.width - 4)
			}
			b.WriteString("\n\n")
			b.WriteString(detailCard.Render(db.String()))
		}
	}

	return b.String()
}
