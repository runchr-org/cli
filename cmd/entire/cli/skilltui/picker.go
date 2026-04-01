package skilltui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/skilldb"
)

//nolint:recvcheck // bubbletea pattern: pointer receivers for mutation, value for update/view
type pickerModel struct {
	allSkills      []skilldb.SkillRow // full list before filtering
	skills         []skilldb.SkillRow // filtered view
	stats          map[string]*skilldb.SkillStatsResult
	populateResult *skilldb.PopulateResult // pipeline diagnostics for empty state
	selected       int
	scope          int // 0 = all, 1 = repo only, 2 = personal only
	styles         tuiStyles
	width          int
	height         int
}

func newPickerModel(styles tuiStyles) pickerModel {
	return pickerModel{
		styles: styles,
		stats:  make(map[string]*skilldb.SkillStatsResult),
	}
}

func (m *pickerModel) setData(skills []skilldb.SkillRow, stats map[string]*skilldb.SkillStatsResult, populateResult *skilldb.PopulateResult) {
	m.populateResult = populateResult
	// Deduplicate skills by name — the same skill may be discovered for
	// multiple source agents (e.g. claude-code and gemini-cli).
	// Keep one row per name and merge stats across agents.
	seen := make(map[string]int) // name -> index in deduped
	var deduped []skilldb.SkillRow
	merged := make(map[string]*skilldb.SkillStatsResult)

	for _, skill := range skills {
		key := skill.Name + "|" + skill.SourceAgent
		st := stats[key]

		if idx, ok := seen[skill.Name]; ok {
			// Merge stats into existing entry.
			if existing := merged[deduped[idx].Name]; existing != nil && st != nil {
				existing.TotalSessions += st.TotalSessions
				existing.TotalFriction += st.TotalFriction
				existing.TotalTokens += st.TotalTokens
				if !st.FirstUsed.IsZero() && (existing.FirstUsed.IsZero() || st.FirstUsed.Before(existing.FirstUsed)) {
					existing.FirstUsed = st.FirstUsed
				}
				if st.LastUsed.After(existing.LastUsed) {
					existing.LastUsed = st.LastUsed
				}
				if existing.TotalSessions > 0 {
					existing.SessionsPerWeek += st.SessionsPerWeek
					existing.AvgScore = (existing.AvgScore*float64(existing.TotalSessions-st.TotalSessions) +
						st.AvgScore*float64(st.TotalSessions)) / float64(existing.TotalSessions)
				}
			} else if existing == nil && st != nil {
				merged[deduped[idx].Name] = st
			}
			continue
		}

		seen[skill.Name] = len(deduped)
		deduped = append(deduped, skill)
		if st != nil {
			cp := *st
			merged[skill.Name] = &cp
		}
	}

	// Rekey stats by "name|sourceAgent" for the kept rows.
	m.stats = make(map[string]*skilldb.SkillStatsResult, len(deduped))
	for _, skill := range deduped {
		key := skill.Name + "|" + skill.SourceAgent
		m.stats[key] = merged[skill.Name]
	}

	m.allSkills = deduped
	m.applyFilter()
}

func (m *pickerModel) applyFilter() {
	if m.scope == 0 {
		m.skills = m.allSkills
	} else {
		scopeName := skilldb.ScopeRepo
		if m.scope == 2 {
			scopeName = skilldb.ScopePersonal
		}
		m.skills = nil
		for _, s := range m.allSkills {
			if s.Scope == scopeName {
				m.skills = append(m.skills, s)
			}
		}
	}
	if m.selected >= len(m.skills) {
		m.selected = max(0, len(m.skills)-1)
	}
}

func (m *pickerModel) setSize(w, h int) { m.width = w; m.height = h }

func (m pickerModel) update(msg tea.Msg) (pickerModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch {
	case key.Matches(keyMsg, pickerKeyMap.Up):
		if m.selected > 0 {
			m.selected--
		}
	case key.Matches(keyMsg, pickerKeyMap.Down):
		if m.selected < len(m.skills)-1 {
			m.selected++
		}
	case key.Matches(keyMsg, pickerKeyMap.Filter):
		m.scope = (m.scope + 1) % 3
		m.applyFilter()
	case key.Matches(keyMsg, pickerKeyMap.Enter):
		if len(m.skills) > 0 {
			skill := m.skills[m.selected]
			return m, func() tea.Msg { return skillSelectedMsg{skill: skill} }
		}
	}

	return m, nil
}

func (m pickerModel) hasAnySessionData() bool {
	for _, st := range m.stats {
		if st != nil && st.TotalSessions > 0 {
			return true
		}
	}
	return false
}

func (m pickerModel) view() string {
	var b strings.Builder

	b.WriteString(renderPickerHeader(m.styles))
	b.WriteString(m.renderFilterChips())
	b.WriteString("\n")

	if len(m.skills) == 0 {
		b.WriteString("  No skills found. Create skill files in .claude/skills/ or .gemini/agents/\n")
		return b.String()
	}

	// If all skills have zero sessions, show an actionable empty state with diagnostics.
	if !m.hasAnySessionData() {
		fmt.Fprintf(&b, "  %d skills discovered, but no session data found.\n\n", len(m.skills))

		// Show pipeline diagnostics if available.
		if pr := m.populateResult; pr != nil && (pr.Step1SignalCount > 0 || pr.Step2ToolCallCount > 0) {
			b.WriteString(m.styles.render(m.styles.sectionHeader, "  Diagnostics:"))
			b.WriteString("\n")
			fmt.Fprintf(&b, "    skill_signals: %d queried, %d resolved, %d inserted\n",
				pr.Step1SignalCount, pr.Step1Resolved, pr.Step1Inserted)
			fmt.Fprintf(&b, "    tool_calls:    %d queried, %d transcripts read, %d inserted\n",
				pr.Step2ToolCallCount, pr.Step2TranscriptsRead, pr.Step2Inserted)
			if len(pr.UnresolvedNames) > 0 {
				names := pr.UnresolvedNames
				if len(names) > 5 {
					names = names[:5]
				}
				fmt.Fprintf(&b, "    unresolved:    %s\n",
					m.styles.render(m.styles.dim, strings.Join(names, ", ")))
			}
			b.WriteString("\n")
		}

		b.WriteString(m.styles.render(m.styles.dim, "  To populate skill analytics:"))
		b.WriteString("\n")
		b.WriteString(m.styles.render(m.styles.dim, "    1. Run sessions with your agent (creates checkpoints)"))
		b.WriteString("\n")
		b.WriteString(m.styles.render(m.styles.dim, "    2. Run `entire insights` to extract skill signals"))
		b.WriteString("\n")
		b.WriteString(m.styles.render(m.styles.dim, "    3. Re-open this dashboard"))
		b.WriteString("\n\n")
		b.WriteString(m.styles.render(m.styles.sectionHeader, "  Skills found:"))
		b.WriteString("\n")
		for _, skill := range m.skills {
			fmt.Fprintf(&b, "    %s  %s\n",
				m.styles.render(m.styles.bold, skill.Name),
				m.styles.render(m.styles.dim, skill.SourceAgent))
		}
		return b.String()
	}

	// Column widths
	const colName = 24
	const colSessions = 10
	const colFreq = 12
	const colScore = 10

	// Column headers
	header := "  " + padRight("Name", colName) +
		padRight("Sessions", colSessions) + padRight("Freq", colFreq) + padRight("Score", colScore)
	b.WriteString(m.styles.render(m.styles.dim, header))
	b.WriteString("\n")
	b.WriteString(m.styles.render(m.styles.dim, "  "+strings.Repeat("\u2500", colName+colSessions+colFreq+colScore)))
	b.WriteString("\n")

	for i, skill := range m.skills {
		statsKey := skill.Name + "|" + skill.SourceAgent
		st := m.stats[statsKey]

		isSelected := i == m.selected
		marker := "  "
		if isSelected {
			marker = m.styles.render(m.styles.selected, "\u25b8 ")
		}

		sessions := 0
		freqStr := m.styles.render(m.styles.dim, "0.0/wk")
		scoreStr := m.styles.render(m.styles.dim, "\u2500")
		if st != nil {
			sessions = st.TotalSessions
			freqStr = formatFrequency(m.styles, st.SessionsPerWeek)
			if st.TotalSessions > 0 {
				scoreStr = fmt.Sprintf("%.0f", st.AvgScore)
			}
		}

		// Render name with style, then pad to visible width.
		var nameStr string
		if isSelected {
			nameStr = m.styles.render(m.styles.selectedRow, skill.Name)
		} else {
			nameStr = skill.Name
		}

		sessStr := strconv.Itoa(sessions)
		if sessions == 0 {
			sessStr = m.styles.render(m.styles.dim, "0")
		}

		line := marker +
			padRight(nameStr, colName) +
			padRight(sessStr, colSessions) +
			padRight(freqStr, colFreq) +
			padRight(scoreStr, colScore)

		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func (m pickerModel) renderFilterChips() string {
	labels := [3]string{"All", "Repo", "Personal"}
	var chips []string
	for i, label := range labels {
		if i == m.scope {
			chips = append(chips, m.styles.render(m.styles.chipActive, "[ "+label+" ]"))
		} else {
			chips = append(chips, m.styles.render(m.styles.chipDisabled, "  "+label+"  "))
		}
	}
	return "  " + strings.Join(chips, " ") + "\n"
}

func formatFrequency(s tuiStyles, perWeek float64) string {
	rate := fmt.Sprintf("%.1f/wk", perWeek)
	switch {
	case perWeek > 1.0:
		return s.render(s.success, "\u25b2") + " " + rate
	case perWeek < 0.5:
		return s.render(s.friction, "\u25bc") + " " + rate
	default:
		return s.render(s.dim, "\u2500") + " " + rate
	}
}
