package skilldiscovery_test

import (
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/skilldiscovery"
)

// TestInstallHintsFor_CodexUsesCodexInvocationSyntax pins the contract that
// codex install hints use codex's actual invocation syntax (@plugin-name or
// $skill-name), not claude's /<plugin>:<command> form. The codex picker can
// never discover a `/plugin:cmd` entry, so a hint with that shape in
// ProvidesAny is permanently unsuppressable.
func TestInstallHintsFor_CodexUsesCodexInvocationSyntax(t *testing.T) {
	t.Parallel()
	hints := skilldiscovery.ActiveInstallHintsFor("codex", nil)
	if len(hints) == 0 {
		// It's valid to have no hints (we may drop them entirely in the
		// future). If the entry exists, it must use codex syntax.
		return
	}
	for _, h := range hints {
		for _, providesAny := range h.ProvidesAny {
			// Reject claude-plugin syntax (`/<plugin>:<command>`) specifically.
			// Bare `/<name>` (e.g. `/review`) is a legitimate codex built-in
			// slash-command, so we don't reject it — only the colon-namespaced
			// form is the bug we're guarding against.
			if strings.HasPrefix(providesAny, "/") && strings.Contains(providesAny, ":") {
				t.Errorf("codex install hint ProvidesAny %q uses claude-plugin syntax (/plugin:command); codex plugins are invoked as @plugin-name or $skill-name", providesAny)
			}
		}
		if strings.Contains(h.Message, "codex plugins add") {
			t.Errorf("codex install hint Message references `codex plugins add` (not a real codex subcommand); use `codex plugin marketplace add <url>` instead. Message: %q", h.Message)
		}
	}
}

func TestCuratedBuiltinsFor_KnownAgents(t *testing.T) {
	t.Parallel()
	claude := skilldiscovery.CuratedBuiltinsFor("claude-code")
	if len(claude) != 3 {
		t.Fatalf("claude-code built-ins: got %d entries, want 3", len(claude))
	}
	codex := skilldiscovery.CuratedBuiltinsFor("codex")
	if len(codex) != 1 || codex[0].Name != "/review" {
		t.Errorf("codex built-ins: got %+v, want 1x /review", codex)
	}
	gemini := skilldiscovery.CuratedBuiltinsFor("gemini")
	if len(gemini) != 0 {
		t.Errorf("gemini built-ins: got %d, want 0", len(gemini))
	}
}

func TestCuratedBuiltinsFor_UnknownAgentReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := skilldiscovery.CuratedBuiltinsFor("nonexistent"); len(got) != 0 {
		t.Errorf("unknown agent: got %d entries, want 0", len(got))
	}
}

func TestActiveInstallHintsFor_SuppressesWhenProvidesAnyDiscovered(t *testing.T) {
	t.Parallel()
	discovered := map[string]struct{}{"/pr-review-toolkit:review-pr": {}}
	hints := skilldiscovery.ActiveInstallHintsFor("claude-code", discovered)
	for _, h := range hints {
		for _, name := range h.ProvidesAny {
			if name == "/pr-review-toolkit:review-pr" {
				t.Errorf("pr-review-toolkit hint should have been suppressed, got %+v", h)
			}
		}
	}
}

func TestActiveInstallHintsFor_ShowsAllWhenNothingDiscovered(t *testing.T) {
	t.Parallel()
	hints := skilldiscovery.ActiveInstallHintsFor("claude-code", nil)
	if len(hints) == 0 {
		t.Fatal("expected at least one hint when discovery set is empty")
	}
}

func TestActiveInstallHintsFor_GeminiAlwaysShownRegardlessOfDiscovery(t *testing.T) {
	t.Parallel()
	hints := skilldiscovery.ActiveInstallHintsFor("gemini", map[string]struct{}{"/anything": {}})
	if len(hints) == 0 {
		t.Error("gemini hint with nil ProvidesAny should always show")
	}
}

func TestIsEligible_IncludesAgentWithOnlyInstallHint(t *testing.T) {
	t.Parallel()
	if !skilldiscovery.IsEligible("gemini") {
		t.Error("gemini should be eligible via install hint alone")
	}
	if !skilldiscovery.IsEligible("claude-code") {
		t.Error("claude-code should be eligible via built-ins")
	}
	if skilldiscovery.IsEligible("nonexistent") {
		t.Error("unknown agent should not be eligible")
	}
}
