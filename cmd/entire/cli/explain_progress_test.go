package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// bytes.Buffer is not a *os.File, so interactive.IsTerminalWriter returns
// false and the writer renders in non-TTY mode (one line per event,
// no ANSI escape sequences for cursor control). These tests therefore
// exercise the line-per-event path, which is the only path tractable
// without spawning a real pty.

func TestExplainProgressWriter_StartPhase_NonTTY(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := newExplainProgressWriter(&buf)

	pw.StartPhase("Fetching from origin")

	require.Equal(t, "→ Fetching from origin...\n", buf.String())
}

func TestExplainProgressWriter_FinishPhase_OK_NonTTY(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := newExplainProgressWriter(&buf)

	pw.StartPhase("Fetching from origin")
	pw.FinishPhase("Fetching from origin", true, "1.6s")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	require.Equal(t, "→ Fetching from origin...", lines[0])
	require.Equal(t, "✓ Fetching from origin (1.6s)", lines[1])
}

func TestExplainProgressWriter_FinishPhase_Fail_NonTTY(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := newExplainProgressWriter(&buf)

	pw.StartPhase("Fetching from checkpoint_remote")
	pw.FinishPhase("Fetching from checkpoint_remote", false, "not configured")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	require.Equal(t, "✗ Fetching from checkpoint_remote: not configured", lines[1])
}

func TestExplainProgressWriter_FinishPhase_OK_NoDetail(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := newExplainProgressWriter(&buf)

	pw.FinishPhase("Loaded local checkpoints", true, "")

	require.Equal(t, "✓ Loaded local checkpoints\n", buf.String())
}

func TestExplainProgressWriter_UpdateSublabel_NonTTY(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := newExplainProgressWriter(&buf)

	pw.UpdateSublabel("Loading checkpoint abc123", "metadata")
	pw.UpdateSublabel("Loading checkpoint abc123", "content")
	pw.UpdateSublabel("Loading checkpoint abc123", "associated commits")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	require.Equal(t, "→ Loading checkpoint abc123 — metadata...", lines[0])
	require.Equal(t, "→ Loading checkpoint abc123 — content...", lines[1])
	require.Equal(t, "→ Loading checkpoint abc123 — associated commits...", lines[2])
}

func TestExplainProgressWriter_UpdateSublabel_DedupesIdentical(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	pw := newExplainProgressWriter(&buf)

	pw.UpdateSublabel("Loading checkpoint abc123", "metadata")
	pw.UpdateSublabel("Loading checkpoint abc123", "metadata")

	// Second identical call should not emit another line.
	require.Equal(t, "→ Loading checkpoint abc123 — metadata...\n", buf.String())
}

func TestExplainProgressWriter_AccessibleMode(t *testing.T) {
	// Not parallel — t.Setenv mutates process env.
	t.Setenv("ACCESSIBLE", "1")
	var buf bytes.Buffer
	pw := newExplainProgressWriter(&buf)

	pw.StartPhase("Fetching from origin")
	pw.FinishPhase("Fetching from origin", true, "1.6s")
	pw.FinishPhase("Fetching from checkpoint_remote", false, "not configured")

	output := buf.String()
	require.NotContains(t, output, "→", "ACCESSIBLE mode should use ASCII arrow")
	require.NotContains(t, output, "✓", "ACCESSIBLE mode should use ASCII ok glyph")
	require.NotContains(t, output, "✗", "ACCESSIBLE mode should use ASCII fail glyph")
	require.Contains(t, output, "->")
	require.Contains(t, output, "[ok]")
	require.Contains(t, output, "[fail]")
}

func TestFormatPhaseDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		d    time.Duration
		want string
	}{
		{120 * time.Millisecond, "120ms"},
		{999 * time.Millisecond, "999ms"},
		{1 * time.Second, "1.0s"},
		{1600 * time.Millisecond, "1.6s"},
		{12345 * time.Millisecond, "12.3s"},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, formatPhaseDuration(tc.d), "d=%v", tc.d)
	}
}
