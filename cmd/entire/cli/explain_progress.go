package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
)

// explainProgressWriter renders phase events for the explain pipeline:
// status flips per attempt (→ label / ✓ label / ✗ label) and an
// in-place sublabel ticker for the data-loading sub-steps. Shares visual
// conventions with the streaming summary progress UX (statusStyles glyphs,
// TTY/non-TTY/ACCESSIBLE fallbacks).
type explainProgressWriter struct {
	w        io.Writer
	inplace  bool
	arrow    string
	check    string
	cross    string
	lastLine string
}

func newExplainProgressWriter(w io.Writer) *explainProgressWriter {
	styles := newStatusStyles(w)
	accessible := IsAccessibleMode()
	inplace := interactive.IsTerminalWriter(w) && !accessible
	arrow, check, cross := "→", "✓", "✗"
	if accessible {
		arrow, check, cross = "->", "[ok]", "[fail]"
	}
	return &explainProgressWriter{
		w:       w,
		inplace: inplace,
		arrow:   styles.render(styles.cyan, arrow),
		check:   styles.render(styles.green, check),
		cross:   styles.render(styles.red, cross),
	}
}

// StartPhase shows "→ label..." for an attempt that's in progress.
// On TTY it can be overwritten in place; on non-TTY it appears as its own line.
func (p *explainProgressWriter) StartPhase(label string) {
	p.updateLine(fmt.Sprintf("%s %s...", p.arrow, label))
}

// FinishPhase replaces the most recent in-flight line with the phase outcome.
// detail is appended after a separator ("✓ label (1.6s)" on ok,
// "✗ label: not configured" on fail). detail may be empty.
func (p *explainProgressWriter) FinishPhase(label string, ok bool, detail string) {
	glyph := p.check
	line := glyph + " " + label
	if !ok {
		glyph = p.cross
		line = glyph + " " + label
		if detail != "" {
			line += ": " + detail
		}
	} else if detail != "" {
		line += " (" + detail + ")"
	}
	p.printLine(line)
}

// UpdateSublabel ticks a single in-place line for sub-step transitions
// (e.g. metadata → content → commits during the data-loading pipeline).
// On non-TTY it emits one line per sub-step.
func (p *explainProgressWriter) UpdateSublabel(prefix, sub string) {
	p.updateLine(fmt.Sprintf("%s %s — %s...", p.arrow, prefix, sub))
}

// updateLine writes line in-place on TTY; on non-TTY appends a new line.
// A subsequent updateLine or printLine call clears the line.
func (p *explainProgressWriter) updateLine(line string) {
	if p.lastLine == line {
		return
	}
	if p.inplace {
		fmt.Fprintf(p.w, "\r\033[2K%s", line)
	} else {
		fmt.Fprintln(p.w, line)
	}
	p.lastLine = line
}

// printLine emits line as a finalized line (with trailing newline),
// clearing any in-flight in-place line first.
func (p *explainProgressWriter) printLine(line string) {
	if p.inplace && p.lastLine != "" {
		fmt.Fprint(p.w, "\r\033[2K")
	}
	fmt.Fprintln(p.w, line)
	p.lastLine = ""
}

// formatPhaseDuration renders a duration for inline phase details
// ("1.6s", "120ms"). Mirrors the style used by the streaming summary UX.
func formatPhaseDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// newPhaseProgressHooks adapts an explainProgressWriter to
// checkpoint.AttemptHooks so phase events fire as each fetch/read attempt
// runs. Successful attempts render as "✓ label (duration)"; failed attempts
// render as "✗ label: <first stderr line>" so the user sees the immediate
// reason rather than just "failed".
func newPhaseProgressHooks(pw *explainProgressWriter) checkpoint.AttemptHooks {
	if pw == nil {
		return checkpoint.AttemptHooks{}
	}
	return checkpoint.AttemptHooks{
		OnStart: func(label string) {
			pw.StartPhase(label)
		},
		OnFinish: func(label string, duration time.Duration, err error) {
			if err == nil {
				pw.FinishPhase(label, true, formatPhaseDuration(duration))
				return
			}
			pw.FinishPhase(label, false, firstStderrLine(err))
		},
	}
}

// firstStderrLine returns a short single-line representation of err
// suitable for an inline "✗ <label>: <line>" diagnostic. Prefers the first
// non-blank line from a captured FetchAttemptError stderr; falls back to
// the first line of err.Error().
func firstStderrLine(err error) string {
	var fae *FetchAttemptError
	if errors.As(err, &fae) && fae.Stderr != "" {
		for _, line := range strings.Split(fae.Stderr, "\n") {
			t := strings.TrimSpace(line)
			if t != "" {
				return t
			}
		}
	}
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i > 0 {
		msg = msg[:i]
	}
	return msg
}
