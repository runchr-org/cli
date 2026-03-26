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
	matches    []memoryloop.Match
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
		Foreground(lipgloss.Color("6")).
		Bold(false)
	t.SetStyles(st)

	ti := textinput.New()
	ti.Placeholder = "type a prompt to test memory matching..."
	ti.Prompt = "> "
	ti.Width = 60

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
	tableH := h - 10 // Reserve space for input + matches
	if tableH < 3 {
		tableH = 3
	}
	m.logTable.SetWidth(w)
	m.logTable.SetHeight(tableH)
	m.input.Width = w - 6
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
		m.matches = msg.matches
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

func (m injectionModel) view() string {
	var b strings.Builder

	// Prompt tester section
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, "PROMPT TESTER"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(m.input.View())
	b.WriteString("\n")

	// Match results
	if len(m.matches) > 0 {
		b.WriteString("\n  ")
		b.WriteString(m.styles.render(m.styles.dim, fmt.Sprintf("MATCHES (%d)", len(m.matches))))
		b.WriteString("\n")
		for _, match := range m.matches {
			fmt.Fprintf(&b, "  %s  %s\n",
				m.styles.render(m.styles.title, match.Record.Title),
				m.styles.render(m.styles.active, fmt.Sprintf("score: %d", match.Score)))
			if match.Reason != "" {
				fmt.Fprintf(&b, "    %s\n", m.styles.render(m.styles.dim, match.Reason))
			}
		}
	}

	// Injection logs
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.dim, "RECENT INJECTIONS"))
	b.WriteString("\n")

	if m.state == nil || len(m.state.InjectionLogs) == 0 {
		b.WriteString("  No injection logs yet. Memories inject when mode is auto.\n")
	} else {
		b.WriteString(m.logTable.View())
	}

	return b.String()
}
