package memorylooptui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

var modeOrder = []memoryloop.Mode{memoryloop.ModeOff, memoryloop.ModeManual, memoryloop.ModeAuto}
var policyOrder = []memoryloop.ActivationPolicy{memoryloop.ActivationPolicyReview, memoryloop.ActivationPolicyAuto}

//nolint:recvcheck // bubbletea pattern: pointer receivers for mutation, value for update/view
type settingsModel struct {
	state  *memoryloop.State
	styles tuiStyles
	width  int
	height int
}

func (m *settingsModel) setState(state *memoryloop.State) { m.state = state }
func (m *settingsModel) setSize(w, h int)                 { m.width = w; m.height = h }

func (m settingsModel) update(msg tea.Msg) (settingsModel, tea.Cmd) {
	if m.state == nil || m.state.Store == nil {
		return m, nil
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch {
		case key.Matches(keyMsg, settingsKeyMap.Mode):
			next := cycleMode(m.state.Store.Mode)
			return m, func() tea.Msg { return settingsChangedMsg{mode: &next} }

		case key.Matches(keyMsg, settingsKeyMap.Policy):
			next := cyclePolicy(m.state.Store.ActivationPolicy)
			return m, func() tea.Msg { return settingsChangedMsg{activationPolicy: &next} }

		case key.Matches(keyMsg, settingsKeyMap.MaxUp):
			next := m.state.Store.MaxInjected + 1
			if next > 10 {
				next = 10
			}
			return m, func() tea.Msg { return settingsChangedMsg{maxInjected: &next} }

		case key.Matches(keyMsg, settingsKeyMap.MaxDown):
			next := m.state.Store.MaxInjected - 1
			if next < 1 {
				next = 1
			}
			return m, func() tea.Msg { return settingsChangedMsg{maxInjected: &next} }
		}
	}
	return m, nil
}

func (m settingsModel) view() string {
	if m.state == nil || m.state.Store == nil {
		return "\n  No settings available.\n"
	}
	store := m.state.Store

	var b strings.Builder
	b.WriteString("\n")

	// Mode
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.bold, "Mode"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, "Controls whether active memories inject into prompts"))
	b.WriteString("\n  ")
	for _, mode := range modeOrder {
		label := string(mode)
		if mode == store.Mode {
			b.WriteString(m.styles.render(m.styles.active, fmt.Sprintf("[%s]", label)))
		} else {
			b.WriteString(m.styles.render(m.styles.dim, fmt.Sprintf(" %s ", label)))
		}
		b.WriteString("  ")
	}
	b.WriteString("\n\n")

	// Policy
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.bold, "Activation Policy"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, "What happens to newly generated memories"))
	b.WriteString("\n  ")
	for _, pol := range policyOrder {
		label := string(pol)
		if pol == store.ActivationPolicy {
			b.WriteString(m.styles.render(m.styles.candidate, fmt.Sprintf("[%s]", label)))
		} else {
			b.WriteString(m.styles.render(m.styles.dim, fmt.Sprintf(" %s ", label)))
		}
		b.WriteString("  ")
	}
	b.WriteString("\n\n")

	// Max Injected
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.bold, "Max Injected"))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, "Maximum memories per prompt injection"))
	b.WriteString("\n  ")
	fmt.Fprintf(&b, "< %s >",
		m.styles.render(m.styles.title, fmt.Sprintf(" %d ", store.MaxInjected)))
	b.WriteString("\n\n")

	// Injection enabled
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.bold, "Injection"))
	b.WriteString("\n  ")
	if store.InjectionEnabled {
		b.WriteString(m.styles.render(m.styles.active, "* enabled"))
	} else {
		b.WriteString(m.styles.render(m.styles.suppressed, "o disabled"))
	}
	b.WriteString("\n\n")

	// Stats
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim, "Last refresh: "+timeAgo(store.GeneratedAt)))
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.dim, fmt.Sprintf("Store version: %d", store.Version)))
	b.WriteString("\n  ")
	b.WriteString(m.styles.render(m.styles.dim, fmt.Sprintf("Source window: %d sessions", store.SourceWindow)))
	b.WriteString("\n")

	return b.String()
}

func cycleMode(current memoryloop.Mode) memoryloop.Mode {
	for i, m := range modeOrder {
		if m == current {
			return modeOrder[(i+1)%len(modeOrder)]
		}
	}
	return memoryloop.ModeOff
}

func cyclePolicy(current memoryloop.ActivationPolicy) memoryloop.ActivationPolicy {
	for i, p := range policyOrder {
		if p == current {
			return policyOrder[(i+1)%len(policyOrder)]
		}
	}
	return memoryloop.ActivationPolicyReview
}
