package summarytui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
)

//nolint:recvcheck // bubbletea-style model mixes pointer sizing with value update/view methods
type detailModel struct {
	styles      styles
	row         insightsdb.SessionRow
	viewport    viewport.Model
	width       int
	height      int
	canGenerate bool   // true when a generate function is available
	status      string // "Generating...", "Generated", "Error: ...", or ""
}

func newDetailModel(styles styles, row insightsdb.SessionRow, canGenerate bool) *detailModel {
	m := &detailModel{
		styles:      styles,
		row:         row,
		canGenerate: canGenerate,
	}
	m.setSize(100, 20)
	return m
}

func (m detailModel) view() string {
	var b strings.Builder
	b.WriteString(m.styles.render(m.styles.appTitle, "SESSION DETAIL"))
	b.WriteString("\n\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n\n")

	if m.status != "" {
		style := m.styles.chipActive
		if strings.HasPrefix(m.status, "Error:") {
			style = m.styles.errorText
		}
		b.WriteString(m.styles.render(style, m.status))
		b.WriteString("  ")
	}

	help := "j/k scroll  esc back  q quit"
	if m.canGenerate {
		help = "j/k scroll  g generate  esc back  q quit"
	}
	b.WriteString(m.styles.render(m.styles.statusBar, help))
	return b.String()
}

func (m detailModel) update(msg tea.Msg) (detailModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *detailModel) setSize(width, height int) {
	m.width = width
	m.height = height
	m.viewport = viewport.New(width, max(5, height-4))
	m.viewport.SetContent(m.renderContent())
}

func (m detailModel) renderContent() string {
	var b strings.Builder

	// Metadata section
	m.writeField(&b, "Agent", m.row.Agent)
	if m.row.OwnerID != "" {
		m.writeField(&b, "Author", m.row.OwnerID)
	}
	if m.row.OwnerName != "" {
		m.writeField(&b, "Author Name", m.row.OwnerName)
	}
	if m.row.OwnerEmail != "" {
		m.writeField(&b, "Author Email", m.row.OwnerEmail)
	}
	if m.row.Model != "" {
		m.writeField(&b, "Model", m.row.Model)
	}
	m.writeField(&b, "Session", m.row.SessionID)
	if m.row.Branch != "" {
		m.writeField(&b, "Branch", m.row.Branch)
	}
	m.writeField(&b, "Checkpoint", m.row.CheckpointID)
	m.writeField(&b, "Created", m.row.CreatedAt.Format("2006-01-02 15:04"))
	m.writeField(&b, "Tokens", strconv.Itoa(m.row.TotalTokens))
	m.writeField(&b, "Turns", strconv.Itoa(m.row.TurnCount))

	b.WriteString("\n")
	m.writeSectionHeader(&b, "Summary")
	b.WriteString(m.renderSummary())
	b.WriteString("\n\n")
	m.writeSectionHeader(&b, "Facets")
	b.WriteString(m.renderFacets())
	return b.String()
}

func (m detailModel) writeField(b *strings.Builder, label, value string) {
	b.WriteString(m.styles.render(m.styles.detailLabel, label))
	b.WriteString(m.styles.render(m.styles.detailValue, value))
	b.WriteString("\n")
}

func (m detailModel) writeSectionHeader(b *strings.Builder, title string) {
	header := fmt.Sprintf("─── %s ", title)
	header += strings.Repeat("─", max(0, 40-len(header)))
	b.WriteString(m.styles.render(m.styles.sectionHeader, header))
	b.WriteString("\n\n")
}

func (m detailModel) writeBullet(b *strings.Builder, text string) {
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.bullet, "•"))
	b.WriteString(" ")
	b.WriteString(text)
	b.WriteString("\n")
}

func (m detailModel) writeEmptyState(b *strings.Builder, text string) {
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.emptyState, text))
	b.WriteString("\n")
}

func (m detailModel) renderSummary() string {
	if !m.row.HasSummary {
		return m.styles.render(m.styles.emptyState, "  No summary cached")
	}

	var b strings.Builder
	m.writeField(&b, "Intent", fallback(m.row.Intent, "—"))
	m.writeField(&b, "Outcome", fallback(m.row.Outcome, "—"))
	b.WriteString("\n")

	b.WriteString(m.styles.render(m.styles.detailLabel, "Friction"))
	b.WriteString("\n")
	if len(m.row.Friction) == 0 {
		m.writeEmptyState(&b, "No friction recorded")
	} else {
		for _, item := range m.row.Friction {
			m.writeBullet(&b, item)
		}
	}

	b.WriteString("\n")
	b.WriteString(m.styles.render(m.styles.detailLabel, "Learnings"))
	b.WriteString("\n")
	if len(m.row.Learnings) == 0 {
		m.writeEmptyState(&b, "No learnings recorded")
	} else {
		for _, item := range m.row.Learnings {
			if item.Path != "" {
				m.writeBullet(&b, fmt.Sprintf("[%s] %s (%s)", item.Scope, item.Finding, item.Path))
				continue
			}
			m.writeBullet(&b, fmt.Sprintf("[%s] %s", item.Scope, item.Finding))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m detailModel) renderFacets() string {
	if !m.row.HasFacets {
		return m.styles.render(m.styles.emptyState, "  No facets cached")
	}

	var b strings.Builder

	m.writeFacetSection(&b, "Repeated Instructions", len(m.row.Facets.RepeatedUserInstructions), func() {
		for _, item := range m.row.Facets.RepeatedUserInstructions {
			m.writeBullet(&b, item.Instruction)
		}
	})

	m.writeFacetSection(&b, "Missing Context", len(m.row.Facets.MissingContext), func() {
		for _, item := range m.row.Facets.MissingContext {
			m.writeBullet(&b, item.Item)
		}
	})

	m.writeFacetSection(&b, "Failure Loops", len(m.row.Facets.FailureLoops), func() {
		for _, item := range m.row.Facets.FailureLoops {
			m.writeBullet(&b, fmt.Sprintf("%s (%d)", item.Description, item.Count))
		}
	})

	m.writeFacetSection(&b, "Skill Signals", len(m.row.Facets.SkillSignals), func() {
		for _, item := range m.row.Facets.SkillSignals {
			m.writeBullet(&b, item.SkillName)
		}
	})

	m.writeFacetSection(&b, "Review-Derived Rules", len(m.row.Facets.ReviewDerivedRules), func() {
		for _, item := range m.row.Facets.ReviewDerivedRules {
			m.writeBullet(&b, item.Rule)
		}
	})

	m.writeFacetSection(&b, "Repo Gotchas", len(m.row.Facets.RepoGotchas), func() {
		for _, item := range m.row.Facets.RepoGotchas {
			m.writeBullet(&b, item)
		}
	})

	m.writeFacetSection(&b, "Workflow Gaps", len(m.row.Facets.WorkflowGaps), func() {
		for _, item := range m.row.Facets.WorkflowGaps {
			m.writeBullet(&b, item)
		}
	})

	return strings.TrimRight(b.String(), "\n")
}

func (m detailModel) writeFacetSection(b *strings.Builder, title string, count int, writeItems func()) {
	b.WriteString(m.styles.render(m.styles.detailLabel, title))
	b.WriteString("\n")
	if count == 0 {
		m.writeEmptyState(b, "None")
	} else {
		writeItems()
	}
	b.WriteString("\n")
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}
