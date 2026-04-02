package summarytui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
)

//nolint:recvcheck // bubbletea-style model mixes pointer sizing with value update/view methods
type detailModel struct {
	styles   styles
	row      insightsdb.SessionRow
	viewport viewport.Model
	width    int
	height   int
}

func newDetailModel(styles styles, row insightsdb.SessionRow) *detailModel {
	m := &detailModel{
		styles: styles,
		row:    row,
	}
	m.setSize(100, 20)
	return m
}

func (m detailModel) view() string {
	var b strings.Builder
	b.WriteString("SESSION DETAIL\n\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n\nEsc returns to the session table")
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

	fmt.Fprintf(&b, "Agent: %s\n", m.row.Agent)
	if m.row.OwnerID != "" {
		fmt.Fprintf(&b, "Author: %s\n", m.row.OwnerID)
	}
	if m.row.OwnerName != "" {
		fmt.Fprintf(&b, "Author Name: %s\n", m.row.OwnerName)
	}
	if m.row.OwnerEmail != "" {
		fmt.Fprintf(&b, "Author Email: %s\n", m.row.OwnerEmail)
	}
	if m.row.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", m.row.Model)
	}
	fmt.Fprintf(&b, "Session: %s\n", m.row.SessionID)
	if m.row.Branch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", m.row.Branch)
	}
	fmt.Fprintf(&b, "Checkpoint: %s\n", m.row.CheckpointID)
	fmt.Fprintf(&b, "Created: %s\n", m.row.CreatedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "Tokens: %d\n", m.row.TotalTokens)
	fmt.Fprintf(&b, "Turns: %d\n\n", m.row.TurnCount)

	b.WriteString("Summary\n")
	b.WriteString(m.renderSummary())
	b.WriteString("\n\n")
	b.WriteString("Facets\n")
	b.WriteString(m.renderFacets())
	return b.String()
}

func (m detailModel) renderSummary() string {
	if !m.row.HasSummary {
		return "No summary cached"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Intent: %s\n", fallback(m.row.Intent, "No summary cached"))
	fmt.Fprintf(&b, "Outcome: %s\n", fallback(m.row.Outcome, "No summary cached"))
	b.WriteString("Friction:\n")
	if len(m.row.Friction) == 0 {
		b.WriteString("  No friction recorded\n")
	} else {
		for _, item := range m.row.Friction {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}

	b.WriteString("Learnings:\n")
	if len(m.row.Learnings) == 0 {
		b.WriteString("  No learnings recorded\n")
	} else {
		for _, item := range m.row.Learnings {
			if item.Path != "" {
				fmt.Fprintf(&b, "  - [%s] %s (%s)\n", item.Scope, item.Finding, item.Path)
				continue
			}
			fmt.Fprintf(&b, "  - [%s] %s\n", item.Scope, item.Finding)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m detailModel) renderFacets() string {
	if !m.row.HasFacets {
		return "No facets cached"
	}

	var b strings.Builder
	b.WriteString("Repeated Instructions:\n")
	if len(m.row.Facets.RepeatedUserInstructions) == 0 {
		b.WriteString("  None\n")
	} else {
		for _, item := range m.row.Facets.RepeatedUserInstructions {
			fmt.Fprintf(&b, "  - %s\n", item.Instruction)
		}
	}

	b.WriteString("Missing Context:\n")
	if len(m.row.Facets.MissingContext) == 0 {
		b.WriteString("  None\n")
	} else {
		for _, item := range m.row.Facets.MissingContext {
			fmt.Fprintf(&b, "  - %s\n", item.Item)
		}
	}

	b.WriteString("Failure Loops:\n")
	if len(m.row.Facets.FailureLoops) == 0 {
		b.WriteString("  None\n")
	} else {
		for _, item := range m.row.Facets.FailureLoops {
			fmt.Fprintf(&b, "  - %s (%d)\n", item.Description, item.Count)
		}
	}

	b.WriteString("Skill Signals:\n")
	if len(m.row.Facets.SkillSignals) == 0 {
		b.WriteString("  None\n")
	} else {
		for _, item := range m.row.Facets.SkillSignals {
			fmt.Fprintf(&b, "  - %s\n", item.SkillName)
		}
	}

	b.WriteString("Review-Derived Rules:\n")
	if len(m.row.Facets.ReviewDerivedRules) == 0 {
		b.WriteString("  None\n")
	} else {
		for _, item := range m.row.Facets.ReviewDerivedRules {
			fmt.Fprintf(&b, "  - %s\n", item.Rule)
		}
	}

	b.WriteString("Repo Gotchas:\n")
	if len(m.row.Facets.RepoGotchas) == 0 {
		b.WriteString("  None\n")
	} else {
		for _, item := range m.row.Facets.RepoGotchas {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}

	b.WriteString("Workflow Gaps:\n")
	if len(m.row.Facets.WorkflowGaps) == 0 {
		b.WriteString("  None\n")
	} else {
		for _, item := range m.row.Facets.WorkflowGaps {
			fmt.Fprintf(&b, "  - %s\n", item)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}
