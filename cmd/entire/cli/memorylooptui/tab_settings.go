package memorylooptui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

var modeOrder = []memoryloop.Mode{memoryloop.ModeOff, memoryloop.ModeManual, memoryloop.ModeAuto}
var policyOrder = []memoryloop.ActivationPolicy{memoryloop.ActivationPolicyReview, memoryloop.ActivationPolicyAuto}
var thresholdOrder = []string{"relaxed", "balanced", "strict"}

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

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	var changed settingsChangedMsg
	hasChange := false

	switch {
	case key.Matches(keyMsg, settingsKeyMap.Mode):
		next := cycleMode(m.state.Store.Mode)
		changed.mode = &next
		hasChange = true

	case key.Matches(keyMsg, settingsKeyMap.Policy):
		next := cyclePolicy(m.state.Store.ActivationPolicy)
		changed.activationPolicy = &next
		hasChange = true

	case key.Matches(keyMsg, settingsKeyMap.MaxUp):
		next := m.state.Store.MaxInjected + 1
		if next > 10 {
			next = 10
		}
		changed.maxInjected = &next
		hasChange = true

	case key.Matches(keyMsg, settingsKeyMap.MaxDown):
		next := m.state.Store.MaxInjected - 1
		if next < 1 {
			next = 1
		}
		changed.maxInjected = &next
		hasChange = true

	case key.Matches(keyMsg, settingsKeyMap.ScopeRepo):
		scopes := toggleScope(m.state.Store.InjectionScopes, memoryloop.ScopeKindRepo)
		changed.injectionScopes = &scopes
		hasChange = true

	case key.Matches(keyMsg, settingsKeyMap.ScopeMe):
		scopes := toggleScope(m.state.Store.InjectionScopes, memoryloop.ScopeKindMe)
		changed.injectionScopes = &scopes
		hasChange = true

	case key.Matches(keyMsg, settingsKeyMap.ScopeBranch):
		scopes := toggleScope(m.state.Store.InjectionScopes, memoryloop.ScopeKindBranch)
		changed.injectionScopes = &scopes
		hasChange = true

	case key.Matches(keyMsg, settingsKeyMap.Threshold):
		next := cycleThreshold(m.state.Store.GenerationThreshold)
		changed.generationThreshold = &next
		hasChange = true
	}

	if hasChange {
		return m, func() tea.Msg { return changed }
	}
	return m, nil
}

func (m settingsModel) view() string {
	if m.state == nil || m.state.Store == nil {
		return "\n  No settings available.\n"
	}
	store := m.state.Store

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("245")).
		Padding(0, 1).
		Width(m.width - 2)

	// Chip styles for selected vs unselected options
	selectedChip := lipgloss.NewStyle().
		Background(lipgloss.Color("214")).
		Foreground(lipgloss.Color("0")).
		Bold(true).
		Padding(0, 1)
	unselectedChip := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Padding(0, 1)

	var b strings.Builder
	b.WriteString("\n")

	// Mode card
	{
		var c strings.Builder
		c.WriteString(m.styles.render(m.styles.bold, "Mode"))
		c.WriteString("  ")
		c.WriteString(m.styles.render(m.styles.dim, "Controls whether active memories inject into prompts"))
		c.WriteString("\n")
		for _, mode := range modeOrder {
			label := string(mode)
			if mode == store.Mode {
				c.WriteString(selectedChip.Render(label))
			} else {
				c.WriteString(unselectedChip.Render(label))
			}
			c.WriteString(" ")
		}
		b.WriteString(cardStyle.Render(c.String()))
		b.WriteString("\n")
	}

	// Policy card
	{
		var c strings.Builder
		c.WriteString(m.styles.render(m.styles.bold, "Activation Policy"))
		c.WriteString("  ")
		c.WriteString(m.styles.render(m.styles.dim, "What happens to newly generated memories"))
		c.WriteString("\n")
		for _, pol := range policyOrder {
			label := string(pol)
			if pol == store.ActivationPolicy {
				c.WriteString(selectedChip.Render(label))
			} else {
				c.WriteString(unselectedChip.Render(label))
			}
			c.WriteString(" ")
		}
		b.WriteString(cardStyle.Render(c.String()))
		b.WriteString("\n")
	}

	// Max Injected card
	{
		var c strings.Builder
		c.WriteString(m.styles.render(m.styles.bold, "Max Injected"))
		c.WriteString("  ")
		c.WriteString(m.styles.render(m.styles.dim, "Maximum memories per prompt injection"))
		c.WriteString("\n")
		numStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)
		fmt.Fprintf(&c, "  ◀  %s  ▶", numStyle.Render(fmt.Sprintf(" %d ", store.MaxInjected)))
		b.WriteString(cardStyle.Render(c.String()))
		b.WriteString("\n")
	}

	// Injection Scopes card
	{
		var c strings.Builder
		c.WriteString(m.styles.render(m.styles.bold, "Injection Scopes"))
		c.WriteString("  ")
		c.WriteString(m.styles.render(m.styles.dim, "Which memory scopes get injected into prompts"))
		c.WriteString("\n")
		activeScopes := effectiveInjectionScopes(store.InjectionScopes)
		for _, scope := range []memoryloop.ScopeKind{memoryloop.ScopeKindRepo, memoryloop.ScopeKindMe, memoryloop.ScopeKindBranch} {
			label := string(scope)
			if hasScopeKind(activeScopes, scope) {
				c.WriteString(selectedChip.Render(label))
			} else {
				c.WriteString(unselectedChip.Render(label))
			}
			c.WriteString(" ")
		}
		b.WriteString(cardStyle.Render(c.String()))
		b.WriteString("\n")
	}

	// Generation Threshold card
	{
		var c strings.Builder
		c.WriteString(m.styles.render(m.styles.bold, "Generation Threshold"))
		c.WriteString("  ")
		c.WriteString(m.styles.render(m.styles.dim, "Controls how aggressively memories are filtered during refresh"))
		c.WriteString("\n")
		current := store.GenerationThreshold
		if current == "" {
			current = "balanced"
		}
		hasOverrides := store.GenerationOverrides != nil
		for _, preset := range thresholdOrder {
			label := preset
			if preset == current {
				if hasOverrides {
					label += "*"
				}
				c.WriteString(selectedChip.Render(label))
			} else {
				c.WriteString(unselectedChip.Render(label))
			}
			c.WriteString(" ")
		}
		if hasOverrides {
			c.WriteString("\n")
			c.WriteString(m.styles.render(m.styles.dim, "Overrides active — run 'entire memory-loop threshold --clear-overrides' to reset"))
		}
		b.WriteString(cardStyle.Render(c.String()))
		b.WriteString("\n")
	}

	// Injection status card
	{
		var c strings.Builder
		c.WriteString(m.styles.render(m.styles.bold, "Injection"))
		c.WriteString("  ")
		if store.InjectionEnabled {
			c.WriteString(m.styles.render(m.styles.active, "● enabled"))
		} else {
			c.WriteString(m.styles.render(m.styles.suppressed, "○ disabled"))
		}
		b.WriteString(cardStyle.Render(c.String()))
		b.WriteString("\n")
	}

	// Stats
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(m.styles.render(m.styles.dim,
		fmt.Sprintf("Last refresh: %s  ·  Store version: %d  ·  Source window: %d sessions",
			timeAgo(store.GeneratedAt), store.Version, store.SourceWindow)))
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

func cycleThreshold(current string) string {
	if current == "" {
		current = "balanced"
	}
	for i, t := range thresholdOrder {
		if t == current {
			return thresholdOrder[(i+1)%len(thresholdOrder)]
		}
	}
	return "balanced"
}

// effectiveInjectionScopes returns the active scopes, defaulting to all if empty.
func effectiveInjectionScopes(scopes []memoryloop.ScopeKind) []memoryloop.ScopeKind {
	if len(scopes) == 0 {
		return memoryloop.DefaultInjectionScopes()
	}
	return scopes
}

func hasScopeKind(scopes []memoryloop.ScopeKind, target memoryloop.ScopeKind) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}

// toggleScope adds or removes a scope kind from the list.
// Starts from DefaultInjectionScopes if the current list is empty.
func toggleScope(current []memoryloop.ScopeKind, target memoryloop.ScopeKind) []memoryloop.ScopeKind {
	if len(current) == 0 {
		current = memoryloop.DefaultInjectionScopes()
	}
	// Check if target is already present.
	for i, s := range current {
		if s == target {
			// Remove it (but don't allow removing all scopes).
			if len(current) <= 1 {
				return current
			}
			return append(current[:i], current[i+1:]...)
		}
	}
	// Add it.
	return append(current, target)
}
