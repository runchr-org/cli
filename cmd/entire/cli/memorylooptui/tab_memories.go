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

type statusFilter int

const (
	filterAll statusFilter = iota
	filterActive
	filterCandidate
	filterSuppressed
	filterArchived
)

var filterLabels = [5]string{"ALL", "ACTIVE", "CANDIDATE", "SUPPRESSED", "ARCHIVED"}

//nolint:recvcheck // bubbletea pattern: pointer receivers for mutation, value for update/view
type memoriesModel struct {
	state      *memoryloop.State
	styles     tuiStyles
	width      int
	height     int
	table      table.Model
	filter     statusFilter
	showDetail bool
	searchMode bool
	searchText string
	records    []memoryloop.MemoryRecord // filtered subset
	addMode    bool
	addFields  [4]textinput.Model // kind, title, body, scope
	addFocus   int
}

func newMemoriesModel(s tuiStyles) memoriesModel {
	columns := []table.Column{
		{Title: " ", Width: 2},
		{Title: "Title", Width: 30},
		{Title: "Kind", Width: 14},
		{Title: "Scope", Width: 5},
		{Title: "Str", Width: 5},
		{Title: "Outcome", Width: 11},
		{Title: "Inj", Width: 4},
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

	return memoriesModel{
		styles:     s,
		table:      t,
		showDetail: true,
	}
}

// capturesInput returns true when the memories tab is handling keyboard input
// internally (add form or search mode), so the root model should skip global keys.
func (m memoriesModel) capturesInput() bool {
	return m.addMode || m.searchMode
}

func newAddFields() [4]textinput.Model {
	kind := textinput.New()
	kind.Placeholder = "repo_rule | workflow_rule | agent_instruction | skill_patch | anti_pattern"
	kind.Prompt = "Kind: "
	kind.Width = 60

	title := textinput.New()
	title.Placeholder = "memory title"
	title.Prompt = "Title: "
	title.Width = 60

	body := textinput.New()
	body.Placeholder = "memory body"
	body.Prompt = "Body: "
	body.Width = 60

	scope := textinput.New()
	scope.Placeholder = "me | repo"
	scope.Prompt = "Scope: "
	scope.Width = 20
	scope.SetValue("me")

	return [4]textinput.Model{kind, title, body, scope}
}

func (m *memoriesModel) setState(state *memoryloop.State) {
	m.state = state
	m.rebuildTable()
}

func (m *memoriesModel) setSize(w, h int) {
	m.width = w
	m.height = h
	// Reserve 3 lines for filter bar + detail pane header area
	tableH := h - 3
	if m.showDetail {
		tableH = h / 2
	}
	if tableH < 3 {
		tableH = 3
	}
	m.table.SetWidth(w)
	m.table.SetHeight(tableH)
}

func (m *memoriesModel) rebuildTable() {
	if m.state == nil || m.state.Store == nil {
		m.records = nil
		m.table.SetRows(nil)
		return
	}

	var filtered []memoryloop.MemoryRecord
	for _, r := range m.state.Store.Records {
		if !m.matchesFilter(r) {
			continue
		}
		if m.searchMode && m.searchText != "" {
			if !strings.Contains(strings.ToLower(r.Title), strings.ToLower(m.searchText)) {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	m.records = filtered

	rows := make([]table.Row, len(filtered))
	for i, r := range filtered {
		rows[i] = table.Row{
			statusDotPlain(r.Status),
			truncate(r.Title, 30),
			string(r.Kind),
			string(r.ScopeKind),
			renderStrengthBar(r.Strength),
			string(r.Outcome),
			strconv.Itoa(r.InjectCount),
		}
	}
	m.table.SetRows(rows)
}

func (m memoriesModel) matchesFilter(r memoryloop.MemoryRecord) bool {
	switch m.filter {
	case filterAll:
		return true
	case filterActive:
		return r.Status == memoryloop.StatusActive
	case filterCandidate:
		return r.Status == memoryloop.StatusCandidate
	case filterSuppressed:
		return r.Status == memoryloop.StatusSuppressed
	case filterArchived:
		return r.Status == memoryloop.StatusArchived
	default:
		return true
	}
}

func (m memoriesModel) selectedRecord() *memoryloop.MemoryRecord {
	cursor := m.table.Cursor()
	if cursor < 0 || cursor >= len(m.records) {
		return nil
	}
	return &m.records[cursor]
}

func (m memoriesModel) update(msg tea.Msg) (memoriesModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if m.addMode {
			return m.updateAddForm(keyMsg)
		}

		if m.searchMode {
			return m.updateSearch(keyMsg)
		}

		switch {
		case key.Matches(keyMsg, memoriesKeyMap.New):
			m.addMode = true
			m.addFields = newAddFields()
			m.addFocus = 0
			m.addFields[0].Focus()
			return m, textinput.Blink

		case key.Matches(keyMsg, memoriesKeyMap.Filter):
			m.filter = (m.filter + 1) % 5
			m.rebuildTable()
			return m, nil

		case key.Matches(keyMsg, memoriesKeyMap.Search):
			m.searchMode = true
			m.searchText = ""
			return m, nil

		case key.Matches(keyMsg, memoriesKeyMap.Enter):
			m.showDetail = !m.showDetail
			m.setSize(m.width, m.height)
			return m, nil

		case key.Matches(keyMsg, memoriesKeyMap.Activate):
			if r := m.selectedRecord(); r != nil {
				return m, func() tea.Msg {
					return lifecycleActionMsg{id: r.ID, action: memoryloop.LifecycleActionActivate}
				}
			}

		case key.Matches(keyMsg, memoriesKeyMap.Promote):
			if r := m.selectedRecord(); r != nil {
				return m, func() tea.Msg {
					return lifecycleActionMsg{id: r.ID, action: memoryloop.LifecycleActionPromote}
				}
			}

		case key.Matches(keyMsg, memoriesKeyMap.Suppress):
			if r := m.selectedRecord(); r != nil {
				return m, func() tea.Msg {
					return lifecycleActionMsg{id: r.ID, action: memoryloop.LifecycleActionSuppress}
				}
			}

		case key.Matches(keyMsg, memoriesKeyMap.Unsuppress):
			if r := m.selectedRecord(); r != nil {
				return m, func() tea.Msg {
					return lifecycleActionMsg{id: r.ID, action: memoryloop.LifecycleActionUnsuppress}
				}
			}

		case key.Matches(keyMsg, memoriesKeyMap.Archive):
			if r := m.selectedRecord(); r != nil {
				return m, func() tea.Msg {
					return lifecycleActionMsg{id: r.ID, action: memoryloop.LifecycleActionArchive}
				}
			}

		case key.Matches(keyMsg, memoriesKeyMap.Prune):
			return m, func() tea.Msg { return pruneMsg{} }
		}
	}

	// Delegate to table for navigation
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m memoriesModel) updateSearch(msg tea.KeyMsg) (memoriesModel, tea.Cmd) {
	switch {
	case key.Matches(msg, memoriesKeyMap.Escape):
		m.searchMode = false
		m.searchText = ""
		m.rebuildTable()
		return m, nil
	case msg.Type == tea.KeyEnter:
		m.searchMode = false
		return m, nil
	case msg.Type == tea.KeyBackspace:
		if len(m.searchText) > 0 {
			runes := []rune(m.searchText)
			m.searchText = string(runes[:len(runes)-1])
			m.rebuildTable()
		}
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.searchText += string(msg.Runes)
			m.rebuildTable()
		}
		return m, nil
	}
}

func (m memoriesModel) updateAddForm(msg tea.KeyMsg) (memoriesModel, tea.Cmd) {
	switch {
	case key.Matches(msg, memoriesKeyMap.Escape):
		m.addMode = false
		return m, nil

	case msg.String() == "tab":
		// Blur current, advance focus, focus next
		m.addFields[m.addFocus].Blur()
		m.addFocus = (m.addFocus + 1) % len(m.addFields)
		m.addFields[m.addFocus].Focus()
		return m, textinput.Blink

	case msg.String() == "shift+tab":
		m.addFields[m.addFocus].Blur()
		m.addFocus = (m.addFocus + len(m.addFields) - 1) % len(m.addFields)
		m.addFields[m.addFocus].Focus()
		return m, textinput.Blink

	case msg.String() == "enter":
		if m.addFocus < len(m.addFields)-1 {
			// Not on last field -- advance to next
			m.addFields[m.addFocus].Blur()
			m.addFocus++
			m.addFields[m.addFocus].Focus()
			return m, textinput.Blink
		}
		// On last field -- submit
		input := memoryloop.ManualRecordInput{
			Kind:       memoryloop.Kind(m.addFields[0].Value()),
			Title:      m.addFields[1].Value(),
			Body:       m.addFields[2].Value(),
			ScopeKind:  memoryloop.ScopeKind(m.addFields[3].Value()),
			ScopeValue: "",
		}
		m.addMode = false
		return m, func() tea.Msg { return addMemoryMsg{input: input} }
	}

	// Delegate to the focused text input for character input
	var cmd tea.Cmd
	m.addFields[m.addFocus], cmd = m.addFields[m.addFocus].Update(msg)
	return m, cmd
}

func (m memoriesModel) view() string {
	if m.addMode {
		return m.renderAddForm()
	}

	if m.state == nil || m.state.Store == nil {
		return "\n  No memory store found. Switch to History tab and press R to refresh.\n"
	}
	if len(m.state.Store.Records) == 0 {
		return "\n  No memories yet. Press n to add one, or switch to History tab and press R to refresh.\n"
	}
	if len(m.records) == 0 {
		return fmt.Sprintf("\n  No %s memories. Press f to change filter.\n", filterLabels[m.filter])
	}

	var b strings.Builder

	// Blank line for spacing between tab bar and filter bar
	b.WriteString("\n")

	// Filter bar
	b.WriteString(m.renderFilterBar())
	b.WriteString("\n")

	// Search bar (if active)
	if m.searchMode {
		fmt.Fprintf(&b, "  / %s\u2588\n", m.searchText)
	}

	// Table
	b.WriteString(m.table.View())

	// Detail pane with spacing above
	if m.showDetail {
		b.WriteString("\n\n")
		b.WriteString(m.renderDetail())
	}

	return b.String()
}

func (m memoriesModel) renderAddForm() string {
	var b strings.Builder
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.title, "NEW MEMORY"))
	b.WriteString("\n\n")
	for i := range m.addFields {
		b.WriteString("  ")
		b.WriteString(m.addFields[i].View())
		b.WriteString("\n")
	}
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.dim, "Tab next field | Enter submit | Esc cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m memoriesModel) renderFilterBar() string {
	counts := m.statusCounts()
	var parts []string
	for i, label := range filterLabels {
		text := fmt.Sprintf("%s (%d)", label, counts[i])
		if m.styles.colorEnabled {
			if statusFilter(i) == m.filter {
				parts = append(parts, m.styles.filterChipActive.Render(text))
			} else {
				parts = append(parts, m.styles.filterChipInactive.Render(text))
			}
		} else {
			if statusFilter(i) == m.filter {
				parts = append(parts, "["+text+"]")
			} else {
				parts = append(parts, " "+text+" ")
			}
		}
	}
	return "  " + strings.Join(parts, " ")
}

func (m memoriesModel) statusCounts() [5]int {
	var counts [5]int
	if m.state == nil || m.state.Store == nil {
		return counts
	}
	for _, r := range m.state.Store.Records {
		counts[0]++ // all
		switch r.Status {
		case memoryloop.StatusActive:
			counts[1]++
		case memoryloop.StatusCandidate:
			counts[2]++
		case memoryloop.StatusSuppressed:
			counts[3]++
		case memoryloop.StatusArchived:
			counts[4]++
		}
	}
	return counts
}

func (m memoriesModel) renderDetail() string {
	r := m.selectedRecord()
	if r == nil {
		return ""
	}

	var out strings.Builder
	out.WriteString("  ")
	out.WriteString(m.styles.render(m.styles.sectionHeader, "DETAILS"))
	out.WriteString("\n")

	var b strings.Builder
	// Title line
	b.WriteString(m.styles.render(m.styles.title, r.Title))
	b.WriteString("  ")
	b.WriteString(kindStyle(m.styles, r.Kind)(string(r.Kind)))
	b.WriteString("  ")
	b.WriteString(statusDot(m.styles, r.Status))
	b.WriteString(" ")
	b.WriteString(string(r.Status))
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, string(r.ScopeKind)))
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, string(r.Origin)))
	b.WriteString("\n")

	// Body
	if r.Body != "" {
		b.WriteString(r.Body + "\n")
	}

	// Why
	if r.Why != "" {
		b.WriteString(m.styles.render(m.styles.dim, "WHY"))
		b.WriteString("\n")
		b.WriteString(r.Why + "\n")
	}

	// Stats
	stats := fmt.Sprintf("strength: %d/5 · injected: %dx · matched: %dx · last injected: %s · created: %s",
		r.Strength, r.InjectCount, r.MatchCount, timeAgo(r.LastInjectedAt), timeAgo(r.CreatedAt))
	b.WriteString(m.styles.render(m.styles.dim, stats))

	// Wrap in bordered card
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("245")).
		Padding(0, 1).
		Width(m.width - 2)
	out.WriteString(cardStyle.Render(b.String()))
	return out.String()
}

func statusDotPlain(status memoryloop.Status) string {
	switch status {
	case memoryloop.StatusActive:
		return "\u25cf"
	case memoryloop.StatusCandidate:
		return "\u25cb"
	case memoryloop.StatusSuppressed:
		return "\u2715"
	case memoryloop.StatusArchived:
		return "\u25cc"
	default:
		return " "
	}
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "\u2026"
}
