package redact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"
)

// progressWriter writes user-facing progress messages for OPF operations.
// It adapts output format based on whether the destination is a TTY and
// whether accessibility mode is enabled.
type progressWriter struct {
	w          io.Writer
	tty        bool
	accessible bool
}

func newProgressWriter(w io.Writer, tty, accessible bool) *progressWriter {
	return &progressWriter{w: w, tty: tty, accessible: accessible}
}

func (p *progressWriter) Start(detail string) {
	switch {
	case p.accessible || !p.tty:
		fmt.Fprintf(p.w, "[entire] redaction: openai privacy filter %s (this may take a few seconds)\n", detail)
	default:
		fmt.Fprintf(p.w, "→ OpenAI Privacy Filter: %s…\n", detail)
	}
}

func (p *progressWriter) Finish(elapsed time.Duration) {
	secs := elapsed.Seconds()
	switch {
	case p.accessible || !p.tty:
		fmt.Fprintf(p.w, "[entire] redaction: openai privacy filter completed in %.1fs\n", secs)
	default:
		fmt.Fprintf(p.w, "✓ OpenAI Privacy Filter: done (%.1fs)\n", secs)
	}
}

// Typed errors for failure-message routing.
var errOPFNotFound = errors.New("not found on PATH")

// opfTimeoutError is a typed error carrying the timeout duration in seconds.
// The errname linter requires error types to be named xxxError.
type opfTimeoutError int

func opfErrTimeout(seconds int) error { return opfTimeoutError(seconds) }
func (e opfTimeoutError) Error() string {
	return fmt.Sprintf("exceeded %ds timeout", int(e))
}

// formatOPFFailure builds a user-facing failure message for an OPF runtime
// error. The message is routed through stderr by handleOPFFailure; it always
// ends with the fallback receipt so users know redaction continued without OPF.
//
// Matching covers both the synthetic sentinels used in unit tests and the
// real errors that the shell-out runtime produces in production
// (exec.ErrNotFound, os.ErrNotExist, context.DeadlineExceeded).
func formatOPFFailure(err error, command string) string {
	var b strings.Builder
	b.WriteString("× OpenAI Privacy Filter: ")
	switch {
	case isOPFNotFoundErr(err):
		fmt.Fprintf(&b, "'%s' not found on PATH. Install with 'pip install opf' (see https://github.com/openai/privacy-filter) or set 'redaction.openai_privacy_filter.command' in .entire/settings.json. ", command)
	case isOPFTimeoutErr(err):
		fmt.Fprintf(&b, "%s. Consider raising 'redaction.openai_privacy_filter.timeout_seconds' or disabling the filter. ", err.Error())
	default:
		fmt.Fprintf(&b, "%s. ", err.Error())
	}
	b.WriteString("Falling back to default redaction layers.")
	return b.String()
}

// isOPFNotFoundErr reports whether err signals that the configured OPF
// binary couldn't be located on PATH or at its absolute path. Matches:
//   - errOPFNotFound (synthetic sentinel used in unit tests)
//   - exec.ErrNotFound (produced by exec.LookPath when a bare name isn't on PATH)
//   - os.ErrNotExist (produced when an absolute path doesn't resolve to a file)
func isOPFNotFoundErr(err error) bool {
	if errors.Is(err, errOPFNotFound) {
		return true
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return false
}

// isOPFTimeoutErr reports whether err signals the OPF call exceeded its
// timeout. Matches both opfTimeoutError (synthetic sentinel) and a wrapped
// context.DeadlineExceeded (produced by shellout.go on real timeouts).
func isOPFTimeoutErr(err error) bool {
	var t opfTimeoutError
	if errors.As(err, &t) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// isTTYWriter reports whether w is a file descriptor connected to a terminal.
// Uses golang.org/x/term for a portable TTY probe. Returns false for any
// non-*os.File writer (e.g. bytes.Buffer in tests).
func isTTYWriter(w io.Writer) bool {
	if w == nil {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd())) //nolint:gosec // fd is always a small non-negative integer
}

// accessibleMode reports whether ACCESSIBLE env var is set, matching the
// existing CLI convention. Local helper so the redact package does not have
// to import cmd/entire/cli/interactive.
func accessibleMode() bool { return os.Getenv("ACCESSIBLE") != "" }

// opfStderr is the destination for user-facing OPF failure and progress
// messages. Production code uses os.Stderr; tests override this to capture output.
var opfStderr io.Writer = os.Stderr
