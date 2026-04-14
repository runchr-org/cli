package summarytui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
)

// SessionData is the TUI's internal data model, populated from checkpoint store data.
type SessionData struct {
	CheckpointID string
	SessionID    string
	SessionIndex int
	Agent        string
	Model        string
	Branch       string
	CreatedAt    time.Time
	FilesTouched []string
	// Token usage (from CommittedMetadata.TokenUsage)
	InputTokens  int
	CacheTokens  int
	OutputTokens int
	TotalTokens  int
	// Session metrics (when available from agent hooks)
	DurationMs int64
	TurnCount  int
	// Summary (nil when not yet generated)
	Summary *checkpoint.Summary
}

const defaultPageSize = 10

// Chrome: filter bar (1) + gap (1) + separator (1) + list header (1) + separator (1) + status bar (1) + padding (1) = 7
const verticalChrome = 7

// GenerateFunc generates summary and facets for a single session on demand.
type GenerateFunc func(ctx context.Context, session SessionData) (SessionData, error)

type branchFilter int

const (
	filterCurrentBranch branchFilter = iota // current working branch
	filterRepo                              // all branches in this repo
)

type timeFilter int

const (
	timeFilter24h timeFilter = iota
	timeFilter7d
	timeFilter30d
	timeFilterAll
)

//nolint:recvcheck // bubbletea pattern: value receiver for interface, pointer receivers for mutation helpers
type rootModel struct {
	ctx           context.Context
	branchRows    []SessionData // rows for "current branch" view
	repoRows      []SessionData // rows for "repo" view
	filteredRows  []SessionData
	currentBranch string
	branchFilter  branchFilter
	timeFilter    timeFilter
	cursor        int
	paginator     paginator.Model
	pageSize      int
	detailVP      viewport.Model
	width         int
	height        int
	styles        styles
	generateFn    GenerateFunc
	generating    bool
	genStatus     string // status message for generate operation
	accessible    bool   // accessible mode fallback
}

type generateDoneMsg struct {
	row SessionData
}

type generateErrMsg struct {
	err error
}

func Run(ctx context.Context, rows []SessionData) error {
	return RunWithCurrentBranch(ctx, rows, "", nil, nil)
}

func RunWithCurrentBranch(_ context.Context, rows []SessionData, currentBranch string, repoRows []SessionData, generateFn GenerateFunc) error {
	p := tea.NewProgram(newRootModel(rows, currentBranch, repoRows, generateFn), tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("run summary TUI: %w", err)
	}
	return nil
}

func newRootModel(rows []SessionData, currentBranch string, repoRows []SessionData, generateFn GenerateFunc) rootModel {
	s := newStyles()
	p := paginator.New()
	p.PerPage = defaultPageSize

	accessible := os.Getenv("ACCESSIBLE") != ""

	vp := viewport.New(60, 20)
	vp.MouseWheelEnabled = false

	m := rootModel{
		ctx:           context.Background(),
		branchRows:    append([]SessionData(nil), rows...),
		repoRows:      append([]SessionData(nil), repoRows...),
		currentBranch: currentBranch,
		branchFilter:  filterCurrentBranch,
		timeFilter:    timeFilterAll,
		paginator:     p,
		pageSize:      defaultPageSize,
		styles:        s,
		width:         100,
		height:        30,
		detailVP:      vp,
		generateFn:    generateFn,
		accessible:    accessible,
	}
	m.rebuildFilteredRows()
	m.updateDetailViewport()
	return m
}

func (m rootModel) Init() tea.Cmd {
	return nil
}

//nolint:cyclop,ireturn // required by bubbletea Model interface; state machine has inherent branching
func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		return m.updateKeys(msg)

	case generateDoneMsg:
		m.generating = false
		m.genStatus = "Generated"
		m.updateRowData(msg.row)
		m.updateDetailViewport()
		return m, nil

	case generateErrMsg:
		m.generating = false
		m.genStatus = "Error: " + msg.err.Error()
		return m, nil
	}

	return m, nil
}

//nolint:cyclop,ireturn // required by bubbletea pattern
func (m rootModel) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.quit):
		return m, tea.Quit
	case key.Matches(msg, keys.cycleTimeFilter):
		m.cycleTime()
		return m, nil
	case key.Matches(msg, keys.cycleBranchFilter):
		m.cycleBranch()
		return m, nil
	case key.Matches(msg, keys.cursorDown):
		m.moveCursor(1)
		return m, nil
	case key.Matches(msg, keys.cursorUp):
		m.moveCursor(-1)
		return m, nil
	case key.Matches(msg, keys.nextPage):
		m.nextPage()
		return m, nil
	case key.Matches(msg, keys.prevPage):
		m.prevPage()
		return m, nil
	case key.Matches(msg, keys.generate):
		return m.handleGenerate()
	}

	//nolint:exhaustive // only Ctrl+C needs special handling here
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	default:
	}

	// Scroll detail viewport
	var cmd tea.Cmd
	m.detailVP, cmd = m.detailVP.Update(msg)
	return m, cmd
}

//nolint:ireturn // required by bubbletea pattern
func (m rootModel) handleGenerate() (tea.Model, tea.Cmd) {
	if m.generateFn == nil || m.generating {
		return m, nil
	}
	row := m.selectedRow()
	if row == nil {
		return m, nil
	}
	m.generating = true
	m.genStatus = "Generating..."
	selected := *row
	ctx := m.ctx
	fn := m.generateFn
	return m, func() tea.Msg {
		updated, err := fn(ctx, selected)
		if err != nil {
			return generateErrMsg{err: err}
		}
		return generateDoneMsg{row: updated}
	}
}

func (m rootModel) View() string {
	var b strings.Builder

	// Filter bar
	b.WriteString(m.renderFilterBar())
	b.WriteString("\n\n")

	listWidth := m.listWidth()
	detailWidth := m.detailWidth()
	contentHeight := m.contentHeight()

	// List pane
	listPane := m.renderListPane(listWidth, contentHeight)

	// Detail pane
	detailPane := m.renderDetailPane(detailWidth, contentHeight)

	// Join horizontally
	split := lipgloss.JoinHorizontal(lipgloss.Top, listPane, detailPane)
	b.WriteString(split)
	b.WriteString("\n")

	// Status bar
	b.WriteString(m.renderStatusBar())

	return b.String()
}

// --- Filter bar ---

func (m rootModel) renderFilterBar() string {
	sep := m.styles.render(m.styles.filterSeparator, " │ ")

	// TIME filter
	timeLabels := []struct {
		filter timeFilter
		label  string
	}{
		{timeFilter24h, "24h"},
		{timeFilter7d, "7d"},
		{timeFilter30d, "30d"},
		{timeFilterAll, "all"},
	}
	var timeParts []string
	for _, item := range timeLabels {
		if item.filter == m.timeFilter {
			timeParts = append(timeParts, m.styles.render(m.styles.filterActive, item.label))
		} else {
			timeParts = append(timeParts, m.styles.render(m.styles.filterInactive, item.label))
		}
	}
	timeStr := m.styles.render(m.styles.filterLabel, "TIME: ") + strings.Join(timeParts, sep)

	// BRANCH filter
	branchLabels := []struct {
		filter branchFilter
		label  string
	}{
		{filterCurrentBranch, "current"},
		{filterRepo, "repo"},
	}
	var branchParts []string
	for _, item := range branchLabels {
		if item.filter == m.branchFilter {
			branchParts = append(branchParts, m.styles.render(m.styles.filterActive, item.label))
		} else {
			branchParts = append(branchParts, m.styles.render(m.styles.filterInactive, item.label))
		}
	}
	branchStr := m.styles.render(m.styles.filterLabel, "BRANCH: ") + strings.Join(branchParts, sep)

	count := m.styles.render(m.styles.sessionCount, fmt.Sprintf("%d sessions", len(m.filteredRows)))

	return timeStr + "    " + branchStr + "    " + count
}

// --- List pane ---

func (m rootModel) renderListPane(width, height int) string {
	var b strings.Builder

	// Column header
	header := m.formatListRow("TIME", "CKPT", "AGENT", width)
	b.WriteString(m.styles.render(m.styles.listHeader, header))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", width))
	b.WriteString("\n")

	pageRows := m.currentPageRows()
	listHeight := height - 2 // header + separator

	for i, row := range pageRows {
		if i >= listHeight {
			break
		}
		timeStr := row.CreatedAt.Format("01-02 15:04")
		checkpoint := row.CheckpointID
		if len(checkpoint) > 6 {
			checkpoint = checkpoint[:6]
		}
		agent := truncate(row.Agent, 14)
		line := m.formatListRow(timeStr, checkpoint, agent, width)

		if i == m.cursor {
			// Selected: amber text + dark background + left accent
			accent := m.styles.render(m.styles.listAccent, "▸ ")
			styled := m.styles.render(m.styles.listSelected, line)
			if m.styles.colorEnabled {
				styled = m.styles.listSelectedBg.Render(styled)
			}
			b.WriteString(accent + styled)
		} else {
			b.WriteString("  " + m.styles.render(m.styles.listNormal, line))
		}
		b.WriteString("\n")
	}

	// Fill remaining lines
	for i := len(pageRows); i < listHeight; i++ {
		b.WriteString("\n")
	}

	// Page indicator
	pageInfo := m.styles.render(m.styles.dim, fmt.Sprintf("%d/%d", m.paginator.Page+1, max(1, m.paginator.TotalPages)))
	b.WriteString(m.styles.render(m.styles.dim, "←→ ") + pageInfo)

	return b.String()
}

func (m rootModel) formatListRow(time, ckpt, agent string, _ int) string {
	return fmt.Sprintf("%-11s %-6s %-14s", time, ckpt, agent)
}

// --- Detail pane ---

func (m rootModel) renderDetailPane(width, _ int) string {
	var b strings.Builder

	// Column header aligned with list pane header
	b.WriteString(m.styles.render(m.styles.listHeader, "Details"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", width))
	b.WriteString("\n")

	row := m.selectedRow()
	if row == nil {
		b.WriteString(m.styles.render(m.styles.emptyState, "  No sessions to display"))
		return b.String()
	}

	// Fixed metadata header
	header := renderMetadataHeader(m.styles, *row, width)
	b.WriteString(header)
	b.WriteString("\n\n")

	// Scrollable viewport (sized in resize())
	b.WriteString(m.detailVP.View())

	return b.String()
}

func (m rootModel) renderStatusBar() string {
	var parts []string
	parts = append(parts, "j/k navigate")
	parts = append(parts, "pgup/dn scroll")
	parts = append(parts, "1 time")
	parts = append(parts, "2 branch")
	parts = append(parts, "←→ page")
	if m.generateFn != nil {
		parts = append(parts, "g generate")
	}
	parts = append(parts, "q quit")
	status := strings.Join(parts, "  ")

	if m.genStatus != "" {
		style := m.styles.filterActive
		if strings.HasPrefix(m.genStatus, "Error:") {
			style = m.styles.errorText
		}
		status = m.styles.render(style, m.genStatus) + "  " + status
	}

	return m.styles.render(m.styles.statusBar, status)
}

// --- Layout helpers ---

func (m rootModel) listWidth() int {
	return max(30, m.width*30/100)
}

func (m rootModel) detailWidth() int {
	return max(30, m.width-m.listWidth()-1)
}

func (m rootModel) contentHeight() int {
	return max(5, m.height-verticalChrome)
}

// --- Data management ---

func (m *rootModel) updateRowData(updated SessionData) {
	for i, row := range m.branchRows {
		if row.SessionID == updated.SessionID {
			m.branchRows[i] = updated
			break
		}
	}
	for i, row := range m.repoRows {
		if row.SessionID == updated.SessionID {
			m.repoRows[i] = updated
			break
		}
	}
	for i, row := range m.filteredRows {
		if row.SessionID == updated.SessionID {
			m.filteredRows[i] = updated
			break
		}
	}
}

func (m *rootModel) rebuildFilteredRows() {
	m.filteredRows = m.applyFilter()
	m.paginator.PerPage = m.pageSize
	totalPages := m.paginator.SetTotalPages(len(m.filteredRows))
	if totalPages == 0 {
		m.paginator.TotalPages = 1
	}
	if m.paginator.Page >= m.paginator.TotalPages {
		m.paginator.Page = max(0, m.paginator.TotalPages-1)
	}
	if m.paginator.Page < 0 {
		m.paginator.Page = 0
	}
	if m.cursor >= len(m.currentPageRows()) {
		m.cursor = max(0, len(m.currentPageRows())-1)
	}
}

func (m rootModel) applyFilter() []SessionData {
	// Select source rows based on branch filter scope.
	var source []SessionData
	switch m.branchFilter {
	case filterCurrentBranch:
		source = m.branchRows
	case filterRepo:
		source = m.repoRows
	}

	filtered := make([]SessionData, 0, len(source))
	now := time.Now()
	for _, row := range source {
		// Time filter
		switch m.timeFilter {
		case timeFilter24h:
			if now.Sub(row.CreatedAt) > 24*time.Hour {
				continue
			}
		case timeFilter7d:
			if now.Sub(row.CreatedAt) > 7*24*time.Hour {
				continue
			}
		case timeFilter30d:
			if now.Sub(row.CreatedAt) > 30*24*time.Hour {
				continue
			}
		case timeFilterAll:
			// no time filtering
		}

		// Current-branch view still filters by branch name.
		if m.branchFilter == filterCurrentBranch && m.currentBranch != "" && row.Branch != m.currentBranch {
			continue
		}

		filtered = append(filtered, row)
	}
	return filtered
}

func (m rootModel) currentPageRows() []SessionData {
	if len(m.filteredRows) == 0 {
		return nil
	}
	start, end := m.paginator.GetSliceBounds(len(m.filteredRows))
	if start >= len(m.filteredRows) {
		return nil
	}
	if end > len(m.filteredRows) {
		end = len(m.filteredRows)
	}
	return m.filteredRows[start:end]
}

func (m rootModel) selectedRow() *SessionData {
	rows := m.currentPageRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return nil
	}
	return &rows[m.cursor]
}

// --- Navigation ---

func (m *rootModel) moveCursor(delta int) {
	pageRows := m.currentPageRows()
	if len(pageRows) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(pageRows) {
		m.cursor = len(pageRows) - 1
	}
	m.genStatus = "" // clear status on navigation
	m.updateDetailViewport()
}

func (m *rootModel) cycleTime() {
	m.timeFilter = (m.timeFilter + 1) % 4
	m.paginator.Page = 0
	m.cursor = 0
	m.genStatus = ""
	m.rebuildFilteredRows()
	m.updateDetailViewport()
}

func (m *rootModel) cycleBranch() {
	m.branchFilter = (m.branchFilter + 1) % 2
	m.paginator.Page = 0
	m.cursor = 0
	m.genStatus = ""
	m.rebuildFilteredRows()
	m.updateDetailViewport()
}

func (m *rootModel) nextPage() {
	if m.paginator.OnLastPage() {
		return
	}
	m.paginator.NextPage()
	m.cursor = 0
	m.genStatus = ""
	m.updateDetailViewport()
}

func (m *rootModel) prevPage() {
	if m.paginator.OnFirstPage() {
		return
	}
	m.paginator.PrevPage()
	m.cursor = 0
	m.genStatus = ""
	m.updateDetailViewport()
}

func (m *rootModel) updateDetailViewport() {
	row := m.selectedRow()
	if row == nil {
		m.detailVP.SetContent("")
		return
	}
	content := renderDetailContent(m.styles, *row, m.detailWidth())
	m.detailVP.SetContent(content)
}

func (m *rootModel) resize(width, height int) {
	selectedSessionID := ""
	if selected := m.selectedRow(); selected != nil {
		selectedSessionID = selected.SessionID
	}

	m.width = width
	m.height = height

	contentHeight := m.contentHeight()
	listHeight := contentHeight - 2 // header + separator
	m.pageSize = max(1, listHeight)

	// Detail viewport: subtract detail header (1) + separator (1) + metadata header (~3) + blank line (1) = 6
	vp := viewport.New(m.detailWidth(), max(3, contentHeight-6))
	vp.MouseWheelEnabled = false
	m.detailVP = vp

	m.rebuildFilteredRows()
	m.restoreSelection(selectedSessionID)
	m.updateDetailViewport()
}

func (m *rootModel) restoreSelection(sessionID string) {
	if sessionID == "" || len(m.filteredRows) == 0 {
		return
	}

	for idx, row := range m.filteredRows {
		if row.SessionID != sessionID {
			continue
		}

		m.paginator.Page = idx / m.pageSize
		cursor := idx % m.pageSize
		pageRows := m.currentPageRows()
		if len(pageRows) > 0 && cursor >= len(pageRows) {
			cursor = len(pageRows) - 1
		}
		m.cursor = cursor
		return
	}
}
