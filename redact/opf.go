package redact

import (
	"sync"

	"github.com/entireio/cli/redact/opf_runtime"
)

// opfDefaultOnFailure is the default value for OPFConfig.OnFailure.
const opfDefaultOnFailure = "warn"

// OPFConfig configures the OpenAI Privacy Filter detection layer.
// All fields are validated and defaulted by ConfigurePrivacyFilter; callers
// should pass values straight from settings without local normalization.
type OPFConfig struct {
	Enabled    bool
	Categories map[string]bool
	Command    string
	Timeout    int    // seconds; 0 means use default of 30
	OnFailure  string // "warn" (default) or "block"

	// runtime is package-private and constructed by ConfigurePrivacyFilter
	// from Command + Timeout. Tests use ConfigurePrivacyFilterWithRuntime to
	// inject a fake.
	runtime opf_runtime.Runtime
}

var (
	opfConfig   *OPFConfig
	opfConfigMu sync.RWMutex
)

// ConfigurePrivacyFilter sets the global OPF configuration and constructs the
// default shell-out runtime. Call once at process startup after loading
// settings. Thread-safe. Subsequent calls replace the previous configuration.
func ConfigurePrivacyFilter(cfg OPFConfig) {
	cfgCopy := cfg
	if cfgCopy.Timeout <= 0 {
		cfgCopy.Timeout = 30
	}
	if cfgCopy.OnFailure == "" {
		cfgCopy.OnFailure = opfDefaultOnFailure
	}
	if cfgCopy.Command == "" {
		cfgCopy.Command = "opf"
	}
	cfgCopy.runtime = opf_runtime.NewShellOut(cfgCopy.Command, cfgCopy.Timeout)
	opfConfigMu.Lock()
	defer opfConfigMu.Unlock()
	opfConfig = &cfgCopy
}

// ConfigurePrivacyFilterWithRuntime is the test-only variant that takes an
// explicit Runtime instead of constructing one. Production callers must use
// ConfigurePrivacyFilter.
func ConfigurePrivacyFilterWithRuntime(cfg OPFConfig, rt opf_runtime.Runtime) {
	cfgCopy := cfg
	if cfgCopy.Timeout <= 0 {
		cfgCopy.Timeout = 30
	}
	if cfgCopy.OnFailure == "" {
		cfgCopy.OnFailure = opfDefaultOnFailure
	}
	cfgCopy.runtime = rt
	opfConfigMu.Lock()
	defer opfConfigMu.Unlock()
	opfConfig = &cfgCopy
}

// getOPFConfig returns the current configuration, or nil if never configured.
func getOPFConfig() *OPFConfig {
	opfConfigMu.RLock()
	defer opfConfigMu.RUnlock()
	return opfConfig
}

// GetOPFConfig returns the current configuration, or nil if never configured.
// This is the exported equivalent of getOPFConfig, used by tests in other
// packages that need to inspect the global state.
func GetOPFConfig() *OPFConfig {
	return getOPFConfig()
}

// ResetOPFConfig nils the package-level global so tests can return to
// "never configured" state. Only call from test cleanup functions.
func ResetOPFConfig() {
	opfConfigMu.Lock()
	defer opfConfigMu.Unlock()
	opfConfig = nil
}
