package memorylooptui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

type wizardStage int

const (
	wizardStageAction wizardStage = iota
	wizardStageScope
	wizardStageLocation
	wizardStagePreview
)

type wizardResolver func(record memoryloop.MemoryRecord, location memoryloop.FileLocation) ([]string, error)

//nolint:recvcheck // pointer receiver used for sizing; value updates keep Bubble Tea transitions ergonomic
type wizardModel struct {
	styles         tuiStyles
	width          int
	height         int
	record         memoryloop.MemoryRecord
	stage          wizardStage
	actionIndex    int
	scopeIndex     int
	locationIndex  int
	previewTargets []string
	previewError   string
	request        WizardRequest
	resolveTargets wizardResolver
}

var wizardActionOptions = []struct {
	intent WizardIntent
	label  string
}{
	{intent: WizardIntentAdopt, label: "Adopt to scope"},
	{intent: WizardIntentApply, label: "Apply to files"},
	{intent: WizardIntentSuppress, label: "Suppress"},
	{intent: WizardIntentArchive, label: "Archive"},
}

var wizardScopeOptions = []struct {
	scope memoryloop.ScopeKind
	label string
}{
	{scope: memoryloop.ScopeKindRepo, label: "repo"},
	{scope: memoryloop.ScopeKindMe, label: "me"},
	{scope: memoryloop.ScopeKindBranch, label: "branch"},
}

var wizardLocationOptions = []struct {
	location memoryloop.FileLocation
	label    string
}{
	{location: memoryloop.FileLocationProject, label: "project"},
	{location: memoryloop.FileLocationPersonal, label: "personal"},
}

func newWizardModel(styles tuiStyles, record memoryloop.MemoryRecord, resolve wizardResolver) wizardModel {
	return wizardModel{
		styles:         styles,
		record:         record,
		resolveTargets: resolve,
		stage:          wizardStageAction,
	}
}

func (m *wizardModel) setSize(w, h int) {
	m.width = w
	m.height = h
}

func (m wizardModel) update(msg tea.Msg) (wizardModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if key.Matches(keyMsg, wizardKeyMap.Escape) {
		switch m.stage {
		case wizardStageAction:
			return m, func() tea.Msg { return wizardCloseMsg{} }
		case wizardStageScope, wizardStageLocation:
			m.stage = wizardStageAction
			return m, nil
		case wizardStagePreview:
			switch m.request.Intent {
			case WizardIntentAdopt:
				m.stage = wizardStageScope
			case WizardIntentApply:
				m.stage = wizardStageLocation
			case WizardIntentSuppress, WizardIntentArchive, "":
				m.stage = wizardStageAction
			default:
				m.stage = wizardStageAction
			}
			return m, nil
		}
	}

	switch m.stage {
	case wizardStageAction:
		switch {
		case key.Matches(keyMsg, wizardKeyMap.Up), key.Matches(keyMsg, wizardKeyMap.Left):
			m.actionIndex = wrapIndex(m.actionIndex-1, len(wizardActionOptions))
			return m, nil
		case key.Matches(keyMsg, wizardKeyMap.Down), key.Matches(keyMsg, wizardKeyMap.Right):
			m.actionIndex = wrapIndex(m.actionIndex+1, len(wizardActionOptions))
			return m, nil
		case key.Matches(keyMsg, wizardKeyMap.Enter):
			return m.advanceFromAction()
		}

	case wizardStageScope:
		switch {
		case key.Matches(keyMsg, wizardKeyMap.Up), key.Matches(keyMsg, wizardKeyMap.Left):
			m.scopeIndex = wrapIndex(m.scopeIndex-1, len(wizardScopeOptions))
			return m, nil
		case key.Matches(keyMsg, wizardKeyMap.Down), key.Matches(keyMsg, wizardKeyMap.Right):
			m.scopeIndex = wrapIndex(m.scopeIndex+1, len(wizardScopeOptions))
			return m, nil
		case key.Matches(keyMsg, wizardKeyMap.Enter):
			m.request.Scope = wizardScopeOptions[m.scopeIndex].scope
			m.stage = wizardStagePreview
			m.previewTargets = nil
			m.previewError = ""
			return m, nil
		}

	case wizardStageLocation:
		switch {
		case key.Matches(keyMsg, wizardKeyMap.Up), key.Matches(keyMsg, wizardKeyMap.Left):
			m.locationIndex = wrapIndex(m.locationIndex-1, len(wizardLocationOptions))
			return m, nil
		case key.Matches(keyMsg, wizardKeyMap.Down), key.Matches(keyMsg, wizardKeyMap.Right):
			m.locationIndex = wrapIndex(m.locationIndex+1, len(wizardLocationOptions))
			return m, nil
		case key.Matches(keyMsg, wizardKeyMap.Enter):
			m.request.Location = wizardLocationOptions[m.locationIndex].location
			targets, err := m.resolvePreviewTargets()
			if err != nil {
				m.previewTargets = nil
				m.previewError = err.Error()
			} else {
				m.previewTargets = targets
				m.previewError = ""
				m.request.Targets = append([]string(nil), targets...)
			}
			m.stage = wizardStagePreview
			return m, nil
		}

	case wizardStagePreview:
		if key.Matches(keyMsg, wizardKeyMap.Enter) {
			if err := m.confirmationError(); err != nil {
				return m, func() tea.Msg {
					return wizardResultMsg{
						success: false,
						flash:   err.Error(),
						request: m.request,
					}
				}
			}
			return m, func() tea.Msg {
				return wizardResultMsg{
					success: true,
					flash:   m.confirmationFlash(),
					request: m.request,
				}
			}
		}
	}

	return m, nil
}

func (m wizardModel) advanceFromAction() (wizardModel, tea.Cmd) {
	selected := wizardActionOptions[m.actionIndex]
	m.request.Intent = selected.intent
	m.request.RecordID = m.record.ID
	switch selected.intent {
	case WizardIntentAdopt:
		m.stage = wizardStageScope
		m.request.Scope = wizardScopeOptions[m.scopeIndex].scope
		m.request.Location = ""
		m.request.Targets = nil
		m.previewTargets = nil
		m.previewError = ""
	case WizardIntentApply:
		m.stage = wizardStageLocation
		m.request.Location = wizardLocationOptions[m.locationIndex].location
		m.request.Scope = ""
		m.request.Targets = nil
		m.previewTargets = nil
		m.previewError = ""
	case WizardIntentSuppress, WizardIntentArchive, "":
		m.stage = wizardStagePreview
		m.request.Scope = ""
		m.request.Location = ""
		m.request.Targets = nil
		m.previewTargets = nil
		m.previewError = ""
	default:
		m.stage = wizardStagePreview
		m.request.Scope = ""
		m.request.Location = ""
		m.request.Targets = nil
		m.previewTargets = nil
		m.previewError = ""
	}
	return m, nil
}

func (m wizardModel) resolvePreviewTargets() ([]string, error) {
	if m.resolveTargets == nil {
		return nil, errors.New("no target resolver configured")
	}
	targets, err := m.resolveTargets(m.record, m.request.Location)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(targets))
	paths = append(paths, targets...)
	return paths, nil
}

func (m wizardModel) confirmationError() error {
	switch m.request.Intent {
	case WizardIntentAdopt:
		return nil
	case WizardIntentApply:
		if len(m.previewTargets) == 0 {
			if m.previewError != "" {
				return errors.New(m.previewError)
			}
			return errors.New("no targets resolved")
		}
		return nil
	case WizardIntentSuppress, WizardIntentArchive:
		return nil
	default:
		return fmt.Errorf("unknown wizard action: %s", m.request.Intent)
	}
}

func (m wizardModel) confirmationFlash() string {
	switch m.request.Intent {
	case WizardIntentAdopt:
		return "Prepared adoption request for " + m.record.Title
	case WizardIntentApply:
		return fmt.Sprintf("Prepared apply request for %d target(s)", len(m.previewTargets))
	case WizardIntentSuppress:
		return "Suppressed " + m.record.Title
	case WizardIntentArchive:
		return "Archived " + m.record.Title
	default:
		return "Prepared wizard request"
	}
}

func (m wizardModel) view() string {
	var b strings.Builder
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.bold, "WIZARD"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, "Memory: "+m.record.Title))
	b.WriteString("\n\n")

	switch m.stage {
	case wizardStageAction:
		b.WriteString(m.renderActionSelectionList())
	case wizardStageScope:
		b.WriteString(m.renderScopeSelectionList())
	case wizardStageLocation:
		b.WriteString(m.renderLocationSelectionList())
	case wizardStagePreview:
		b.WriteString(m.renderPreview())
	}

	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.dim, m.hints()))
	b.WriteString("\n")
	return b.String()
}

func (m wizardModel) renderActionSelectionList() string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.title, "Choose an action"))
	b.WriteString("\n\n")
	for i, option := range wizardActionOptions {
		label := m.actionOptionLabel(option.intent)
		prefix := "  "
		if i == m.actionIndex {
			prefix = ">"
			label = m.styles.render(m.styles.selected, label)
		}
		b.WriteString(prefix)
		b.WriteString(" ")
		b.WriteString(label)
		b.WriteString("\n")
	}
	return b.String()
}

func (m wizardModel) renderScopeSelectionList() string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.title, "Adopt to scope"))
	b.WriteString("\n\n")
	for i, option := range wizardScopeOptions {
		label := option.label
		prefix := "  "
		if i == m.scopeIndex {
			prefix = ">"
			label = m.styles.render(m.styles.selected, label)
		}
		b.WriteString(prefix)
		b.WriteString(" ")
		b.WriteString(label)
		b.WriteString("\n")
	}
	return b.String()
}

func (m wizardModel) renderLocationSelectionList() string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.title, m.applyLocationTitle()))
	b.WriteString("\n\n")
	for i, option := range wizardLocationOptions {
		label := option.label
		prefix := "  "
		if i == m.locationIndex {
			prefix = ">"
			label = m.styles.render(m.styles.selected, label)
		}
		b.WriteString(prefix)
		b.WriteString(" ")
		b.WriteString(label)
		b.WriteString("\n")
	}
	return b.String()
}

func (m wizardModel) renderPreview() string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.title, "Preview"))
	b.WriteString("\n\n")
	b.WriteString("  ")
	fmt.Fprintf(&b, "Action: %s", wizardActionLabel(m.request.Intent))
	b.WriteString("\n")

	switch m.request.Intent {
	case WizardIntentAdopt:
		b.WriteString("  ")
		fmt.Fprintf(&b, "Scope: %s", m.request.Scope)
		b.WriteString("\n")
	case WizardIntentApply:
		b.WriteString("  ")
		fmt.Fprintf(&b, "Location: %s", m.request.Location)
		b.WriteString("\n")
		if m.previewError != "" {
			b.WriteString("  ")
			b.WriteString(m.styles.render(m.styles.suppressed, m.previewError))
			b.WriteString("\n")
		} else if len(m.previewTargets) > 0 {
			b.WriteString("\n")
			b.WriteString("  Targets:\n")
			for _, target := range m.previewTargets {
				b.WriteString("  - ")
				b.WriteString(target)
				b.WriteString("\n")
			}
		}
	case WizardIntentSuppress, WizardIntentArchive, "":
		b.WriteString("\n")
	default:
		b.WriteString("\n")
	}

	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.bold, "Press enter to confirm"))
	b.WriteString("\n")
	return b.String()
}

func (m wizardModel) hints() string {
	switch m.stage {
	case wizardStageAction:
		return "up/down choose · enter next · esc close"
	case wizardStageScope, wizardStageLocation:
		return "up/down choose · enter preview · esc back"
	case wizardStagePreview:
		return "enter confirm · esc back"
	default:
		return "esc close"
	}
}

func (m wizardModel) actionOptionLabel(intent WizardIntent) string {
	if intent == WizardIntentApply {
		if memoryloop.RecordUsesSkillFileTargets(m.record) {
			return "Apply to skill files"
		}
		return "Apply to agent files"
	}
	return wizardActionLabel(intent)
}

func (m wizardModel) applyLocationTitle() string {
	if memoryloop.RecordUsesSkillFileTargets(m.record) {
		return "Apply to skill files"
	}
	return "Apply to agent files"
}

func wizardActionLabel(intent WizardIntent) string {
	switch intent {
	case WizardIntentAdopt:
		return "Adopt to scope"
	case WizardIntentApply:
		return "Apply to files"
	case WizardIntentSuppress:
		return "Suppress"
	case WizardIntentArchive:
		return "Archive"
	default:
		return string(intent)
	}
}

func wrapIndex(idx, size int) int {
	if size <= 0 {
		return 0
	}
	if idx < 0 {
		return size - 1
	}
	if idx >= size {
		return 0
	}
	return idx
}
