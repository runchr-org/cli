package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/trail"
)

func TestPrintTrailDetailsIncludesDiscussionComments(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	metadata := &trail.Metadata{
		TrailID:   trail.ID("abcdef123456"),
		Branch:    "feature/review-visible",
		Base:      "main",
		Title:     "Make review visible",
		Status:    trail.StatusInReview,
		CreatedAt: now,
		UpdatedAt: now,
	}
	discussion := &trail.Discussion{Comments: []trail.Comment{
		{
			ID:        "comment-123",
			Author:    "Marvin",
			CreatedAt: now,
			Body:      "<!-- entire-run:trail-pr-review:run-123 -->\n\n### Marvin Review\n\nFound 1 finding.",
			Resolved:  false,
		},
	}}

	var out bytes.Buffer
	printTrailDetails(&out, metadata, discussion)
	got := out.String()

	for _, want := range []string{"Comments:", "Marvin", "### Marvin Review", "Found 1 finding."} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "entire-run:trail-pr-review") {
		t.Fatalf("output exposed hidden marker:\n%s", got)
	}
}
