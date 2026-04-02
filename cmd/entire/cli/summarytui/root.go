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

type branchFilter int

const (
	filterCurrentBranch branchFilter = iota
	filterMainBranch
	filterAllBranches
)

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
}

type openDetailMsg struct {
	row insightsdb.SessionRow
}

type closeDetailMsg struct{}

func Run(ctx context.Context, rows []insightsdb.SessionRow) error {
	p := tea.NewProgram(newRootModel(rows, ""), tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("run summary TUI: %w", err)
	}
	return nil
}

func newRootModel(rows []insightsdb.SessionRow, currentBranch string) rootModel {
	columns := []table.Column{
		{Title: "TIME", Width: 16},
		{Title: "AGENT", Width: 14},
		{Title: "BRANCH", Width: 24},
		{Title: "CHECKPOINT", Width: 12},
		{Title: "SUMMARY", Width: 8},
		{Title: "FACETS", Width: 7},
		{Title: "TOKENS", Width: 8},
		{Title: "TURNS", Width: 5},
	}
	t := table.New(table.WithColumns(columns), table.WithFocused(true))
	p := paginator.New()
	p.PerPage = defaultPageSize
	m := rootModel{
		ctx:           context.Background(),
		rows:          append([]insightsdb.SessionRow(nil), rows...),
		currentBranch: currentBranch,
		filter:        filterCurrentBranch,
		table:         t,
		paginator:     p,
		pageSize:      defaultPageSize,
		styles:        newStyles(),
		width:         100,
		height:        30,
	}
	m.rebuildFilteredRows()
	return m
}

func (m rootModel) Init() tea.Cmd {
	return nil
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		if m.detailPage != nil {
			switch msg.Type {
			case tea.KeyEsc:
				return m, func() tea.Msg { return closeDetailMsg{} }
			case tea.KeyCtrlC:
				return m, tea.Quit
			}
			return m, nil
		}

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
		}

		switch msg.Type {
		case tea.KeyEnter:
			if row := m.selectedRow(); row != nil {
				selected := *row
				return m, func() tea.Msg { return openDetailMsg{row: selected} }
			}
		case tea.KeyCtrlC:
			return m, tea.Quit
		}

	case openDetailMsg:
		m.detailPage = newDetailModel(m.styles, msg.row)
		return m, nil

	case closeDetailMsg:
		m.detailPage = nil
		return m, nil
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
	b.WriteString("SESSION SUMMARY BROWSER\n\n")
	b.WriteString(filterLabel(m.filter))
	b.WriteString("  ")
	b.WriteString(pageLabel(m.paginator))
	b.WriteString("\n\n")
	b.WriteString(m.table.View())
	b.WriteString("\n\nj/k navigate  f filter  ←/→ page  enter open detail  q quit")
	return b.String()
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
			statusFlag(row.HasSummary),
			statusFlag(row.HasFacets),
			strconv.Itoa(row.TotalTokens),
			strconv.Itoa(row.TurnCount),
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
		if m.filter == filterMainBranch && row.Branch != "main" {
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

func filterLabel(filter branchFilter) string {
	switch filter {
	case filterMainBranch:
		return "Main"
	case filterAllBranches:
		return "All"
	default:
		return "Current Branch"
	}
}

func pageLabel(p paginator.Model) string {
	return fmt.Sprintf("Page %d/%d", p.Page+1, max(1, p.TotalPages))
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
