package termstyle_test

import (
	"bytes"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
)

// TestNew_NoColor verifies that New returns a Styles with ColorEnabled=false
// when the writer is not a terminal (e.g. bytes.Buffer).
func TestNew_NoColor(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := termstyle.New(&buf)
	if s.ColorEnabled {
		t.Error("expected ColorEnabled=false for non-terminal writer")
	}
}

// TestNew_Width verifies that New returns a fallback width when no terminal is present.
func TestNew_Width(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := termstyle.New(&buf)
	if s.Width != 60 {
		t.Errorf("expected Width=60 fallback, got %d", s.Width)
	}
}

// TestShouldUseColor_NoColorEnv verifies that NO_COLOR env disables color.
func TestShouldUseColor_NoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := termstyle.ShouldUseColor(&bytes.Buffer{})
	if got {
		t.Error("expected false when NO_COLOR is set")
	}
}

// TestShouldUseColor_NonTerminal verifies that a buffer writer returns false.
func TestShouldUseColor_NonTerminal(t *testing.T) {
	t.Parallel()
	got := termstyle.ShouldUseColor(&bytes.Buffer{})
	if got {
		t.Error("expected false for non-terminal writer")
	}
}

// TestGetTerminalWidth_Fallback verifies the fallback width of 60.
func TestGetTerminalWidth_Fallback(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	got := termstyle.GetTerminalWidth(&buf)
	if got != 60 {
		t.Errorf("expected fallback width 60, got %d", got)
	}
}

// TestFormatTokenCount covers the token count formatting rules.
func TestFormatTokenCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1k"},
		{1200, "1.2k"},
		{14300, "14.3k"},
		{100000, "100k"},
	}
	for _, tt := range tests {
		got := termstyle.FormatTokenCount(tt.input)
		if got != tt.want {
			t.Errorf("FormatTokenCount(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestTotalTokens_Nil verifies nil input returns 0.
func TestTotalTokens_Nil(t *testing.T) {
	t.Parallel()
	if got := termstyle.TotalTokens(nil); got != 0 {
		t.Errorf("TotalTokens(nil) = %d, want 0", got)
	}
}

// TestTotalTokens_Basic verifies basic token summation.
func TestTotalTokens_Basic(t *testing.T) {
	t.Parallel()
	tu := &agent.TokenUsage{
		InputTokens:         10,
		CacheCreationTokens: 5,
		CacheReadTokens:     3,
		OutputTokens:        2,
	}
	want := 20
	if got := termstyle.TotalTokens(tu); got != want {
		t.Errorf("TotalTokens = %d, want %d", got, want)
	}
}

// TestTotalTokens_Recursive verifies subagent tokens are included.
func TestTotalTokens_Recursive(t *testing.T) {
	t.Parallel()
	tu := &agent.TokenUsage{
		InputTokens:  10,
		OutputTokens: 5,
		SubagentTokens: &agent.TokenUsage{
			InputTokens:  3,
			OutputTokens: 2,
		},
	}
	want := 20 // 10+5 + 3+2
	if got := termstyle.TotalTokens(tu); got != want {
		t.Errorf("TotalTokens = %d, want %d", got, want)
	}
}

// TestRender_NoColor verifies Render returns plain text when color is disabled.
func TestRender_NoColor(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := termstyle.New(&buf)
	got := s.Render(s.Bold, "hello")
	if got != "hello" {
		t.Errorf("Render with no color = %q, want %q", got, "hello")
	}
}

// TestHorizontalRule_NoColor verifies HorizontalRule returns plain dashes when no color.
func TestHorizontalRule_NoColor(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := termstyle.New(&buf)
	// Override width for deterministic output
	s.Width = 5
	got := s.HorizontalRule()
	want := "─────"
	if got != want {
		t.Errorf("HorizontalRule() = %q, want %q", got, want)
	}
}

// TestSectionRule_NoColor verifies SectionRule output format without color.
func TestSectionRule_NoColor(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := termstyle.New(&buf)
	s.Width = 20
	got := s.SectionRule("Foo")
	// "── Foo " = 7 runes used, trailing = 13
	want := "── Foo " + "─────────────"
	if got != want {
		t.Errorf("SectionRule(%q) = %q, want %q", "Foo", got, want)
	}
}

// TestSectionRule_ShortWidth verifies trailing is at least 1 even when label is long.
func TestSectionRule_ShortWidth(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := termstyle.New(&buf)
	s.Width = 1
	got := s.SectionRule("Very Long Label")
	// trailing forced to 1 minimum
	want := "── Very Long Label " + "─"
	if got != want {
		t.Errorf("SectionRule short width = %q, want %q", got, want)
	}
}
