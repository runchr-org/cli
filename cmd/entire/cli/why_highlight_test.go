package cli

import (
	"slices"
	"strings"
	"testing"
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
