package memorylooptui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

const (
	tabMemories  = 0
	tabInjection = 1
	tabHistory   = 2
	tabSettings  = 3
	maxWidth     = 120
)

//nolint:recvcheck // bubbletea pattern: value receivers for interface, pointer for pushState mutation
type rootModel struct {
	ctx                 context.Context
	activeTab           int
	state               *memoryloop.State
	styles              tuiStyles
	width               int
	height              int
	showHelp            bool
	isRefreshing        bool
	spinner             spinner.Model
	err                 error
	flashText           string
	flashStyle          lipgloss.Style
	detailPage          *memoryDetailModel
	wizard              *wizardModel
	wizardActionHandler WizardActionHandler

	memoriesTab  memoriesModel
	injectionTab injectionModel
	historyTab   historyModel
	settingsTab  settingsModel
}

// Run launches the TUI program.
func Run(ctx context.Context, opts RunOptions) error {
	s := spinner.New()
	s.Spinner = spinner.Dot

	styles := newStyles()
	m := rootModel{
		ctx:                 ctx,
		styles:              styles,
		width:               maxWidth, // will be updated by tea.WindowSizeMsg
		spinner:             s,
		wizardActionHandler: opts.WizardActionHandler,
		memoriesTab:         newMemoriesModel(styles),
		injectionTab:        newInjectionModel(styles),
		historyTab:          newHistoryModel(styles),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

func (m rootModel) Init() tea.Cmd {
	return func() tea.Msg {
		state, err := memoryloop.LoadState(m.ctx)
		return stateLoadedMsg{state: state, err: err}
	}
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = min(msg.Width, maxWidth)
		m.height = msg.Height
		m.memoriesTab.setSize(m.width, m.contentHeight())
		m.injectionTab.setSize(m.width, m.contentHeight())
		m.historyTab.setSize(m.width, m.contentHeight())
		m.settingsTab.setSize(m.width, m.contentHeight())
		if m.detailPage != nil {
			m.detailPage.setSize(m.width, m.contentHeight())
		}
		if m.wizard != nil && m.detailPage == nil {
			m.wizard.setSize(m.width, m.contentHeight())
		}
		return m, nil

	case tea.KeyMsg:
		if key.Matches(msg, globalKeyMap.Quit) {
			return m, tea.Quit
		}

		if m.detailPage != nil {
			var cmd tea.Cmd
			*m.detailPage, cmd = m.detailPage.update(msg)
			m.wizard = &m.detailPage.wizard
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		if m.wizard != nil {
			var cmd tea.Cmd
			*m.wizard, cmd = m.wizard.update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		// When a sub-model is capturing input (add form, search, text input),
		// skip global key handling so Tab/Esc/number keys reach the sub-model.
		tabCapturesInput := (m.activeTab == tabMemories && m.memoriesTab.capturesInput()) ||
			(m.activeTab == tabInjection && m.injectionTab.inputFocus)

		if !tabCapturesInput {
			// Global keys -- check before delegating to tabs
			switch {
			case key.Matches(msg, globalKeyMap.Help):
				m.showHelp = !m.showHelp
				return m, nil
			case key.Matches(msg, globalKeyMap.TabNext):
				m.activeTab = (m.activeTab + 1) % 4
				return m, nil
			case key.Matches(msg, globalKeyMap.TabPrev):
				m.activeTab = (m.activeTab + 3) % 4
				return m, nil
			case key.Matches(msg, globalKeyMap.Tab1):
				m.activeTab = tabMemories
				return m, nil
			case key.Matches(msg, globalKeyMap.Tab2):
				m.activeTab = tabInjection
				return m, nil
			case key.Matches(msg, globalKeyMap.Tab3):
				m.activeTab = tabHistory
				return m, nil
			case key.Matches(msg, globalKeyMap.Tab4):
				m.activeTab = tabSettings
				return m, nil
			}
		}

	case stateLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.state = msg.state
		m.pushState()
		return m, nil

	case addMemoryMsg:
		return m.handleAddMemory(msg)

	case pruneMsg:
		return m.handlePrune()

	case settingsChangedMsg:
		return m.handleSettingsChanged(msg)

	case testPromptMsg:
		if m.state == nil || m.state.Store == nil {
			return m, nil
		}
		var selectOpts []memoryloop.SelectOption
		if len(m.state.Store.InjectionScopes) > 0 {
			selectOpts = append(selectOpts, memoryloop.WithInjectionScopes(m.state.Store.InjectionScopes))
		}
		matches := memoryloop.SelectRelevant(*m.state.Store, msg.prompt, time.Now(), selectOpts...)
		return m, func() tea.Msg { return testPromptResultMsg{matches: matches} }

	case refreshStartedMsg:
		if m.isRefreshing {
			return m, nil
		}
		m.isRefreshing = true
		// Full async refresh will be added in a later task.
		return m, func() tea.Msg {
			return flashMsg{text: "Refresh not yet implemented in TUI. Use: entire memory-loop refresh", success: false}
		}

	case wizardOpenMsg:
		m.showHelp = false
		m.detailPage = m.newDetailPage(msg.record)
		m.wizard = &m.detailPage.wizard
		return m, nil

	case wizardCloseMsg:
		m.detailPage = nil
		m.wizard = nil
		return m, nil

	case wizardResultMsg:
		return m.handleWizardResult(msg)

	case flashMsg:
		m.setFlash(msg.text, msg.success)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })

	case clearFlashMsg:
		m.flashText = ""
		m.flashStyle = lipgloss.Style{}
		return m, nil
	}

	// Delegate to active tab
	var cmd tea.Cmd
	switch m.activeTab {
	case tabMemories:
		m.memoriesTab, cmd = m.memoriesTab.update(msg)
	case tabInjection:
		m.injectionTab, cmd = m.injectionTab.update(msg)
	case tabHistory:
		m.historyTab, cmd = m.historyTab.update(msg)
	case tabSettings:
		m.settingsTab, cmd = m.settingsTab.update(msg)
	}
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m rootModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error loading memory store: %v\nCheck .entire/memory-loop.json", m.err)
	}
	if m.state == nil {
		return "Loading..."
	}

	var b strings.Builder

	// Tab bar
	mode := memoryloop.ModeOff
	policy := memoryloop.ActivationPolicyReview
	if m.state.Store != nil {
		mode = m.state.Store.Mode
		policy = m.state.Store.ActivationPolicy
	}
	b.WriteString(renderTabBar(m.styles, m.activeTab, m.width, mode, policy))
	b.WriteString("\n")

	// Content area
	switch {
	case m.showHelp:
		b.WriteString(m.renderHelp())
	case m.detailPage != nil:
		b.WriteString(m.detailPage.view())
	case m.wizard != nil:
		b.WriteString(m.wizard.view())
	default:
		switch m.activeTab {
		case tabMemories:
			b.WriteString(m.memoriesTab.view())
		case tabInjection:
			b.WriteString(m.injectionTab.view())
		case tabHistory:
			b.WriteString(m.historyTab.view())
		case tabSettings:
			b.WriteString(m.settingsTab.view())
		}
	}

	// Status bar
	b.WriteString("\n")
	if m.flashText != "" {
		b.WriteString(m.styles.render(m.flashStyle, m.flashText))
	} else {
		hints := m.activeTabHints()
		info := m.activeTabInfo()
		b.WriteString(renderStatusBar(m.styles, hints, info, m.width))
	}

	return b.String()
}

func (m rootModel) contentHeight() int {
	// Total height minus tab bar (2) and status bar (1) and newlines (2)
	h := m.height - 5
	if h < 5 {
		h = 5
	}
	return h
}

func (m *rootModel) pushState() {
	m.memoriesTab.setState(m.state)
	m.injectionTab.setState(m.state)
	m.historyTab.setState(m.state)
	m.settingsTab.setState(m.state)
}

func (m rootModel) saveState() error {
	return memoryloop.SaveState(m.ctx, m.state) //nolint:wrapcheck // internal helper, callers wrap
}

func (m rootModel) handleAddMemory(msg addMemoryMsg) (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Store == nil || m.isRefreshing {
		return m, nil
	}
	updated, _, err := memoryloop.AddManualRecord(m.state.Store.Records, msg.input, time.Now())
	if err != nil {
		return m, func() tea.Msg { return flashMsg{text: err.Error(), success: false} }
	}
	m.state.Store.Records = updated
	if err := m.saveState(); err != nil {
		return m, func() tea.Msg { return flashMsg{text: fmt.Sprintf("save failed: %v", err), success: false} }
	}
	m.memoriesTab.setState(m.state)
	m.injectionTab.setState(m.state)
	m.historyTab.setState(m.state)
	m.settingsTab.setState(m.state)
	return m, nil
}

func (m rootModel) handlePrune() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Store == nil || m.isRefreshing {
		return m, nil
	}
	updated, result := memoryloop.PruneRecords(m.state.Store.Records, time.Now())
	m.state.Store.Records = updated
	if err := m.saveState(); err != nil {
		return m, func() tea.Msg { return flashMsg{text: fmt.Sprintf("save failed: %v", err), success: false} }
	}
	m.memoriesTab.setState(m.state)
	m.injectionTab.setState(m.state)
	m.historyTab.setState(m.state)
	m.settingsTab.setState(m.state)
	msg := fmt.Sprintf("Pruned %d records", result.ArchivedCount)
	return m, func() tea.Msg { return flashMsg{text: msg, success: true} }
}

func (m rootModel) handleSettingsChanged(msg settingsChangedMsg) (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Store == nil {
		return m, nil
	}
	if msg.mode != nil {
		m.state.Store.Mode = *msg.mode
	}
	if msg.activationPolicy != nil {
		m.state.Store.ActivationPolicy = *msg.activationPolicy
	}
	if msg.maxInjected != nil {
		m.state.Store.MaxInjected = *msg.maxInjected
	}
	if msg.injectionScopes != nil {
		m.state.Store.InjectionScopes = *msg.injectionScopes
	}
	if err := m.saveState(); err != nil {
		return m, func() tea.Msg { return flashMsg{text: fmt.Sprintf("save failed: %v", err), success: false} }
	}
	m.memoriesTab.setState(m.state)
	m.injectionTab.setState(m.state)
	m.historyTab.setState(m.state)
	m.settingsTab.setState(m.state)
	return m, nil
}

func (m rootModel) newDetailPage(record memoryloop.MemoryRecord) *memoryDetailModel {
	detail := newMemoryDetailModel(m.styles, record, m.resolveWizardTargets)
	detail.setSize(m.width, m.contentHeight())
	return &detail
}

func (m rootModel) resolveWizardTargets(record memoryloop.MemoryRecord, location memoryloop.FileLocation) ([]string, error) {
	repoRoot, err := paths.WorktreeRoot(m.ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve repo root: %w", err)
	}

	targets, err := memoryloop.ResolveFileTargetsForRecord(repoRoot, location, record, memoryloop.SkillTargetInput{})
	if err != nil {
		return nil, fmt.Errorf("resolve file targets: %w", err)
	}
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		out = append(out, target.Path)
	}
	return out, nil
}

func (m rootModel) handleWizardResult(msg wizardResultMsg) (tea.Model, tea.Cmd) {
	if msg.success && m.wizardActionHandler != nil {
		return m.applyWizardRequest(msg.request, msg.flash)
	}

	switch msg.request.Intent {
	case WizardIntentSuppress:
		return m.handleWizardLifecycle(msg.request.RecordID, memoryloop.LifecycleActionSuppress, msg.flash)
	case WizardIntentArchive:
		return m.handleWizardLifecycle(msg.request.RecordID, memoryloop.LifecycleActionArchive, msg.flash)
	case WizardIntentAdopt, WizardIntentApply, "":
		if msg.success {
			m.detailPage = nil
			m.wizard = nil
		}
		m.setFlash(msg.flash, msg.success)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
	default:
		m.setFlash(fmt.Sprintf("unknown wizard action: %s", msg.request.Intent), false)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
	}
}

func (m rootModel) applyWizardRequest(request WizardRequest, successFlash string) (tea.Model, tea.Cmd) {
	flash, err := m.wizardActionHandler(m.ctx, request)
	if err != nil {
		m.setFlash(err.Error(), false)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
	}
	if strings.TrimSpace(flash) == "" {
		flash = successFlash
	}

	state, loadErr := memoryloop.LoadState(m.ctx)
	if loadErr != nil {
		m.setFlash(fmt.Sprintf("reload failed: %v", loadErr), false)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
	}

	m.state = state
	m.pushState()
	m.detailPage = nil
	m.wizard = nil
	m.setFlash(flash, true)
	return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
}

func (m rootModel) handleWizardLifecycle(recordID string, action memoryloop.LifecycleAction, successFlash string) (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Store == nil || m.isRefreshing {
		return m, nil
	}

	updated, _, err := memoryloop.TransitionRecordLifecycle(m.state.Store.Records, recordID, action, time.Now().UTC())
	if err != nil {
		m.setFlash(err.Error(), false)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
	}

	m.state.Store.Records = updated
	if err := m.saveState(); err != nil {
		m.setFlash(fmt.Sprintf("save failed: %v", err), false)
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
	}

	m.pushState()
	m.detailPage = nil
	m.wizard = nil
	m.setFlash(successFlash, true)
	return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
}

func (m *rootModel) setFlash(text string, success bool) {
	m.flashText = text
	if success {
		m.flashStyle = m.styles.active
		return
	}
	m.flashStyle = m.styles.errorFlash
}

func (m rootModel) activeTabHints() string {
	if m.detailPage != nil {
		return m.detailPage.hints()
	}
	if m.wizard != nil {
		return m.wizard.hints()
	}
	switch m.activeTab {
	case tabMemories:
		return "j/k navigate · enter open detail · f filter · S scope · n new · ? help"
	case tabInjection:
		return "i focus input · enter test · esc unfocus · j/k navigate · ? help"
	case tabHistory:
		return "j/k navigate · R refresh · ? help"
	case tabSettings:
		return "m mode · p policy · +/- max injected · r/e/b toggle scopes · ? help"
	default:
		return "? help · q quit"
	}
}

func (m rootModel) activeTabInfo() string {
	if m.state == nil || m.state.Store == nil {
		return ""
	}
	switch m.activeTab {
	case tabMemories:
		return fmt.Sprintf("%d records", len(m.state.Store.Records))
	case tabInjection:
		return fmt.Sprintf("%d logs", len(m.state.InjectionLogs))
	case tabHistory:
		return fmt.Sprintf("%d refreshes", len(m.state.Store.RefreshHistory))
	default:
		return ""
	}
}

func (m rootModel) renderHelp() string {
	var b strings.Builder
	b.WriteString(m.styles.render(m.styles.bold, "Keyboard Shortcuts"))
	b.WriteString("\n\n")

	b.WriteString(m.styles.render(m.styles.title, "Global"))
	b.WriteString("\n")
	b.WriteString("  Tab/Shift+Tab  cycle tabs    1-4  jump to tab\n")
	b.WriteString("  q              quit          ?    toggle help\n")
	b.WriteString("\n")

	b.WriteString(m.styles.render(m.styles.title, "Memories"))
	b.WriteString("\n")
	b.WriteString("  j/k  navigate    enter  open detail      w  open detail    f  filter    S  scope\n")
	b.WriteString("  D    prune       n      new memory      /  search\n")
	b.WriteString("\n")
	b.WriteString(m.styles.render(m.styles.title, "Detail Page"))
	b.WriteString("\n")
	b.WriteString("  up/down  choose    enter  next/confirm    esc  back/close\n")
	b.WriteString("\n")

	b.WriteString(m.styles.render(m.styles.title, "Injection"))
	b.WriteString("\n")
	b.WriteString("  i  focus input    enter  test prompt    esc  unfocus    j/k  navigate\n")
	b.WriteString("\n")

	b.WriteString(m.styles.render(m.styles.title, "History"))
	b.WriteString("\n")
	b.WriteString("  j/k  navigate    R  trigger refresh\n")
	b.WriteString("\n")

	b.WriteString(m.styles.render(m.styles.title, "Settings"))
	b.WriteString("\n")
	b.WriteString("  m  cycle mode    p  cycle policy    +/-  max injected\n")
	b.WriteString("  r  toggle repo   e  toggle me       b    toggle branch\n")

	return b.String()
}
