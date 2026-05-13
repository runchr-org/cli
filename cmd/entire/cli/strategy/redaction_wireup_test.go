package strategy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/entireio/cli/redact"
)

// TestLoadAndApplyRedactionSettings_WiresOPF verifies that
// loadAndApplyRedactionSettings correctly reads OPF settings from
// .entire/settings.json and calls redact.ConfigurePrivacyFilter with the
// parsed values. The test bypasses EnsureRedactionConfigured entirely so it
// is not blocked by the sync.Once guard.
//
// Cannot t.Parallel() — this test mutates package globals in the redact
// package and uses t.Chdir, which Go's test framework rejects in parallel.
func TestLoadAndApplyRedactionSettings_WiresOPF(t *testing.T) {
	t.Cleanup(redact.ResetOPFConfigForTest)

	dir := t.TempDir()
	t.Chdir(dir)
	testutil.InitRepo(t, dir)

	settingsPath := filepath.Join(dir, ".entire", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsJSON := `{"redaction":{"openai_privacy_filter":{"enabled":true,"categories":{"private_person":true},"command":"/nonexistent/opf","timeout_seconds":15,"on_failure":"warn"}}}`
	if err := os.WriteFile(settingsPath, []byte(settingsJSON), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	loadAndApplyRedactionSettings()

	cfg := redact.GetOPFConfigForTest()
	if cfg == nil || !cfg.Enabled {
		t.Fatalf("OPF was not configured")
	}
	if cfg.Command != "/nonexistent/opf" {
		t.Errorf("Command: want /nonexistent/opf, got %q", cfg.Command)
	}
	if cfg.Timeout != 15 {
		t.Errorf("Timeout: want 15, got %d", cfg.Timeout)
	}
	if !cfg.Categories["private_person"] {
		t.Errorf("Categories[private_person] was not configured")
	}
}
