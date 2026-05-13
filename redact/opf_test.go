package redact

import "testing"

// NOTE: Tests in this file mutate the package-level opfConfig global via
// ConfigurePrivacyFilter. They cannot be t.Parallel'd — Go's test framework
// would race on the global even with the RWMutex (one test's t.Cleanup could
// wipe the global between another test's set and read). This mirrors the
// pii_test.go and custom_test.go patterns.

// resetOPFConfig clears any package-level OPF configuration so tests start from
// a known "never configured" state and don't leak configuration into each other.
// Mirrors the resetPIIConfig / customConfig = nil pattern used elsewhere in
// this package.
func resetOPFConfig() {
	opfConfigMu.Lock()
	defer opfConfigMu.Unlock()
	opfConfig = nil
}

func TestConfigurePrivacyFilter_StoresConfig(t *testing.T) {
	resetOPFConfig()
	cfg := OPFConfig{
		Enabled:    true,
		Categories: map[string]bool{"private_person": true},
		Command:    "/opt/opf",
		Timeout:    30,
		OnFailure:  "warn",
	}
	ConfigurePrivacyFilter(cfg)
	t.Cleanup(resetOPFConfig)

	got := getOPFConfig()
	if got == nil {
		t.Fatalf("getOPFConfig returned nil")
	}
	if !got.Enabled {
		t.Errorf("Enabled: want true")
	}
	if got.Command != "/opt/opf" {
		t.Errorf("Command: want /opt/opf, got %q", got.Command)
	}
}

func TestConfigurePrivacyFilter_DefaultsApplied(t *testing.T) {
	resetOPFConfig()
	ConfigurePrivacyFilter(OPFConfig{Enabled: true})
	t.Cleanup(resetOPFConfig)

	got := getOPFConfig()
	if got.Command != "opf" {
		t.Errorf("default Command: want %q, got %q", "opf", got.Command)
	}
	if got.Timeout != 30 {
		t.Errorf("default Timeout: want 30, got %d", got.Timeout)
	}
	if got.OnFailure != "warn" {
		t.Errorf("default OnFailure: want warn, got %q", got.OnFailure)
	}
}
