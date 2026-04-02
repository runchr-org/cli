//nolint:ireturn // bubbletea model methods in this file must return framework interfaces
package summarytui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
)

const defaultPageSize = 10
const rootVerticalChrome = 6

// GenerateFunc generates summary and facets for a single session on demand.
// Returns the updated session row with populated summary/facet data.
type GenerateFunc func(ctx context.Context, row insightsdb.SessionRow) (insightsdb.SessionRow, error)

type branchFilter int

const (
	filterRepo          branchFilter = iota // default branch (main/master)
	filterCurrentBranch                     // current working branch
	filterAllBranches                       // all branches
)

//nolint:recvcheck // bubbletea pattern: value receiver for interface, pointer receivers for mutation helpers
type rootModel struct {
	ctx           context.Context
	rows          []insightsdb.SessionRow
	filteredRows  []insightsdb.SessionRow
	currentBranch string
	filter        branchFilter
	table         table.Model
	paginator     paginator.Model
	pageSize      int
	width         int
	height        int
	styles        styles
	detailPage    *detailModel
	generateFn    GenerateFunc
	generating    bool
}

type openDetailMsg struct {
	row insightsdb.SessionRow
}

type closeDetailMsg struct{}

type generateDoneMsg struct {
	row insightsdb.SessionRow
}

type generateErrMsg struct {
	err error
}

func Run(ctx context.Context, rows []insightsdb.SessionRow) error {
	return RunWithCurrentBranch(ctx, rows, "", nil)
}

func RunWithCurrentBranch(_ context.Context, rows []insightsdb.SessionRow, currentBranch string, generateFn GenerateFunc) error {
	p := tea.NewProgram(newRootModel(rows, currentBranch, generateFn), tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("run summary TUI: %w", err)
	}
	return nil
}

func newRootModel(rows []insightsdb.SessionRow, currentBranch string, generateFn GenerateFunc) rootModel {
	columns := []table.Column{
		{Title: "TIME", Width: 19},
		{Title: "AGENT", Width: 16},
		{Title: "BRANCH", Width: 26},
		{Title: "CHECKPOINT", Width: 15},
		{Title: "TOKENS", Width: 10},
	}
	styles := newStyles()
	t := table.New(table.WithColumns(columns), table.WithFocused(true))
	t.SetStyles(styles.tableStyles())
	p := paginator.New()
	p.PerPage = defaultPageSize
	m := rootModel{
		ctx:           context.Background(),
		rows:          append([]insightsdb.SessionRow(nil), rows...),
		currentBranch: currentBranch,
		filter:        filterRepo,
		table:         t,
		paginator:     p,
		pageSize:      defaultPageSize,
		styles:        styles,
		width:         100,
		height:        30,
		generateFn:    generateFn,
	}
	m.rebuildFilteredRows()
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
		if m.detailPage != nil {
			m.detailPage.setSize(m.width, m.height)
		}
		return m, nil

	case tea.KeyMsg:
		if m.detailPage != nil {
			return m.updateDetailKeys(msg)
		}
		return m.updateTableKeys(msg)

	case openDetailMsg:
		m.detailPage = newDetailModel(m.styles, msg.row, m.generateFn != nil)
		m.detailPage.setSize(m.width, m.height)
		return m, nil

	case closeDetailMsg:
		m.detailPage = nil
		m.generating = false
		return m, nil

	case generateDoneMsg:
		m.generating = false
		m.updateRowData(msg.row)
		if m.detailPage != nil {
			m.detailPage.row = msg.row
			m.detailPage.status = "Generated"
			m.detailPage.setSize(m.width, m.height) // re-render content
		}
		return m, nil

	case generateErrMsg:
		m.generating = false
		if m.detailPage != nil {
			m.detailPage.status = "Error: " + msg.err.Error()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

//nolint:ireturn // required by bubbletea pattern
func (m rootModel) updateDetailKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	//nolint:exhaustive // only quit/back/generate keys handled; rest delegates to viewport
	switch msg.Type {
	case tea.KeyEsc:
		return m, func() tea.Msg { return closeDetailMsg{} }
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "q":
			return m, tea.Quit
		case "g":
			if m.generateFn != nil && !m.generating && m.detailPage != nil {
				m.generating = true
				m.detailPage.status = "Generating summary and facets..."
				row := m.detailPage.row
				ctx := m.ctx
				fn := m.generateFn
				return m, func() tea.Msg {
					updated, err := fn(ctx, row)
					if err != nil {
						return generateErrMsg{err: err}
					}
					return generateDoneMsg{row: updated}
				}
			}
		}
	default:
	}
	var cmd tea.Cmd
	*m.detailPage, cmd = m.detailPage.update(msg)
	return m, cmd
}

//nolint:ireturn // required by bubbletea pattern
func (m rootModel) updateTableKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.cycleFilter):
		m.cycleFilter()
		return m, nil
	case key.Matches(msg, keys.nextPage):
		m.nextPage()
		return m, nil
	case key.Matches(msg, keys.prevPage):
		m.prevPage()
		return m, nil
	case key.Matches(msg, keys.quit):
		return m, tea.Quit
	}

	//nolint:exhaustive // only command-specific keys are handled here; all others fall through to the table widget
	switch msg.Type {
	case tea.KeyEnter:
		if row := m.selectedRow(); row != nil {
			selected := *row
			return m, func() tea.Msg { return openDetailMsg{row: selected} }
		}
	case tea.KeyCtrlC:
		return m, tea.Quit
	default:
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m rootModel) View() string {
	if m.detailPage != nil {
		return m.detailPage.view()
	}

	var b strings.Builder
	b.WriteString(m.styles.render(m.styles.appTitle, "SESSION SUMMARY"))
	b.WriteString("\n\n")
	b.WriteString(renderFilterChips(m.styles, m.filter))
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, pageLabel(m.paginator)))
	b.WriteString("\n\n")
	b.WriteString(m.table.View())
	b.WriteString("\n\n")
	b.WriteString(m.styles.render(m.styles.statusBar, "j/k navigate  f filter  ←/→ page  enter detail  q quit"))
	return b.String()
}

func (m *rootModel) updateRowData(updated insightsdb.SessionRow) {
	for i, row := range m.rows {
		if row.SessionID == updated.SessionID {
			m.rows[i] = updated
			break
		}
	}
	for i, row := range m.filteredRows {
		if row.SessionID == updated.SessionID {
			m.filteredRows[i] = updated
			break
		}
	}
	m.rebuildTable()
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
	m.rebuildTable()
}

func (m *rootModel) rebuildTable() {
	pageRows := m.currentPageRows()
	rows := make([]table.Row, 0, len(pageRows))
	for _, row := range pageRows {
		checkpoint := row.CheckpointID
		if len(checkpoint) > 12 {
			checkpoint = checkpoint[:12]
		}
		rows = append(rows, table.Row{
			row.CreatedAt.Format("2006-01-02 15:04"),
			row.Agent,
			truncate(row.Branch, 24),
			checkpoint,
			strconv.Itoa(row.TotalTokens),
		})
	}
	m.table.SetRows(rows)
	if len(rows) == 0 {
		m.table.SetCursor(0)
		return
	}
	if m.table.Cursor() >= len(rows) {
		m.table.SetCursor(len(rows) - 1)
	}
}

func (m rootModel) selectedRow() *insightsdb.SessionRow {
	rows := m.currentPageRows()
	idx := m.table.Cursor()
	if idx < 0 || idx >= len(rows) {
		return nil
	}
	return &rows[idx]
}

func (m rootModel) applyFilter() []insightsdb.SessionRow {
	filtered := make([]insightsdb.SessionRow, 0, len(m.rows))
	for _, row := range m.rows {
		if m.filter == filterRepo && row.Branch != "main" && row.Branch != "master" {
			continue
		}
		if m.filter == filterCurrentBranch && m.currentBranch != "" && row.Branch != m.currentBranch {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func (m rootModel) currentPageRows() []insightsdb.SessionRow {
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

func (m *rootModel) cycleFilter() {
	m.filter = (m.filter + 1) % 3
	m.paginator.Page = 0
	m.table.SetCursor(0)
	m.rebuildFilteredRows()
}

func (m *rootModel) nextPage() {
	if m.paginator.OnLastPage() {
		return
	}
	m.paginator.NextPage()
	m.table.SetCursor(0)
	m.rebuildTable()
}

func (m *rootModel) prevPage() {
	if m.paginator.OnFirstPage() {
		return
	}
	m.paginator.PrevPage()
	m.table.SetCursor(0)
	m.rebuildTable()
}

func pageLabel(p paginator.Model) string {
	return fmt.Sprintf("Page %d/%d", p.Page+1, max(1, p.TotalPages))
}

func renderFilterChips(styles styles, active branchFilter) string {
	labels := []struct {
		filter branchFilter
		label  string
	}{
		{filterRepo, "Repo"},
		{filterCurrentBranch, "Current Branch"},
		{filterAllBranches, "All"},
	}

	parts := make([]string, 0, len(labels))
	for _, item := range labels {
		text := item.label
		if item.filter == active {
			text = "[" + text + "]"
			parts = append(parts, styles.render(styles.chipActive, text))
			continue
		}
		parts = append(parts, styles.render(styles.chipInactive, text))
	}
	return strings.Join(parts, " ")
}

func (m *rootModel) resize(width, height int) {
	selectedSessionID := ""
	if selected := m.selectedRow(); selected != nil {
		selectedSessionID = selected.SessionID
	}

	m.width = width
	m.height = height
	m.table.SetWidth(width)

	tableHeight := max(3, height-rootVerticalChrome)
	m.table.SetHeight(tableHeight)
	m.pageSize = max(1, tableHeight-1)

	m.rebuildFilteredRows()
	m.restoreSelection(selectedSessionID)
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
		m.rebuildTable()

		cursor := idx % m.pageSize
		if pageRows := m.currentPageRows(); len(pageRows) > 0 && cursor >= len(pageRows) {
			cursor = len(pageRows) - 1
		}
		m.table.SetCursor(cursor)
		return
	}
}
