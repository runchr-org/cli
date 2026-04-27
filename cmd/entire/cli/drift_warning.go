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
//   - cmd is the root command itself (bare `entire` prints help or runs
//     first-time setup; neither is the right surface for the warning).
//   - cmd or any ancestor has Hidden=true (internal / machine-invoked
//     commands: `entire hooks`, `entire migrate`, dev helpers).
func shouldSkipDriftWarning(cmd *cobra.Command) bool {
	if cmd == nil {
		return true
	}
	if cmd.Name() == "entire" {
		return true
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Hidden {
			return true
		}
	}
	return false
}

// staleHooksHeader is the first warning line; %s is the comma-joined
// drifted-agent list. staleHooksFix is the second line. Both are
// referenced verbatim from tests.
const (
	staleHooksHeader = "Action required: agent hooks need updating (%s)"
	staleHooksFix    = "  Run: entire enable --force"
)

// emitStaleHooksWarning writes the two-line stale-hooks warning to w.
// Yellow when w is a TTY, no-op when drifts is empty. The root
// PersistentPreRun is the sole caller — every visible command goes
// through it, including `entire status`.
func emitStaleHooksWarning(w io.Writer, drifts []agent.DriftReport) {
	if len(drifts) == 0 {
		return
	}
	names := make([]string, 0, len(drifts))
	for _, r := range drifts {
		names = append(names, string(r.Agent))
	}

	sty := newStatusStyles(w)
	fmt.Fprintln(w, sty.render(sty.yellow, fmt.Sprintf(staleHooksHeader, strings.Join(names, ", "))))
	fmt.Fprintln(w, sty.render(sty.yellow, staleHooksFix))
}

// checkHookDriftForWarning indirects agent.CheckHookDrift so tests can stub
// the drift list. Production callers never reassign it.
var checkHookDriftForWarning = agent.CheckHookDrift

// isTerminalWriterFn indirects interactive.IsTerminalWriter so tests can
// simulate a TTY stderr without shuffling real file descriptors.
var isTerminalWriterFn = interactive.IsTerminalWriter

// inGitRepoFn indirects the git-repo check so tests can stub it.
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
