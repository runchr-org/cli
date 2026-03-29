package skilltui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var tabNames = [3]string{"Stats", "Friction", "Improve"}

func renderTabBar(s tuiStyles, activeTab int, _ int, skillName string) string {
	var b strings.Builder

	// App title
	b.WriteString(s.render(s.appTitle, "SKILL IMPROVEMENT"))
	b.WriteString("  ")

	// Skill name indicator
	skillLabel := fmt.Sprintf("\u2039 %s \u203a", skillName)
	b.WriteString(s.render(s.dim, skillLabel))
	b.WriteString("   ")

	// Use visible width (not byte length) for underline alignment.
	// lipgloss.Width strips ANSI codes and counts display columns correctly.
	activeStart := 0
	activeWidth := 0
	pos := lipgloss.Width(b.String())

	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == activeTab {
			activeStart = pos
			activeWidth = len(label)
			b.WriteString(s.render(s.tabActive, label))
		} else {
			b.WriteString(s.render(s.tabInactive, label))
		}
		pos += len(label) // tab labels are ASCII-only, len() is fine
		if i < len(tabNames)-1 {
			b.WriteString("   ")
			pos += 3
		}
	}

	line1 := b.String()

	// Underline row: spaces aligned to visible column position
	line2 := strings.Repeat(" ", activeStart) +
		s.render(s.tabUnderline, strings.Repeat("\u2500", activeWidth))

	return line1 + "\n" + line2
}

func renderPickerHeader(s tuiStyles) string {
	var b strings.Builder

	b.WriteString(s.render(s.appTitle, "SKILL IMPROVEMENT ENGINE"))
	b.WriteString("\n")

	return b.String()
}

func renderStatusBar(s tuiStyles, hints string, info string, width int) string {
	hintsLen := len(hints)
	infoLen := len(info)
	padding := width - hintsLen - infoLen
	if padding < 1 {
		padding = 1
	}
	return s.render(s.statusBar, hints) + strings.Repeat(" ", padding) + s.render(s.dim, info)
}

func formatTokens(tokens int) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%dK", tokens/1_000)
	default:
		return strconv.Itoa(tokens)
	}
}

func formatTokens64(tokens int64) string {
	return formatTokens(int(tokens))
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02")
}

// padRight pads a string to a visible width, accounting for ANSI escape codes.
func padRight(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}
