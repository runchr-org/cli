package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/search"
)

// TestSearchCmd_AccessibleModeRequiresQuery verifies that accessible mode
// is treated like --json: a query is required when ACCESSIBLE=1.
// Note: this test modifies process-global state (env var), so it must NOT
// use t.Parallel().
func TestSearchCmd_AccessibleModeRequiresQuery(t *testing.T) {
	t.Setenv("ACCESSIBLE", "1")

	root := NewRootCmd()
	root.SetArgs([]string{"search", "--json"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when no query with --json + ACCESSIBLE=1")
	}

	want := "query required when using --json, accessible mode, or piped output"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want containing %q", err.Error(), want)
	}
}

func TestSearchCmd_HelpMentionsRepoFlagAndInlineFilters(t *testing.T) {
	t.Parallel()

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"search", "-h"})

	if err := root.Execute(); err != nil {
		t.Fatalf("help command failed: %v", err)
	}

	help := buf.String()
	if !strings.Contains(help, "--repo") {
		t.Fatalf("help missing --repo flag:\n%s", help)
	}
	if !strings.Contains(help, "inline filters") {
		t.Fatalf("help missing inline filter note:\n%s", help)
	}
	if !strings.Contains(help, "repo:*") {
		t.Fatalf("help missing repo:* inline example:\n%s", help)
	}
}

func TestWriteSearchJSON_ZeroLimitFallsBackToDefaultPageSize(t *testing.T) {
	t.Parallel()

	resp := &search.Response{
		Results: testResults(),
		Total:   2,
		Page:    1,
	}

	var buf bytes.Buffer
	if err := writeSearchJSON(&buf, resp, 0, 1); err != nil {
		t.Fatalf("writeSearchJSON returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"limit": 25`) {
		t.Fatalf("output missing default limit fallback:\n%s", output)
	}
	if !strings.Contains(output, `"total_pages": 1`) {
		t.Fatalf("output missing total_pages:\n%s", output)
	}
}

func TestSearchTokenResourceURL_NormalizesToOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		serviceURL string
		want       string
	}{
		{"plain origin", "https://entire.io", "https://entire.io"},
		{"trailing slash", "https://entire.io/", "https://entire.io"},
		{"pathful search URL", "https://entire.io/custom/search", "https://entire.io"},
		{"localhost port", "http://localhost:8787/search", "http://localhost:8787"},
		{"parse fallback", "://not-a-url", "://not-a-url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := searchTokenResourceURL(tt.serviceURL); got != tt.want {
				t.Fatalf("searchTokenResourceURL(%q) = %q, want %q", tt.serviceURL, got, tt.want)
			}
		})
	}
}
