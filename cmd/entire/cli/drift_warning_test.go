package cli

import (
	"testing"

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
