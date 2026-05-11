package learn

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/spf13/cobra"
)

// TestGenerate_RegenerateBypassesResolveState pins the bypass behavior
// the changelog/release flow relies on: when Options.Regenerate is true
// Generate must skip ResolveState entirely and route directly into the
// agent-driven path. The pipeline runs in CI checkouts that have no
// .entire/settings.json, so a stray LoadSettings or ListInstalledAgents
// call would (a) waste work and (b) route the request through StageSetup,
// which produces the 4-line stub the regen validator rejects.
//
// We assert via spies on the two Options callbacks. The Generate call
// itself is expected to fail (no agent on PATH in the test environment,
// or the cancelled context aborts before any network), and we don't
// assert on the error — only on the spies — so the test stays
// hermetic regardless of which agents happen to be installed.
func TestGenerate_RegenerateBypassesResolveState(t *testing.T) {
	t.Parallel()
	var settingsCalls, agentsCalls atomic.Int32
	opts := Options{
		LoadSettings: func(_ context.Context) (bool, bool, error) {
			settingsCalls.Add(1)
			return false, false, nil
		},
		ListInstalledAgents: func(_ context.Context) []types.AgentName {
			agentsCalls.Add(1)
			return nil
		},
		Regenerate: true,
	}
	root := &cobra.Command{Use: "entire"}

	// Pre-cancel the context so that if ResolveTextGenerator does happen
	// to find a real agent on the test machine, the GenerateText call
	// fails fast without hitting the network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := Generate(ctx, root, opts)
	// We expect failure (no agent on PATH or cancelled ctx aborts the
	// agent call); the assertions below are on the bypass behavior, not
	// the error. Result+err are bound so errcheck and unparam don't
	// complain about discarded returns.
	_ = result
	_ = err

	if got := settingsCalls.Load(); got != 0 {
		t.Errorf("LoadSettings called %d time(s); --regenerate must bypass settings load", got)
	}
	if got := agentsCalls.Load(); got != 0 {
		t.Errorf("ListInstalledAgents called %d time(s); --regenerate must bypass agent enumeration", got)
	}
}

// TestGenerate_DefaultPathConsultsResolveState asserts the inverse:
// without Regenerate, Generate must call LoadSettings to decide the
// routing stage. This pins the contract on both sides so a future
// refactor that accidentally bypasses ResolveState for all paths
// (regression) gets caught.
func TestGenerate_DefaultPathConsultsResolveState(t *testing.T) {
	t.Parallel()
	var settingsCalls atomic.Int32
	opts := Options{
		LoadSettings: func(_ context.Context) (bool, bool, error) {
			settingsCalls.Add(1)
			// Return enabled=false so ResolveState routes to StageSetup
			// quickly without trying to enumerate agents or open the
			// repo.
			return false, false, nil
		},
		ListInstalledAgents: func(_ context.Context) []types.AgentName {
			return nil
		},
	}
	root := &cobra.Command{Use: "entire"}

	result, err := Generate(context.Background(), root, opts)
	if err != nil {
		t.Fatalf("Generate returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("Generate returned nil result on the default path")
	}
	if got := settingsCalls.Load(); got == 0 {
		t.Error("LoadSettings was not called on the default path; ResolveState should consult settings")
	}
	// With enabled=false the routing is StageSetup, whose Markdown is
	// the hand-written setup prompt. Asserting on a stable substring
	// pins both the routing decision and the Result wiring.
	if !strings.Contains(result.Markdown, "Get started with Entire") {
		t.Errorf("Generate returned unexpected setup-stage markdown: %q", result.Markdown)
	}
}
