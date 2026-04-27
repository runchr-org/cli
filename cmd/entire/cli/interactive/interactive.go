// Package interactive provides TTY-related helpers shared between the cli
// and strategy packages without inducing an import cycle (strategy cannot
// import cli).
package interactive

import (
	"io"
	"os"

	"golang.org/x/term"
)

// CanPromptInteractively reports whether interactive confirmation prompts
// (huh forms, yes/no questions, etc.) can be shown. Returns false in CI,
// agent subprocesses that inherit a TTY but can't respond to prompts,
// and other environments without a controlling TTY.
//
// When ENTIRE_TEST_TTY is set (to any value, including empty) it is treated
// as a test override and the result is determined solely by whether the
// value equals "1":
//   - ENTIRE_TEST_TTY=1 forces interactive mode on
//   - any other value (including empty) forces interactive mode off
//   - ENTIRE_TEST_TTY unset falls through to agent-env guards and
//     /dev/tty probing. In that case the return value depends on the
//     actual environment, so tests that need a specific answer should set
//     ENTIRE_TEST_TTY explicitly rather than assume a non-interactive host.
func CanPromptInteractively() bool {
	if v := os.Getenv("ENTIRE_TEST_TTY"); v != "" {
		return v == "1"
	}

	// Agent subprocesses may inherit the user's TTY but can't respond to
	// interactive prompts. Treat them as non-TTY.
	//   - GEMINI_CLI=1: Gemini CLI shell tool (https://geminicli.com/docs/tools/shell/)
	//   - COPILOT_CLI=1: Copilot CLI hook subprocesses (v0.0.421+)
	//   - PI_CODING_AGENT=true: Pi Coding Agent shell tool
	//   - GIT_TERMINAL_PROMPT=0: caller (CI, Factory AI Droid, etc.) asked
	//     git to stop prompting; respect it from git-hook context too.
	if os.Getenv("GEMINI_CLI") != "" ||
		os.Getenv("COPILOT_CLI") != "" ||
		os.Getenv("PI_CODING_AGENT") != "" ||
		os.Getenv("GIT_TERMINAL_PROMPT") == "0" {
		return false
	}

	// CI=<non-empty> is the de-facto CI-provider convention (GitHub Actions,
	// CircleCI, GitLab, Travis, Buildkite). Self-hosted runners expose /dev/tty,
	// so the probe below isn't enough — an interactive prompt on CI hangs.
	// CI=false is the `is-ci` escape hatch for developers who need to override
	// an inherited value.
	if v := os.Getenv("CI"); v != "" && v != "false" {
		return false
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = tty.Close()
	return true
}

// IsTerminalWriter reports whether w is an *os.File backed by a terminal.
// Use for deciding on color, pager, progress bars, or other writer-scoped
// TTY formatting. For "can I prompt the user?" use CanPromptInteractively.
func IsTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd())) //nolint:gosec // G115: uintptr->int is safe for fd
}
