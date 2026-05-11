package learn

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
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
// Hermeticity: t.Setenv("PATH", "") so that
// external.DiscoverAndRegisterAlways doesn't register an
// entire-agent-* plugin from the host PATH into the package-shared
// agent registry, and so the built-in agents' CLI-availability checks
// all return false. ResolveTextGenerator then returns
// ErrNoTextGenerator deterministically regardless of test machine.
// Not parallel: t.Setenv is incompatible with t.Parallel.
func TestGenerate_RegenerateBypassesResolveState(t *testing.T) {
	t.Setenv("PATH", "")

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

	_, err := Generate(context.Background(), root, opts)
	if !errors.Is(err, ErrNoTextGenerator) {
		t.Fatalf("Generate(Regenerate=true, PATH=\"\") = %v; want ErrNoTextGenerator", err)
	}
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
//
// Hermeticity: ResolveState's first step is paths.WorktreeRoot, which
// walks up from CWD looking for a .git. A test run from a non-git
// CWD (some CI sandboxes strip the worktree) would short-circuit to
// StageNotGitRepo before LoadSettings is consulted, masking the
// bypass we're trying to assert. Stand up an isolated tmp repo and
// chdir into it so the test is independent of host CWD.
// Not parallel: t.Chdir is incompatible with t.Parallel.
func TestGenerate_DefaultPathConsultsResolveState(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	t.Chdir(tmpDir)

	var settingsCalls atomic.Int32
	opts := Options{
		LoadSettings: func(_ context.Context) (bool, bool, error) {
			settingsCalls.Add(1)
			// Return enabled=false so ResolveState routes to StageSetup
			// quickly without trying to enumerate agents or open the
			// repo further.
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
