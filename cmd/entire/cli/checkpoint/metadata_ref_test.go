package checkpoint_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v6/plumbing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func writeSettings(t *testing.T, repoDir string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repoDir, paths.EntireDir), 0o755); err != nil {
		t.Fatalf("mkdir .entire: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, paths.EntireDir, paths.SettingsFileName), []byte(contents), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

func TestMetadataRef_LegacyVsCustom(t *testing.T) {
	// Not t.Parallel() — uses t.Chdir for settings.Load CWD resolution.
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	t.Chdir(dir)

	// No settings: legacy branch ref.
	if got := checkpoint.MetadataRef(context.Background()); got != plumbing.NewBranchReferenceName(paths.MetadataBranchName) {
		t.Fatalf("default = %s; want legacy branch ref", got)
	}

	// checkpoints_version: 1.1 → custom ref.
	writeSettings(t, dir, `{"strategy_options":{"checkpoints_version":"1.1"}}`)
	if got := checkpoint.MetadataRef(context.Background()); got != plumbing.ReferenceName(paths.MetadataRefName) {
		t.Fatalf("1.1 = %s; want %s", got, paths.MetadataRefName)
	}

	// checkpoints_version: 1 → legacy.
	writeSettings(t, dir, `{"strategy_options":{"checkpoints_version":1}}`)
	if got := checkpoint.MetadataRef(context.Background()); got != plumbing.NewBranchReferenceName(paths.MetadataBranchName) {
		t.Fatalf("v1 = %s; want legacy branch ref", got)
	}
}

func TestMetadataTrackingRef_LegacyVsCustom(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	t.Chdir(dir)

	if got := checkpoint.MetadataTrackingRef(context.Background()); got != plumbing.NewRemoteReferenceName("origin", paths.MetadataBranchName) {
		t.Fatalf("default tracking = %s; want legacy tracking", got)
	}

	writeSettings(t, dir, `{"strategy_options":{"checkpoints_version":1.1}}`)
	if got := checkpoint.MetadataTrackingRef(context.Background()); got != plumbing.ReferenceName(paths.MetadataTrackingRefName) {
		t.Fatalf("1.1 tracking = %s; want %s", got, paths.MetadataTrackingRefName)
	}
}
