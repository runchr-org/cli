package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/spf13/cobra"
)

func TestShouldSkipDriftWarning(t *testing.T) {
	t.Parallel()

	visible := &cobra.Command{Use: "rewind"}
	hidden := &cobra.Command{Use: "hooks", Hidden: true}
	hiddenChild := &cobra.Command{Use: "claude-code"}
	hidden.AddCommand(hiddenChild)
	enable := &cobra.Command{Use: "enable"}
	configure := &cobra.Command{Use: "configure"}

	cases := []struct {
		name string
		cmd  *cobra.Command
		want bool
	}{
		{"nil", nil, true},
		{"visible", visible, false},
		{"hidden", hidden, true},
		{"hidden-ancestor", hiddenChild, true},
		{"enable", enable, false},
		{"configure", configure, false},
		{"root", &cobra.Command{Use: "entire"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldSkipDriftWarning(tc.cmd); got != tc.want {
				t.Errorf("shouldSkipDriftWarning(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestEmitStaleHooksWarning(t *testing.T) {
	t.Parallel()

	t.Run("empty reports writes nothing", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		emitStaleHooksWarning(&buf, nil)
		if buf.Len() != 0 {
			t.Fatalf("expected no output for empty drifts, got %q", buf.String())
		}
	})

	t.Run("single agent", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		emitStaleHooksWarning(&buf, []agent.DriftReport{{Agent: types.AgentName("claude-code")}})
		got := buf.String()
		if !strings.Contains(got, "Action required: agent hooks need updating (claude-code)") {
			t.Errorf("expected first line, got: %q", got)
		}
		if !strings.Contains(got, "Run: entire enable --force") {
			t.Errorf("expected second line, got: %q", got)
		}
	})

	t.Run("multiple agents comma-separated", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		emitStaleHooksWarning(&buf, []agent.DriftReport{
			{Agent: types.AgentName("claude-code")},
			{Agent: types.AgentName("cursor")},
		})
		got := buf.String()
		if !strings.Contains(got, "(claude-code, cursor)") {
			t.Errorf("expected comma-separated list, got: %q", got)
		}
	})
}

// TestDriftWarningPreRun stubs the drift checker, TTY check, and git-repo
// check so every gate in driftWarningPreRun can be exercised deterministically.
// Not parallel: overrides package-level stubs.
func TestDriftWarningPreRun(t *testing.T) {
	origCheck := checkHookDriftForWarning
	origTTY := isTerminalWriterFn
	origRepo := inGitRepoFn
	t.Cleanup(func() {
		checkHookDriftForWarning = origCheck
		isTerminalWriterFn = origTTY
		inGitRepoFn = origRepo
	})

	checkHookDriftForWarning = func(context.Context) []agent.DriftReport {
		return []agent.DriftReport{{Agent: types.AgentName("claude-code")}}
	}
	// Default to "not a TTY" so skip-path cases don't need to override.
	isTerminalWriterFn = func(io.Writer) bool { return false }
	// Default to "inside a git repo" — most cases exercise behavior there.
	inGitRepoFn = func(context.Context) bool { return true }

	run := func(c *cobra.Command) string {
		var buf bytes.Buffer
		c.SetErr(&buf)
		c.SetContext(t.Context())
		driftWarningPreRun(c, nil)
		return buf.String()
	}
	// withTTY flips isTerminalWriterFn to true for the calling subtest only.
	withTTY := func(t *testing.T) {
		t.Helper()
		isTerminalWriterFn = func(io.Writer) bool { return true }
		t.Cleanup(func() {
			isTerminalWriterFn = func(io.Writer) bool { return false }
		})
	}

	// Skip paths — all expect no output.
	skipCases := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"hidden command", &cobra.Command{Use: "hooks", Hidden: true}},
		{"visible non-TTY stderr", &cobra.Command{Use: "rewind"}},
	}
	for _, tc := range skipCases {
		t.Run(tc.name, func(t *testing.T) {
			if out := run(tc.cmd); out != "" {
				t.Errorf("expected no output for %s, got %q", tc.name, out)
			}
		})
	}

	// Positive path — visible command with simulated TTY stderr emits both lines.
	t.Run("visible TTY stderr emits warning", func(t *testing.T) {
		withTTY(t)
		out := run(&cobra.Command{Use: "rewind"})
		want := fmt.Sprintf(staleHooksHeader, "claude-code")
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got %q", want, out)
		}
		if !strings.Contains(out, strings.TrimSpace(staleHooksFix)) {
			t.Errorf("expected %q in output, got %q", staleHooksFix, out)
		}
	})

	// enable/configure --force must skip even with a TTY stderr: the user is
	// already running the exact remediation the warning would suggest.
	forceSkipCases := []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"enable --force", func() *cobra.Command {
			c := &cobra.Command{Use: "enable"}
			c.Flags().BoolP("force", "f", false, "")
			if err := c.Flags().Set("force", "true"); err != nil {
				t.Fatalf("set force: %v", err)
			}
			return c
		}},
		{"configure --force", func() *cobra.Command {
			c := &cobra.Command{Use: "configure"}
			c.Flags().BoolP("force", "f", false, "")
			if err := c.Flags().Set("force", "true"); err != nil {
				t.Fatalf("set force: %v", err)
			}
			return c
		}},
	}
	for _, tc := range forceSkipCases {
		t.Run(tc.name, func(t *testing.T) {
			withTTY(t)
			if out := run(tc.cmd()); out != "" {
				t.Errorf("expected no output for %s, got %q", tc.name, out)
			}
		})
	}

	// enable WITHOUT --force still gets the warning (flag present but unset).
	t.Run("enable without --force emits warning", func(t *testing.T) {
		withTTY(t)
		c := &cobra.Command{Use: "enable"}
		c.Flags().BoolP("force", "f", false, "")
		if out := run(c); !strings.Contains(out, "Action required") {
			t.Errorf("expected warning for enable without --force, got %q", out)
		}
	})

	// Explicit --force=false from a wrapper should NOT suppress the warning;
	// flag.Changed is true but the remediation is not actually in flight.
	t.Run("enable --force=false emits warning", func(t *testing.T) {
		withTTY(t)
		c := &cobra.Command{Use: "enable"}
		c.Flags().BoolP("force", "f", false, "")
		if err := c.Flags().Set("force", "false"); err != nil {
			t.Fatalf("set force=false: %v", err)
		}
		if out := run(c); !strings.Contains(out, "Action required") {
			t.Errorf("expected warning for enable --force=false, got %q", out)
		}
	})

	// Outside a git worktree the pre-run must skip entirely — otherwise
	// a non-repo dir with a stray .claude/ could warn and then the
	// command would bail with "not a git repository".
	t.Run("outside git repo skips", func(t *testing.T) {
		withTTY(t)
		inGitRepoFn = func(context.Context) bool { return false }
		t.Cleanup(func() {
			inGitRepoFn = func(context.Context) bool { return true }
		})
		if out := run(&cobra.Command{Use: "rewind"}); out != "" {
			t.Errorf("expected no output outside git repo, got %q", out)
		}
	})
}
