package cli

import (
	"bytes"
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
		{"enable", enable, true},
		{"configure", configure, true},
	}
	for _, tc := range cases {
		tc := tc
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
