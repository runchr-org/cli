package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestWhyHighlight_KnownExtensionUsesLexer(t *testing.T) {
	t.Parallel()

	lines := []string{"package main", "func main() {}"}
	got := highlightWhyCodeLines("main.go", lines, true)

	if len(got) != len(lines) {
		t.Fatalf("highlighted lines = %d, want %d", len(got), len(lines))
	}
	if !strings.Contains(got[0], "package") || !strings.Contains(got[0], "main") {
		t.Fatalf("first highlighted line = %q, want source text", got[0])
	}
	if !strings.Contains(got[0], "\x1b[") {
		t.Fatalf("first highlighted line = %q, want ANSI styling", got[0])
	}
}

func TestWhyHighlight_UsesDarkDefaultStyle(t *testing.T) {
	t.Parallel()

	if got := whyHighlightStyle().Name; got != whyHighlightStyleName {
		t.Fatalf("highlight style = %q, want %q", got, whyHighlightStyleName)
	}
}

func TestWhyHighlight_UnknownExtensionReturnsPlainCode(t *testing.T) {
	t.Parallel()

	lines := []string{"plain line", "another line"}
	got := highlightWhyCodeLines("file.unknown-extension-for-why", lines, true)

	if !slices.Equal(got, lines) {
		t.Fatalf("highlighted lines = %#v, want %#v", got, lines)
	}
}

func TestWhyHighlight_ColorDisabledReturnsPlainCode(t *testing.T) {
	t.Parallel()

	lines := []string{"package main", "func main() {}"}
	got := highlightWhyCodeLines("main.go", lines, false)

	if !slices.Equal(got, lines) {
		t.Fatalf("highlighted lines = %#v, want %#v", got, lines)
	}
}

func TestWhyHighlight_PreservesLineCountForEmptyAndTrailingLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		lines []string
	}{
		{name: "empty file", lines: nil},
		{name: "single empty line", lines: []string{""}},
		{name: "trailing empty line", lines: []string{"package main", ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := highlightWhyCodeLines("main.go", tt.lines, true)
			if len(got) != len(tt.lines) {
				t.Fatalf("highlighted lines = %d, want %d: %#v", len(got), len(tt.lines), got)
			}
		})
	}
}

func TestWhyHighlight_TruncatesAfterLexingFullContent(t *testing.T) {
	t.Parallel()

	lines := []string{
		`"github.com/entireio/cli/cmd/entire/cli/transcript/compact"`,
		`"github.com/entireio/cli/redact"`,
	}
	got := highlightWhyCodeLines("main.go", lines, true, 28)

	if len(got) != len(lines) {
		t.Fatalf("highlighted lines = %d, want %d: %#v", len(got), len(lines), got)
	}
	for i, line := range got {
		if width := lipgloss.Width(line); width > 28 {
			t.Fatalf("line %d width = %d, want <= 28: %q", i, width, line)
		}
		if !strings.Contains(line, "\x1b[") {
			t.Fatalf("line %d = %q, want ANSI styling", i, line)
		}
	}

	visibleSecond := whyANSIRe.ReplaceAllString(got[1], "")
	if !strings.HasPrefix(visibleSecond, `"github.com/entireio/cli`) {
		t.Fatalf("second line visible text = %q, want independently highlighted import path prefix", visibleSecond)
	}
}
