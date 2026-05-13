package redact

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

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

// GetOPFConfigForTest returns the current configuration, or nil if never
// configured. The "ForTest" suffix signals test-only intent — callers should
// be limited to test files in other packages (the same-package tests use the
// private getOPFConfig). _test.go files cannot satisfy this need because Go
// excludes them from the package's cross-package import surface.
func GetOPFConfigForTest() *OPFConfig {
	return getOPFConfig()
}

// ResetOPFConfigForTest nils the package-level global so tests in other
// packages can return to "never configured" state. The "ForTest" suffix
// signals test-only intent; do not call from production code paths.
func ResetOPFConfigForTest() {
	opfConfigMu.Lock()
	defer opfConfigMu.Unlock()
	opfConfig = nil
}

// opfLabelMap maps OPF native labels to our taggedRegion.label values.
// Empty mapped value renders as the bare "REDACTED" token; non-empty
// values render as "[REDACTED_<LABEL>]" via the existing replacementToken
// helper in redact.go.
var opfLabelMap = map[string]string{
	"private_person":  "PERSON",
	"private_email":   "EMAIL",
	"private_phone":   "PHONE",
	"private_address": "ADDRESS",
	"private_url":     "URL",
	"private_date":    "DATE",
	"account_number":  "ACCOUNT_NUMBER",
	"secret":          "", // -> bare REDACTED
}

func mapOPFLabel(opfLabel string) string {
	mapped, ok := opfLabelMap[opfLabel]
	if !ok {
		return "" // unknown labels collapse to bare REDACTED rather than panicking
	}
	return mapped
}

// enabledCategories returns the subset of opfLabelMap keys that the user
// has explicitly set to true in cfg.Categories.
func enabledCategories(cfg *OPFConfig) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Categories))
	for label, enabled := range cfg.Categories {
		if !enabled {
			continue
		}
		if _, known := opfLabelMap[label]; !known {
			continue
		}
		out = append(out, label)
	}
	sort.Strings(out) // deterministic order for tests + logs
	return out
}

// detectOPF runs the OPF runtime and returns tagged regions for any spans
// whose category is enabled in cfg. Returns nil if cfg is nil, disabled,
// has no enabled categories, the runtime is unset, or the runtime returns
// an error. Errors are routed to the configured failure handler before
// returning nil.
func detectOPF(ctx context.Context, cfg *OPFConfig, s string) []taggedRegion {
	if cfg == nil || !cfg.Enabled || cfg.runtime == nil || s == "" {
		return nil
	}
	cats := enabledCategories(cfg)
	if len(cats) == 0 {
		return nil
	}

	progress := newProgressWriter(opfStderr, isTTYWriter(opfStderr), accessibleMode())
	progress.Start("scanning transcript")
	start := time.Now()
	spans, err := cfg.runtime.Redact(ctx, s, cats)
	if err != nil {
		handleOPFFailure(ctx, cfg, err)
		return nil
	}
	progress.Finish(time.Since(start))

	out := make([]taggedRegion, 0, len(spans))
	for _, sp := range spans {
		if !cfg.Categories[sp.Label] {
			continue // belt-and-suspenders: runtime returned a category we didn't ask for
		}
		if sp.Start < 0 || sp.End > len(s) || sp.Start >= sp.End {
			continue // ignore malformed spans rather than crashing
		}
		out = append(out, taggedRegion{
			region: region{sp.Start, sp.End},
			label:  mapOPFLabel(sp.Label),
		})
	}
	return out
}

// handleOPFFailure dispatches an OPF runtime error to the configured handler.
// Always logs via slog.WarnContext and prints a user-facing message to
// opfStderr via formatOPFFailure. In "block" mode, callers must propagate the
// returned-nil regions back up as a hard error; in "warn" mode (default),
// detectOPF simply returns nil and the existing seven layers complete the
// redaction without OPF.
func handleOPFFailure(ctx context.Context, cfg *OPFConfig, err error) {
	slog.WarnContext(ctx, "OpenAI Privacy Filter call failed",
		componentAttr,
		slog.String("command", cfg.Command),
		slog.String("on_failure", cfg.OnFailure),
		slog.String("error", err.Error()),
	)
	if opfStderr != nil {
		fmt.Fprintln(opfStderr, formatOPFFailure(err, cfg.Command))
	}
}
