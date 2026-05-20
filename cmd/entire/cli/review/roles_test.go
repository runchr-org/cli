package review_test

import (
	"reflect"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/review"
	"github.com/entireio/cli/cmd/entire/cli/settings"
)

func TestNormalizeRoles_DefaultsEmptyToReviewer(t *testing.T) {
	t.Parallel()
	in := map[string]settings.ReviewConfig{
		"claude-code": {Skills: []string{"/review"}},
	}
	out := review.NormalizeRoles(in)
	if out["claude-code"].Role != settings.RoleReviewer {
		t.Errorf("expected RoleReviewer, got %q", out["claude-code"].Role)
	}
}

func TestNormalizeRoles_AtMostOneFixer(t *testing.T) {
	t.Parallel()
	in := map[string]settings.ReviewConfig{
		"claude-code": {Role: settings.RoleFixer},
		"codex":       {Role: settings.RoleFixer},
		"gemini":      {Role: settings.RoleBoth},
	}
	out := review.NormalizeRoles(in)
	fixers := 0
	for _, c := range out {
		if c.Role.IsFixer() {
			fixers++
		}
	}
	if fixers != 1 {
		t.Errorf("expected exactly 1 fixer, got %d", fixers)
	}
	if !out["claude-code"].Role.IsFixer() {
		t.Errorf("expected claude-code (alphabetical first) to keep fixer role, got %+v", out)
	}
}

func TestNormalizeRoles_EmptyInputReturnsFreshEmptyMap(t *testing.T) {
	t.Parallel()
	in := map[string]settings.ReviewConfig{}
	out := review.NormalizeRoles(in)
	if out == nil {
		t.Fatal("expected non-nil empty map, got nil")
	}
	// Mutating `out` must not mutate `in`.
	out["x"] = settings.ReviewConfig{Role: settings.RoleReviewer}
	if _, leaked := in["x"]; leaked {
		t.Errorf("NormalizeRoles returned the input map; mutations leaked")
	}
}

func TestNormalizeRoles_SkipPreserved(t *testing.T) {
	t.Parallel()
	in := map[string]settings.ReviewConfig{"gemini": {Role: settings.RoleSkip}}
	out := review.NormalizeRoles(in)
	if out["gemini"].Role != settings.RoleSkip {
		t.Errorf("Skip role should be preserved, got %q", out["gemini"].Role)
	}
}

func TestReviewersOf_FiltersByRole(t *testing.T) {
	t.Parallel()
	s := &settings.EntireSettings{
		Review: map[string]settings.ReviewConfig{
			"claude-code": {Role: settings.RoleReviewer},
			"codex":       {Role: settings.RoleFixer},
			"gemini":      {Role: settings.RoleBoth},
		},
	}
	got := review.ReviewersOf(s)
	want := []string{"claude-code", "gemini"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReviewersOf = %v, want %v", got, want)
	}
}

func TestFixerOf_AlphabeticalWinnerWhenMultiple(t *testing.T) {
	t.Parallel()
	s := &settings.EntireSettings{
		Review: map[string]settings.ReviewConfig{
			"codex":  {Role: settings.RoleFixer},
			"gemini": {Role: settings.RoleBoth},
		},
	}
	if got := review.FixerOf(s); got != "codex" {
		t.Errorf("FixerOf = %q, want codex (alphabetical first)", got)
	}
}

func TestFixerOf_EmptyWhenNoFixer(t *testing.T) {
	t.Parallel()
	s := &settings.EntireSettings{
		Review: map[string]settings.ReviewConfig{
			"claude-code": {Role: settings.RoleReviewer},
		},
	}
	if got := review.FixerOf(s); got != "" {
		t.Errorf("FixerOf = %q, want empty", got)
	}
}
