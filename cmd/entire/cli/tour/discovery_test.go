package tour

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestDiscover_StripsHiddenAndDeprecated(t *testing.T) {
	root := &cobra.Command{Use: "entire", Short: "root"}
	root.AddCommand(&cobra.Command{Use: "enable", Short: "enable entire"})
	root.AddCommand(&cobra.Command{Use: "internal-thing", Short: "private", Hidden: true})
	root.AddCommand(&cobra.Command{Use: "old", Short: "old", Deprecated: "use new"})
	root.AddCommand(&cobra.Command{Use: "completion", Short: "shell completion"})

	surface := Discover(root)

	got := childNames(surface.Root)
	want := []string{"enable"}
	if !equalStrings(got, want) {
		t.Fatalf("Discover() child names = %v, want %v", got, want)
	}
}

func TestDiscover_RecursesIntoSubcommands(t *testing.T) {
	root := &cobra.Command{Use: "entire"}
	checkpoint := &cobra.Command{Use: "checkpoint", Short: "checkpoint group"}
	checkpoint.AddCommand(&cobra.Command{Use: "list", Short: "list checkpoints"})
	checkpoint.AddCommand(&cobra.Command{Use: "search", Short: "search checkpoints"})
	root.AddCommand(checkpoint)

	surface := Discover(root)

	cp := findChildOrFail(t, surface.Root, "checkpoint")
	got := childNames(*cp)
	want := []string{"list", "search"}
	if !equalStrings(got, want) {
		t.Fatalf("checkpoint child names = %v, want %v", got, want)
	}
	if cp.Path != "entire checkpoint" {
		t.Errorf("checkpoint.Path = %q, want %q", cp.Path, "entire checkpoint")
	}
}

func TestTrimDescription_KeepsFirstParagraph(t *testing.T) {
	long := "First paragraph that explains the command.\n\nSecond paragraph with examples and details that should be omitted from the tour."
	got := trimDescription(long)
	want := "First paragraph that explains the command."
	if got != want {
		t.Errorf("trimDescription = %q, want %q", got, want)
	}
}

func childNames(node CommandNode) []string {
	out := make([]string, 0, len(node.Subcommands))
	for _, sub := range node.Subcommands {
		out = append(out, sub.Name)
	}
	return out
}

func findChildOrFail(t *testing.T, node CommandNode, name string) *CommandNode {
	t.Helper()
	for i := range node.Subcommands {
		if node.Subcommands[i].Name == name {
			return &node.Subcommands[i]
		}
	}
	t.Fatalf("missing subcommand %q under %q", name, node.Name)
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
