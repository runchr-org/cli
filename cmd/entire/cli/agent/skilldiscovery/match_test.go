package skilldiscovery_test

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/skilldiscovery"
)

func TestMatches_NameKeywords(t *testing.T) {
	t.Parallel()
	cases := []string{"/review", "/security-audit", "/inspect-pr", "/critique", "/assess"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !skilldiscovery.Matches(name, "") {
				t.Errorf("Matches(%q, \"\") = false, want true", name)
			}
		})
	}
}

func TestMatches_DescriptionKeywords(t *testing.T) {
	t.Parallel()
	if !skilldiscovery.Matches("/unrelated", "Find silent failures during code review") {
		t.Error("description containing 'review' should match")
	}
	if !skilldiscovery.Matches("/unrelated", "Audit test coverage on changed files") {
		t.Error("description containing 'audit' should match")
	}
}

func TestMatches_CaseInsensitive(t *testing.T) {
	t.Parallel()
	if !skilldiscovery.Matches("/REVIEW", "") {
		t.Error("uppercase name keyword should match")
	}
	if !skilldiscovery.Matches("/run", "Deeply INSPECT the diff") {
		t.Error("uppercase description keyword should match")
	}
}

func TestMatches_NoMatchWhenNeitherContains(t *testing.T) {
	t.Parallel()
	if skilldiscovery.Matches("/format", "Reformat code") {
		t.Error("format/reformat should not match review keywords")
	}
	if skilldiscovery.Matches("/lint-fix", "Auto-fix lint errors") {
		t.Error("lint is no longer in the keyword set — see spec §Keyword Set")
	}
}
