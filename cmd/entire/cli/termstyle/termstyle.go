// Package termstyle provides shared terminal styling utilities for CLI output.
// It wraps lipgloss styles with color/width detection so callers don't need
// to handle terminal detection themselves.
package termstyle

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/agent"
	"golang.org/x/term"
)

// Styles holds pre-built lipgloss styles and terminal metadata.
// ColorEnabled and Width are exported so callers can read them, but
// mutation of individual style fields should be done via assignment to the
// whole Styles value (returned from New).
type Styles struct {
	ColorEnabled bool
	Width        int

	Green  lipgloss.Style
	Red    lipgloss.Style
	Yellow lipgloss.Style
	Gray   lipgloss.Style
	Bold   lipgloss.Style
	Dim    lipgloss.Style
	Agent  lipgloss.Style // amber/orange for agent names
	Cyan   lipgloss.Style
}

// New creates a Styles value appropriate for the given output writer.
// Color is disabled when the writer is not a terminal or when NO_COLOR is set.
// Width is capped at 80 with a fallback of 60 when no terminal size is available.
func New(w io.Writer) Styles {
	useColor := ShouldUseColor(w)
	width := GetTerminalWidth(w)

	s := Styles{
		ColorEnabled: useColor,
		Width:        width,
	}

	if useColor {
		s.Green = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
		s.Red = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		s.Yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
		s.Gray = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.Bold = lipgloss.NewStyle().Bold(true)
		s.Dim = lipgloss.NewStyle().Faint(true)
		s.Agent = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
		s.Cyan = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	}

	return s
}

// Render applies the given style to text only when color is enabled.
// When color is disabled the text is returned unchanged so output stays
// machine-readable (e.g. in CI or when piped).
func (s Styles) Render(style lipgloss.Style, text string) string {
	if !s.ColorEnabled {
		return text
	}
	return style.Render(text)
}

// ShouldUseColor returns true if the writer supports color output.
// Color is suppressed when the NO_COLOR environment variable is non-empty,
// or when the writer is not an *os.File connected to a terminal.
func ShouldUseColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd())) //nolint:gosec // G115: uintptr->int is safe for fd
	}
	return false
}

// GetTerminalWidth returns the terminal width, capped at 80 with a fallback of 60.
// It first checks the writer itself, then falls back to Stdout/Stderr.
func GetTerminalWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 { //nolint:gosec // G115: uintptr->int is safe for fd
			return min(width, 80)
		}
	}

	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if f == nil {
			continue
		}
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 { //nolint:gosec // G115: uintptr->int is safe for fd
			return min(width, 80)
		}
	}

	return 60
}

// FormatTokenCount formats a token count for compact display.
// Values below 1000 are rendered as plain integers; larger values use a
// one-decimal-place "k" suffix with the trailing ".0" trimmed
// (e.g. 0→"0", 500→"500", 1000→"1k", 1200→"1.2k", 14300→"14.3k").
func FormatTokenCount(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	f := float64(n) / 1000.0
	s := fmt.Sprintf("%.1f", f)
	s = strings.TrimSuffix(s, ".0")
	return s + "k"
}

// TotalTokens recursively sums all token fields in a TokenUsage value,
// including any subagent tokens. Returns 0 for a nil pointer.
func TotalTokens(tu *agent.TokenUsage) int {
	if tu == nil {
		return 0
	}
	total := tu.InputTokens + tu.CacheCreationTokens + tu.CacheReadTokens + tu.OutputTokens
	total += TotalTokens(tu.SubagentTokens)
	return total
}

// HorizontalRule renders a dimmed horizontal rule spanning the stored width.
func (s Styles) HorizontalRule() string {
	rule := strings.Repeat("─", s.Width)
	return s.Render(s.Dim, rule)
}

// SectionRule renders a section header of the form: ── Label ────────────
// The trailing dashes fill the remaining width; trailing is at least 1.
func (s Styles) SectionRule(label string) string {
	prefix := "── "
	content := label + " "
	usedWidth := len([]rune(prefix)) + len([]rune(content))
	trailing := s.Width - usedWidth
	if trailing < 1 {
		trailing = 1
	}

	var b strings.Builder
	b.WriteString(s.Render(s.Dim, "── "))
	b.WriteString(s.Render(s.Dim, label))
	b.WriteString(" ")
	b.WriteString(s.Render(s.Dim, strings.Repeat("─", trailing)))
	return b.String()
}
