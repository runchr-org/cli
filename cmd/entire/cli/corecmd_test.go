package cli

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTableStylesDecorate(t *testing.T) {
	t.Parallel()

	plain := lipgloss.NewStyle()
	enabled := tableStyles{enabled: true, primary: plain, cell: plain, header: plain}
	disabled := tableStyles{}

	const url = "entire://aws-us-east-2.entire.io/gh/entirehq/entire.io"

	t.Run("url gets an OSC8 hyperlink when enabled", func(t *testing.T) {
		t.Parallel()
		got := enabled.decorate(enabled.cell, url)
		if !strings.Contains(got, "\x1b]8;;"+url) {
			t.Errorf("expected OSC8 hyperlink targeting %q, got %q", url, got)
		}
	})

	t.Run("non-url is not hyperlinked", func(t *testing.T) {
		t.Parallel()
		if got := enabled.decorate(enabled.cell, "yes"); strings.Contains(got, "\x1b]8;;") {
			t.Errorf("non-url should not be hyperlinked, got %q", got)
		}
	})

	t.Run("disabled passes through unchanged", func(t *testing.T) {
		t.Parallel()
		if got := disabled.decorate(disabled.cell, url); got != url {
			t.Errorf("disabled decorate = %q, want plain %q", got, url)
		}
	})
}
