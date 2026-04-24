// Package cli — drift_warning.go surfaces a single "agent hooks need
// updating" warning on every visible user command. See
// docs/superpowers/specs/2026-04-24-stale-hooks-warning-design.md for design.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/spf13/cobra"
)

// shouldSkipDriftWarning returns true when the stale-hooks warning should
// NOT be emitted for cmd. Rules (any triggers skip):
//   - cmd is nil (defensive).
//   - cmd or any ancestor has Hidden=true (internal / machine-invoked commands
//     like `entire hooks`, `entire migrate`, dev helpers).
//   - cmd.Name() is "enable" or "configure" — those run the shared setup
//     flow, which prints the same warning via emitStaleHooksWarning itself,
//     and `enable --force` is the fix so a second warning is noise.
func shouldSkipDriftWarning(cmd *cobra.Command) bool {
	if cmd == nil {
		return true
	}
	switch cmd.Name() {
	case "enable", "configure":
		return true
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Hidden {
			return true
		}
	}
	return false
}

// emitStaleHooksWarning renders the user-facing stale-hooks warning to w.
// Two lines, yellow when w is a TTY, no-op when drifts is empty. Callers
// (the root PersistentPreRun, status.go, setup.go) are responsible for the
// skip rules (hidden commands, non-TTY stderr, dev build, etc.) — this
// renderer is pure.
func emitStaleHooksWarning(w io.Writer, drifts []agent.DriftReport) {
	if len(drifts) == 0 {
		return
	}
	names := make([]string, 0, len(drifts))
	for _, r := range drifts {
		names = append(names, string(r.Agent))
	}
	joined := strings.Join(names, ", ")

	sty := newStatusStyles(w)
	fmt.Fprintln(w, sty.render(sty.yellow, fmt.Sprintf("Action required: agent hooks need updating (%s)", joined)))
	fmt.Fprintln(w, sty.render(sty.yellow, "  Run: entire enable --force"))
}
