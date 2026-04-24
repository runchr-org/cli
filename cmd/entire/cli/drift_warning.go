// Package cli — drift_warning.go surfaces a single "agent hooks need
// updating" warning on every visible user command. See
// docs/superpowers/specs/2026-04-24-stale-hooks-warning-design.md for design.
package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/spf13/cobra"
)

// shouldSkipDriftWarning returns true when the stale-hooks warning should
// NOT be emitted for cmd. Rules (any triggers skip):
//   - cmd is nil (defensive).
//   - cmd is the root command itself (bare `entire`, which prints help or
//     runs the first-time setup flow — neither is the right surface for a
//     warning).
//   - cmd or any ancestor has Hidden=true (internal / machine-invoked commands
//     like `entire hooks`, `entire migrate`, dev helpers).
//   - cmd.Name() is "status" — the status card calls emitStaleHooksWarning
//     inline so the warning renders integrated with the rest of the card.
func shouldSkipDriftWarning(cmd *cobra.Command) bool {
	if cmd == nil {
		return true
	}
	switch cmd.Name() {
	case "entire", "status":
		// "entire" is the root command (bare `entire` prints help or runs
		// first-time setup); "status" renders the warning inline.
		return true
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Hidden {
			return true
		}
	}
	return false
}

// staleHooksWarningLines returns the two yellow-styled warning lines for a
// non-empty drifts list (or nil for an empty one). The caller supplies a
// statusStyles built from its own output writer so that color is decided
// by the real destination writer, not by whatever buffer a helper routes
// through on the way there.
func staleHooksWarningLines(sty statusStyles, drifts []agent.DriftReport) []string {
	if len(drifts) == 0 {
		return nil
	}
	names := make([]string, 0, len(drifts))
	for _, r := range drifts {
		names = append(names, string(r.Agent))
	}
	joined := strings.Join(names, ", ")
	return []string{
		sty.render(sty.yellow, fmt.Sprintf("Action required: agent hooks need updating (%s)", joined)),
		sty.render(sty.yellow, "  Run: entire enable --force"),
	}
}

// emitStaleHooksWarning writes the two-line stale-hooks warning to w.
// Yellow when w is a TTY, no-op when drifts is empty. The root
// PersistentPreRun is the only caller today; the status card uses
// staleHooksWarningLines directly so it can reuse the card's own
// statusStyles and indent each line under its 2-space gutter.
func emitStaleHooksWarning(w io.Writer, drifts []agent.DriftReport) {
	for _, line := range staleHooksWarningLines(newStatusStyles(w), drifts) {
		fmt.Fprintln(w, line)
	}
}

// checkHookDriftForWarning indirects agent.CheckHookDrift so tests can stub
// the drift list. Production callers never reassign it.
var checkHookDriftForWarning = agent.CheckHookDrift

// isTerminalWriterFn indirects interactive.IsTerminalWriter so tests can
// simulate a TTY stderr without shuffling real file descriptors.
var isTerminalWriterFn = interactive.IsTerminalWriter

// inGitRepoFn indirects the "am I inside a git worktree?" check so tests
// can flip it without a real fs layout. Production path delegates to
// paths.WorktreeRoot and treats any error as "not in a repo".
var inGitRepoFn = func(ctx context.Context) bool {
	_, err := paths.WorktreeRoot(ctx)
	return err == nil
}

// driftWarningPreRun is wired as the root command's PersistentPreRun. It
// emits the stale-hooks warning on stderr when:
//   - cmd passes shouldSkipDriftWarning (not hidden, not `status`),
//   - cmd is not `enable`/`configure` being invoked with `--force` (user
//     is already running the remediation the warning would suggest),
//   - stderr is an actual terminal (don't pollute scripted / CI stderr),
//   - checkHookDriftForWarning returns a non-empty list.
//
// All other conditions return silently. Cheap: CheckHookDrift bails
// before touching the agent registry or filesystem when the floor is
// still "0.0.0" (the default).
func driftWarningPreRun(cmd *cobra.Command, _ []string) {
	if shouldSkipDriftWarning(cmd) {
		return
	}
	if forceRemediationInFlight(cmd) {
		return
	}
	// Outside a git worktree there is no valid Entire context yet —
	// agent hook detectors fall back to "." on WorktreeRoot failure,
	// so a non-repo dir that happens to contain a stray .claude/ would
	// otherwise warn and then the command itself would bail with
	// "not a git repository", which is misleading.
	if !inGitRepoFn(cmd.Context()) {
		return
	}
	w := cmd.ErrOrStderr()
	if !isTerminalWriterFn(w) {
		return
	}
	drifts := checkHookDriftForWarning(cmd.Context())
	if len(drifts) == 0 {
		return
	}
	emitStaleHooksWarning(w, drifts)
}

// forceRemediationInFlight reports whether cmd is `entire enable` or
// `entire configure` invoked with `--force=true`. Re-running `entire
// enable --force` is the exact remediation the warning tells users to
// run, so firing the warning during that invocation is noise.
//
// We check the flag's resolved value, not just flag.Changed: wrappers
// that always emit every flag may pass `--force=false` explicitly, which
// Cobra still marks as Changed but obviously isn't the remediation.
func forceRemediationInFlight(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "enable", "configure":
	default:
		return false
	}
	force, err := cmd.Flags().GetBool("force")
	if err != nil {
		return false
	}
	return force
}
