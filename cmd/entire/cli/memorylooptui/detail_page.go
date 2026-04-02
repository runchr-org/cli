package memorylooptui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

type memoryDetailModel struct {
	styles tuiStyles
	width  int
	height int
	record memoryloop.MemoryRecord
	wizard wizardModel
}

func newMemoryDetailModel(styles tuiStyles, record memoryloop.MemoryRecord, resolve wizardResolver) memoryDetailModel {
	wizard := newWizardModel(styles, record, resolve)
	return memoryDetailModel{
		styles: styles,
		record: record,
		wizard: wizard,
	}
}

func (m *memoryDetailModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.wizard.setSize(w, h)
}

func (m *memoryDetailModel) update(msg tea.Msg) (memoryDetailModel, tea.Cmd) {
	nextWizard, cmd := m.wizard.update(msg)
	m.wizard = nextWizard
	return *m, cmd
}

func (m *memoryDetailModel) hints() string {
	return m.wizard.hints()
}

func (m *memoryDetailModel) view() string {
	var b strings.Builder

	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.bold, "MEMORY DETAIL"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, "Esc returns to the memories table"))
	b.WriteString("\n\n")
	b.WriteString(m.renderSummaryCard())
	b.WriteString("\n\n")
	b.WriteString(m.renderContentCard())
	b.WriteString("\n\n")
	b.WriteString(m.renderActionCard())
	b.WriteString("\n\n  ")
	b.WriteString(m.styles.render(m.styles.dim, m.hints()))
	b.WriteString("\n")

	return b.String()
}

func (m *memoryDetailModel) renderSummaryCard() string {
	var body strings.Builder

	body.WriteString(m.styles.render(m.styles.title, m.record.Title))
	body.WriteString("\n\n")
	body.WriteString(kindStyle(m.styles, m.record.Kind)(string(m.record.Kind)))
	body.WriteString("  ")
	body.WriteString(statusDot(m.styles, m.record.Status))
	body.WriteString(" ")
	body.WriteString(string(m.record.Status))
	body.WriteString("  ")
	body.WriteString(m.styles.render(m.styles.dim, formatMemoryScope(m.record)))
	body.WriteString("  ")
	body.WriteString(m.styles.render(m.styles.dim, string(m.record.Origin)))

	body.WriteString("\n\n")
	body.WriteString(renderStrengthBar(m.record.Strength))
	body.WriteString("  ")
	fmt.Fprintf(&body, "strength %d/5", m.record.Strength)
	body.WriteString("  ")
	body.WriteString(m.styles.render(m.styles.dim, fmt.Sprintf("outcome %s", m.record.Outcome)))

	body.WriteString("\n")
	body.WriteString(m.styles.render(m.styles.dim, fmt.Sprintf(
		"injected %dx  matched %dx  updated %s  created %s",
		m.record.InjectCount,
		m.record.MatchCount,
		timeAgo(m.record.UpdatedAt),
		timeAgo(m.record.CreatedAt),
	)))

	if !m.record.LastInjectedAt.IsZero() {
		body.WriteString("\n")
		body.WriteString(m.styles.render(m.styles.dim, "last injected "+timeAgo(m.record.LastInjectedAt)))
	}

	return m.renderCard("Summary", body.String())
}

func (m *memoryDetailModel) renderContentCard() string {
	var body strings.Builder

	if m.record.Body != "" {
		body.WriteString(m.record.Body)
	} else {
		body.WriteString(m.styles.render(m.styles.dim, "No memory body recorded."))
	}

	body.WriteString("\n\n")
	body.WriteString(m.styles.render(m.styles.sectionHeader, "WHY"))
	body.WriteString("\n")
	if m.record.Why != "" {
		body.WriteString(m.record.Why)
	} else {
		body.WriteString(m.styles.render(m.styles.dim, "No rationale recorded."))
	}

	if m.record.SkillName != "" || m.record.SkillPath != "" {
		body.WriteString("\n\n")
		body.WriteString(m.styles.render(m.styles.sectionHeader, "SKILL"))
		body.WriteString("\n")
		if m.record.SkillName != "" {
			fmt.Fprintf(&body, "name: %s\n", m.record.SkillName)
		}
		if m.record.SkillPath != "" {
			fmt.Fprintf(&body, "path: %s", m.record.SkillPath)
		}
	}

	return m.renderCard("Memory", body.String())
}

func (m *memoryDetailModel) renderActionCard() string {
	var content string
	switch m.wizard.stage {
	case wizardStageAction:
		content = strings.TrimSpace(m.wizard.renderActionSelectionList())
	case wizardStageScope:
		content = strings.TrimSpace(m.wizard.renderScopeSelectionList())
	case wizardStageLocation:
		content = strings.TrimSpace(m.wizard.renderLocationSelectionList())
	case wizardStagePreview:
		content = strings.TrimSpace(m.wizard.renderPreview())
	default:
		content = ""
	}

	return m.renderCard("Actions", content)
}

func (m *memoryDetailModel) renderCard(title string, content string) string {
	contentWidth := m.width - 8
	if contentWidth < 32 {
		contentWidth = 32
	}

	var body strings.Builder
	body.WriteString(m.styles.render(m.styles.sectionHeader, strings.ToUpper(title)))
	body.WriteString("\n\n")
	body.WriteString(content)

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("245")).
		Padding(1, 2).
		Width(contentWidth).
		MarginLeft(2)

	if !m.styles.colorEnabled {
		style = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2).
			Width(contentWidth).
			MarginLeft(2)
	}

	return style.Render(body.String())
}

func formatMemoryScope(record memoryloop.MemoryRecord) string {
	if record.ScopeValue == "" {
		return string(record.ScopeKind)
	}
	return fmt.Sprintf("%s:%s", record.ScopeKind, record.ScopeValue)
}
