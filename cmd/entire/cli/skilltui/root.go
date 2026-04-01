package skilltui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/skilldb"
	"github.com/entireio/cli/cmd/entire/cli/skillimprove"
)

const (
	screenPicker    = 0
	screenDashboard = 1

	tabStats    = 0
	tabFriction = 1
	tabImprove  = 2

	maxWidth = 120
)

//nolint:recvcheck // bubbletea pattern: value receivers for interface, pointer for mutation
type rootModel struct {
	ctx       context.Context
	screen    int
	activeTab int

	// Current skill
	selectedSkill *skilldb.SkillRow

	// Data sources
	skillDB    *skilldb.SkillDB
	insightsDB *insightsdb.InsightsDB
	generator  *skillimprove.Generator
	repoRoot   string

	// Sub-models
	picker      pickerModel
	statsTab    statsModel
	frictionTab frictionModel
	improveTab  improveModel

	// UI state
	styles   tuiStyles
	width    int
	height   int
	showHelp bool
	errFlash string
	err      error
}

// Run launches the skill improvement TUI program.
func Run(ctx context.Context, skillDBPath, insightsDBPath, repoRoot string) error {
	sdb, err := skilldb.Open(skillDBPath)
	if err != nil {
		return fmt.Errorf("open skill database: %w", err)
	}
	defer sdb.Close()

	idb, err := insightsdb.Open(insightsDBPath)
	if err != nil {
		return fmt.Errorf("open insights database: %w", err)
	}
	defer idb.Close()

	styles := newStyles()
	m := rootModel{
		ctx:        ctx,
		styles:     styles,
		width:      maxWidth,
		skillDB:    sdb,
		insightsDB: idb,
		generator:  &skillimprove.Generator{},
		repoRoot:   repoRoot,
		picker:     newPickerModel(styles),
		improveTab: newImproveModel(styles),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, runErr := p.Run()
	if runErr != nil {
		return fmt.Errorf("run TUI: %w", runErr)
	}
	return nil
}

func (m rootModel) Init() tea.Cmd {
	return m.loadData()
}

func (m rootModel) loadData() tea.Cmd {
	return func() tea.Msg {
		// Discover skills on disk.
		discovered, err := skilldb.DiscoverSkills(m.repoRoot)
		if err != nil {
			return dataLoadedMsg{err: fmt.Errorf("discover skills: %w", err)}
		}

		// Convert discovered skills to SkillRow for refresh.
		rows := make([]skilldb.SkillRow, len(discovered))
		now := time.Now().UTC()
		for i, d := range discovered {
			rows[i] = skilldb.SkillRow{
				Name:         d.Name,
				SourceAgent:  d.SourceAgent,
				Path:         d.Path,
				Kind:         d.Kind,
				Scope:        d.Scope,
				DiscoveredAt: now,
				LastSeenAt:   now,
			}
		}

		// Refresh cache from insightsdb.
		populateResult, _, err := m.skillDB.RefreshFromInsightsDB(m.ctx, m.insightsDB, rows, m.repoRoot)
		if err != nil {
			return dataLoadedMsg{err: fmt.Errorf("refresh skill cache: %w", err)}
		}

		// Load stats for each discovered skill.
		// Use rows directly (not ListSkills) to preserve Scope from discovery.
		stats := make(map[string]*skilldb.SkillStatsResult, len(rows))
		for _, skill := range rows {
			st, statsErr := m.skillDB.SkillStats(m.ctx, skill.Name, skill.SourceAgent)
			if statsErr != nil {
				return dataLoadedMsg{err: fmt.Errorf("skill stats for %q: %w", skill.Name, statsErr)}
			}
			stats[skill.Name+"|"+skill.SourceAgent] = st
		}

		return dataLoadedMsg{skills: rows, stats: stats, populateResult: populateResult}
	}
}

//nolint:ireturn // required by bubbletea Model interface
func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case spinner.TickMsg:
		return m.handleSpinnerTick(msg)
	default:
		return m.handleDataMsg(msg)
	}
}

//nolint:ireturn // returns tea.Model as required by bubbletea dispatch pattern
func (m rootModel) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = min(msg.Width, maxWidth)
	m.height = msg.Height
	m.picker.setSize(m.width, m.contentHeight())
	m.statsTab.setSize(m.width, m.contentHeight())
	m.frictionTab.setSize(m.width, m.contentHeight())
	m.improveTab.setSize(m.width, m.contentHeight())
	return m, nil
}

//nolint:ireturn // returns tea.Model as required by bubbletea dispatch pattern
func (m rootModel) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Always allow ctrl+c to quit.
	if key.Matches(msg, globalKeyMap.Quit) && msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	if m.screen == screenPicker {
		return m.handlePickerKeys(msg)
	}
	return m.handleDashboardKeys(msg)
}

//nolint:ireturn // returns tea.Model as required by bubbletea dispatch pattern
func (m rootModel) handlePickerKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, globalKeyMap.Quit) {
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.picker, cmd = m.picker.update(msg)
	return m, cmd
}

//nolint:ireturn // returns tea.Model as required by bubbletea dispatch pattern
func (m rootModel) handleDashboardKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, globalKeyMap.Quit):
		return m, tea.Quit
	case key.Matches(msg, globalKeyMap.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, globalKeyMap.TabNext):
		m.activeTab = (m.activeTab + 1) % 3
		return m, nil
	case key.Matches(msg, globalKeyMap.TabPrev):
		m.activeTab = (m.activeTab + 2) % 3
		return m, nil
	case key.Matches(msg, globalKeyMap.Tab1):
		m.activeTab = tabStats
		return m, nil
	case key.Matches(msg, globalKeyMap.Tab2):
		m.activeTab = tabFriction
		return m, nil
	case key.Matches(msg, globalKeyMap.Tab3):
		m.activeTab = tabImprove
		return m, nil
	case key.Matches(msg, dashboardKeyMap.Back):
		return m, func() tea.Msg { return backToPickerMsg{} }
	case key.Matches(msg, dashboardKeyMap.Refresh):
		return m, func() tea.Msg { return refreshMsg{} }
	default:
		return m.delegateToActiveTab(msg)
	}
}

//nolint:ireturn // returns tea.Model as required by bubbletea dispatch pattern
func (m rootModel) handleSpinnerTick(msg spinner.TickMsg) (tea.Model, tea.Cmd) {
	if m.improveTab.isGenerating {
		var cmd tea.Cmd
		m.improveTab.spinner, cmd = m.improveTab.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

//nolint:ireturn,cyclop // returns tea.Model; message dispatch requires many cases
func (m rootModel) handleDataMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dataLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.picker.setData(msg.skills, msg.stats, msg.populateResult)
		return m, nil

	case skillSelectedMsg:
		m.selectedSkill = &msg.skill
		m.screen = screenDashboard
		m.activeTab = tabStats
		return m, m.loadSkillDetail(msg.skill)

	case skillDetailLoadedMsg:
		if msg.err != nil {
			return m, func() tea.Msg { return errorFlashMsg{text: msg.err.Error()} }
		}
		m.statsTab.setData(msg.stats, msg.sessions, msg.agents)
		m.frictionTab.setData(msg.friction, msg.missing)
		m.improveTab.setData(msg.improvements)
		return m, nil

	case backToPickerMsg:
		m.screen = screenPicker
		m.selectedSkill = nil
		m.activeTab = tabStats
		m.showHelp = false
		return m, nil

	case generateStartedMsg:
		m.improveTab.isGenerating = true
		return m, tea.Batch(m.improveTab.spinner.Tick, m.generateSuggestions())

	case generateDoneMsg:
		m.improveTab.isGenerating = false
		if msg.err != nil {
			return m, func() tea.Msg { return errorFlashMsg{text: fmt.Sprintf("generate failed: %v", msg.err)} }
		}
		m.improveTab.suggestions = msg.suggestions
		m.improveTab.selected = 0
		return m, nil

	case applyDiffMsg:
		return m, m.applyDiff(msg.index)

	case applyDiffResultMsg:
		return m.handleApplyResult(msg)

	case dismissSuggestionMsg:
		m.removeSuggestion(msg.index)
		return m, nil

	case refreshMsg:
		return m, m.loadData()

	case errorFlashMsg:
		m.errFlash = msg.text
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearErrorMsg{} })

	case clearErrorMsg:
		m.errFlash = ""
		return m, nil

	default:
		return m.delegateToActiveTab(msg)
	}
}

//nolint:ireturn // returns tea.Model as required by bubbletea dispatch pattern
func (m rootModel) handleApplyResult(msg applyDiffResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg { return errorFlashMsg{text: fmt.Sprintf("apply failed: %v", msg.err)} }
	}
	m.removeSuggestion(msg.index)
	return m, func() tea.Msg { return errorFlashMsg{text: "Diff applied successfully"} }
}

func (m *rootModel) removeSuggestion(index int) {
	if index < len(m.improveTab.suggestions) {
		m.improveTab.suggestions = append(
			m.improveTab.suggestions[:index],
			m.improveTab.suggestions[index+1:]...,
		)
		if m.improveTab.selected >= len(m.improveTab.suggestions) && m.improveTab.selected > 0 {
			m.improveTab.selected--
		}
	}
}

//nolint:ireturn // returns tea.Model as required by bubbletea dispatch pattern
func (m rootModel) delegateToActiveTab(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.screen != screenDashboard {
		return m, nil
	}
	var cmd tea.Cmd
	switch m.activeTab {
	case tabStats:
		m.statsTab, cmd = m.statsTab.update(msg)
	case tabFriction:
		m.frictionTab, cmd = m.frictionTab.update(msg)
	case tabImprove:
		m.improveTab, cmd = m.improveTab.update(msg)
	}
	return m, cmd
}

func (m rootModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nCheck database paths and try again.", m.err)
	}

	if m.screen == screenPicker && len(m.picker.skills) == 0 && m.picker.stats == nil {
		return "Loading..."
	}

	var b strings.Builder

	if m.screen == screenPicker {
		b.WriteString(m.picker.view())
		b.WriteString("\n")
		hints := "j/k navigate \u00b7 f filter \u00b7 enter select \u00b7 q quit"
		info := fmt.Sprintf("%d skills", len(m.picker.skills))
		b.WriteString(renderStatusBar(m.styles, hints, info, m.width))
		return b.String()
	}

	// Dashboard screen
	skillName := ""
	if m.selectedSkill != nil {
		skillName = m.selectedSkill.Name
	}

	b.WriteString(renderTabBar(m.styles, m.activeTab, m.width, skillName))
	b.WriteString("\n")

	if m.showHelp {
		b.WriteString(m.renderHelp())
	} else {
		switch m.activeTab {
		case tabStats:
			b.WriteString(m.statsTab.view())
		case tabFriction:
			b.WriteString(m.frictionTab.view())
		case tabImprove:
			b.WriteString(m.improveTab.view())
		}
	}

	// Status bar
	b.WriteString("\n")
	if m.errFlash != "" {
		b.WriteString(m.styles.render(m.styles.errorFlash, m.errFlash))
	} else {
		hints := m.activeTabHints()
		info := m.activeTabInfo()
		b.WriteString(renderStatusBar(m.styles, hints, info, m.width))
	}

	return b.String()
}

func (m rootModel) contentHeight() int {
	// Total height minus tab bar (2) and status bar (1) and newlines (2).
	h := m.height - 5
	if h < 5 {
		h = 5
	}
	return h
}

func (m rootModel) loadSkillDetail(skill skilldb.SkillRow) tea.Cmd {
	return func() tea.Msg {
		stats, err := m.skillDB.SkillStats(m.ctx, skill.Name, skill.SourceAgent)
		if err != nil {
			return skillDetailLoadedMsg{err: fmt.Errorf("load skill stats: %w", err)}
		}

		sessions, err := m.skillDB.RecentSessions(m.ctx, skill.Name, skill.SourceAgent, 10)
		if err != nil {
			return skillDetailLoadedMsg{err: fmt.Errorf("load recent sessions: %w", err)}
		}

		friction, err := m.skillDB.SkillFrictionThemes(m.ctx, skill.Name, skill.SourceAgent)
		if err != nil {
			return skillDetailLoadedMsg{err: fmt.Errorf("load friction themes: %w", err)}
		}

		missing, err := m.skillDB.SkillMissingInstructions(m.ctx, skill.Name, skill.SourceAgent)
		if err != nil {
			return skillDetailLoadedMsg{err: fmt.Errorf("load missing instructions: %w", err)}
		}

		agents, err := m.skillDB.AgentBreakdown(m.ctx, skill.Name, skill.SourceAgent)
		if err != nil {
			return skillDetailLoadedMsg{err: fmt.Errorf("load agent breakdown: %w", err)}
		}

		improvements, err := m.skillDB.ListImprovements(m.ctx, skill.Name, skill.SourceAgent, "")
		if err != nil {
			return skillDetailLoadedMsg{err: fmt.Errorf("load improvements: %w", err)}
		}

		return skillDetailLoadedMsg{
			stats:        stats,
			sessions:     sessions,
			friction:     friction,
			missing:      missing,
			agents:       agents,
			improvements: improvements,
		}
	}
}

func (m rootModel) generateSuggestions() tea.Cmd {
	return func() tea.Msg {
		if m.selectedSkill == nil {
			return generateDoneMsg{err: errors.New("no skill selected")}
		}

		skill := *m.selectedSkill

		// Read skill file content.
		skillPath := filepath.Join(m.repoRoot, skill.Path)
		content, err := os.ReadFile(skillPath) //nolint:gosec // path is constructed from repo root + db path
		if err != nil {
			return generateDoneMsg{err: fmt.Errorf("read skill file: %w", err)}
		}

		// Load data needed for the request.
		stats, err := m.skillDB.SkillStats(m.ctx, skill.Name, skill.SourceAgent)
		if err != nil {
			return generateDoneMsg{err: fmt.Errorf("load stats: %w", err)}
		}

		friction, err := m.skillDB.SkillFrictionThemes(m.ctx, skill.Name, skill.SourceAgent)
		if err != nil {
			return generateDoneMsg{err: fmt.Errorf("load friction: %w", err)}
		}

		missing, err := m.skillDB.SkillMissingInstructions(m.ctx, skill.Name, skill.SourceAgent)
		if err != nil {
			return generateDoneMsg{err: fmt.Errorf("load missing: %w", err)}
		}

		agents, err := m.skillDB.AgentBreakdown(m.ctx, skill.Name, skill.SourceAgent)
		if err != nil {
			return generateDoneMsg{err: fmt.Errorf("load agents: %w", err)}
		}

		// Build the request.
		frictionRate := 0.0
		if stats.TotalSessions > 0 {
			frictionRate = float64(stats.TotalFriction) / float64(stats.TotalSessions) * 100
		}

		var frictionThemes []skillimprove.FrictionTheme
		for _, f := range friction {
			frictionThemes = append(frictionThemes, skillimprove.FrictionTheme{
				Text:     f.Text,
				Category: f.Category,
				Count:    f.Count,
			})
		}

		var missingInstructions []skillimprove.MissingInstruction
		for _, mi := range missing {
			missingInstructions = append(missingInstructions, skillimprove.MissingInstruction{
				Instruction: mi.Instruction,
				Count:       mi.Count,
				Evidence:    mi.Evidence,
			})
		}

		var agentBreakdown []skillimprove.AgentStats
		for _, a := range agents {
			agentBreakdown = append(agentBreakdown, skillimprove.AgentStats{
				Agent:        a.Agent,
				SessionCount: a.SessionCount,
				AvgScore:     a.AvgScore,
			})
		}

		req := skillimprove.SkillImprovementRequest{
			SkillName:           skill.Name,
			SkillPath:           skill.Path,
			SkillContent:        string(content),
			FrictionThemes:      frictionThemes,
			MissingInstructions: missingInstructions,
			TotalSessions:       stats.TotalSessions,
			FrictionRate:        frictionRate,
			AgentBreakdown:      agentBreakdown,
		}

		result, err := m.generator.Generate(m.ctx, req)
		if err != nil {
			return generateDoneMsg{err: err}
		}

		return generateDoneMsg{suggestions: result.Suggestions}
	}
}

func (m rootModel) applyDiff(index int) tea.Cmd {
	return func() tea.Msg {
		if index >= len(m.improveTab.suggestions) || m.selectedSkill == nil {
			return applyDiffResultMsg{index: index, err: errors.New("invalid suggestion index")}
		}

		sug := m.improveTab.suggestions[index]
		skillPath := filepath.Join(m.repoRoot, m.selectedSkill.Path)

		if err := skillimprove.ApplyDiff(skillPath, sug.Diff); err != nil {
			return applyDiffResultMsg{index: index, err: fmt.Errorf("apply diff: %w", err)}
		}

		// Record the applied improvement in the database.
		imp := skilldb.SkillImprovement{
			ID:          fmt.Sprintf("%d-%s", time.Now().UnixNano(), m.selectedSkill.Name),
			SkillName:   m.selectedSkill.Name,
			SourceAgent: m.selectedSkill.SourceAgent,
			Title:       sug.Title,
			Description: sug.Description,
			Diff:        sug.Diff,
			Priority:    sug.Priority,
			Status:      "applied",
			CreatedAt:   time.Now().UTC(),
		}
		appliedAt := time.Now().UTC()
		imp.AppliedAt = &appliedAt

		if err := m.skillDB.InsertImprovement(m.ctx, imp); err != nil {
			// Non-fatal: diff was applied but recording failed.
			return applyDiffResultMsg{index: index, err: nil}
		}

		return applyDiffResultMsg{index: index, err: nil}
	}
}

func (m rootModel) activeTabHints() string {
	switch m.activeTab {
	case tabStats:
		return "esc back \u00b7 r refresh \u00b7 q quit"
	case tabFriction:
		return "j/k scroll \u00b7 esc back \u00b7 q quit"
	case tabImprove:
		return "g generate \u00b7 a apply \u00b7 d dismiss \u00b7 j/k navigate \u00b7 esc back"
	default:
		return "q quit"
	}
}

func (m rootModel) activeTabInfo() string {
	switch m.activeTab {
	case tabStats:
		if m.statsTab.stats != nil {
			return fmt.Sprintf("%d sessions", m.statsTab.stats.TotalSessions)
		}
	case tabFriction:
		total := len(m.frictionTab.friction) + len(m.frictionTab.missing)
		return fmt.Sprintf("%d items", total)
	case tabImprove:
		return fmt.Sprintf("%d suggestions", len(m.improveTab.suggestions))
	}
	return ""
}

func (m rootModel) renderHelp() string {
	var b strings.Builder
	b.WriteString(m.styles.render(m.styles.bold, "Keyboard Shortcuts"))
	b.WriteString("\n\n")

	b.WriteString(m.styles.render(m.styles.title, "Global"))
	b.WriteString("\n")
	b.WriteString("  Tab/Shift+Tab  cycle tabs    1-3  jump to tab\n")
	b.WriteString("  esc            back          q    quit          ?  toggle help\n")
	b.WriteString("\n")

	b.WriteString(m.styles.render(m.styles.title, "Stats"))
	b.WriteString("\n")
	b.WriteString("  r  refresh data\n")
	b.WriteString("\n")

	b.WriteString(m.styles.render(m.styles.title, "Friction & Gaps"))
	b.WriteString("\n")
	b.WriteString("  j/k  scroll items\n")
	b.WriteString("\n")

	b.WriteString(m.styles.render(m.styles.title, "Improve"))
	b.WriteString("\n")
	b.WriteString("  g  generate suggestions    a  apply diff    d  dismiss\n")
	b.WriteString("  j/k  navigate suggestions\n")

	return b.String()
}
