package skilltui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/skilldb"
	"github.com/entireio/cli/cmd/entire/cli/skillimprove"
)

//nolint:recvcheck // bubbletea pattern: pointer receivers for mutation, value for update/view
type improveModel struct {
	suggestions  []skillimprove.SkillSuggestion
	improvements []skilldb.SkillImprovement
	selected     int
	isGenerating bool
	spinner      spinner.Model
	styles       tuiStyles
	width        int
	height       int
}

func newImproveModel(styles tuiStyles) improveModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	return improveModel{
		styles:  styles,
		spinner: s,
	}
}

func (m *improveModel) setData(improvements []skilldb.SkillImprovement) {
	m.improvements = improvements
	m.suggestions = nil // Clear stale suggestions from previous skill.
	m.selected = 0
}

func (m *improveModel) setSize(w, h int) { m.width = w; m.height = h }

func (m improveModel) update(msg tea.Msg) (improveModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.isGenerating {
			return m, nil
		}

		switch {
		case key.Matches(msg, pickerKeyMap.Up):
			if m.selected > 0 {
				m.selected--
			}
		case key.Matches(msg, pickerKeyMap.Down):
			if m.selected < len(m.suggestions)-1 {
				m.selected++
			}
		case key.Matches(msg, improveKeyMap.Generate):
			return m, func() tea.Msg { return generateStartedMsg{} }
		case key.Matches(msg, improveKeyMap.Apply):
			if len(m.suggestions) > 0 && m.selected < len(m.suggestions) {
				idx := m.selected
				return m, func() tea.Msg { return applyDiffMsg{index: idx} }
			}
		case key.Matches(msg, improveKeyMap.Dismiss):
			if len(m.suggestions) > 0 && m.selected < len(m.suggestions) {
				idx := m.selected
				return m, func() tea.Msg { return dismissSuggestionMsg{index: idx} }
			}
		}

	case spinner.TickMsg:
		if m.isGenerating {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

func (m improveModel) view() string {
	var b strings.Builder

	// Generating state
	if m.isGenerating {
		fmt.Fprintf(&b, "  %s Analyzing skill usage data and generating suggestions...\n",
			m.spinner.View())
		return b.String()
	}

	// No suggestions state
	if len(m.suggestions) == 0 {
		b.WriteString("  No improvement suggestions yet.\n\n")
		b.WriteString("  Press 'g' to analyze usage data and generate suggestions.\n")

		// Show history if available
		if len(m.improvements) > 0 {
			b.WriteString("\n")
			b.WriteString(m.renderHistory())
		}
		return b.String()
	}

	// Suggestions list
	fmt.Fprintf(&b, "  %s\n",
		m.styles.render(m.styles.sectionHeader, fmt.Sprintf("Suggestions (%d)", len(m.suggestions))))
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, strings.Repeat("\u2500", 40)))
	b.WriteString("\n\n")

	for i, sug := range m.suggestions {
		isSelected := i == m.selected

		marker := "  "
		if isSelected {
			marker = m.styles.render(m.styles.selected, "\u25b8 ")
		}

		priorityBadge := m.renderPriority(sug.Priority)
		fmt.Fprintf(&b, "%s%s %s\n", marker, priorityBadge, sug.Title)

		// Show detail only for selected suggestion
		if isSelected {
			// Description
			if sug.Description != "" {
				wrapped := wrapText(sug.Description, m.width-6)
				for _, line := range strings.Split(wrapped, "\n") {
					fmt.Fprintf(&b, "    %s\n", m.styles.render(m.styles.dim, line))
				}
				b.WriteString("\n")
			}

			// Diff
			if sug.Diff != "" {
				b.WriteString(m.renderDiff(sug.Diff))
				b.WriteString("\n")
			}

			// Evidence
			if len(sug.Evidence) > 0 {
				fmt.Fprintf(&b, "    Evidence: %d sessions\n", len(sug.Evidence))
			}

			fmt.Fprintf(&b, "    %s\n", m.styles.render(m.styles.dim, "a: apply \u00b7 d: dismiss"))
		}
		b.WriteString("\n")
	}

	// History section
	if len(m.improvements) > 0 {
		b.WriteString(m.renderHistory())
	}

	return b.String()
}

func (m improveModel) renderPriority(priority string) string {
	switch strings.ToLower(priority) {
	case "high":
		return m.styles.render(m.styles.priorityHigh, "[HIGH]")
	case "medium":
		return m.styles.render(m.styles.priorityMedium, "[MED]")
	case "low":
		return m.styles.render(m.styles.priorityLow, "[LOW]")
	default:
		return m.styles.render(m.styles.dim, fmt.Sprintf("[%s]", strings.ToUpper(priority)))
	}
}

func (m improveModel) renderDiff(diff string) string {
	var b strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		prefix := "    "
		switch {
		case strings.HasPrefix(line, "+"):
			b.WriteString(prefix + m.styles.render(m.styles.diffAdd, "+ "+line[1:]) + "\n")
		case strings.HasPrefix(line, "-"):
			b.WriteString(prefix + m.styles.render(m.styles.diffRemove, "- "+line[1:]) + "\n")
		case strings.HasPrefix(line, "@@"):
			b.WriteString(prefix + m.styles.render(m.styles.dim, line) + "\n")
		default:
			b.WriteString(prefix + m.styles.render(m.styles.dim, "  "+line) + "\n")
		}
	}
	return b.String()
}

func (m improveModel) renderHistory() string {
	var b strings.Builder

	applied := filterByStatus(m.improvements, "applied")
	if len(applied) > 0 {
		fmt.Fprintf(&b, "  %s\n",
			m.styles.render(m.styles.sectionHeader, fmt.Sprintf("Applied (%d)", len(applied))))
		b.WriteString("  ")
		b.WriteString(m.styles.render(m.styles.dim, strings.Repeat("\u2500", 30)))
		b.WriteString("\n")

		for _, imp := range applied {
			ago := "unknown"
			if imp.AppliedAt != nil {
				ago = timeAgo(*imp.AppliedAt)
			}
			fmt.Fprintf(&b, "  %s %-40s applied %s\n",
				m.styles.render(m.styles.success, "\u2713"),
				imp.Title,
				m.styles.render(m.styles.dim, ago))
		}
	}

	return b.String()
}

func filterByStatus(improvements []skilldb.SkillImprovement, status string) []skilldb.SkillImprovement {
	var result []skilldb.SkillImprovement
	for _, imp := range improvements {
		if imp.Status == status {
			result = append(result, imp)
		}
	}
	return result
}

func wrapText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		maxWidth = 80
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	var lines []string
	currentLine := words[0]

	for _, word := range words[1:] {
		if len(currentLine)+1+len(word) > maxWidth {
			lines = append(lines, currentLine)
			currentLine = word
		} else {
			currentLine += " " + word
		}
	}
	lines = append(lines, currentLine)

	return strings.Join(lines, "\n")
}
