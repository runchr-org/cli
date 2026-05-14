package redact

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	OnFailure  string // "warn" (default); "block" is reserved but not yet enforced — settings layer rejects it at parse time
	// TODO(opf-block-mode): "block" is reserved in the schema but the
	// validator in cmd/entire/cli/settings rejects it at parse time, so it
	// can never reach this struct in production. handleOPFFailure always
	// falls through to warn behavior. Wiring "block" requires returning a
	// hard error from JSONLBytesWithPrivacyFilter /
	// JSONLContentWithPrivacyFilter (the other two entry points return
	// non-error types so they can't block) AND relaxing the settings-layer
	// rejection above.

	// runtime is package-private and constructed by ConfigurePrivacyFilter
	// from Command + Timeout. Tests use ConfigurePrivacyFilterWithRuntime to
	// inject a fake.
	runtime opf_runtime.Runtime
}

var (
	opfConfig   *OPFConfig
	opfConfigMu sync.RWMutex
	// opfBreakerTripped, once set, disables OPF for the rest of this process.
	// The first detectOPF failure flips it via handleOPFFailure; subsequent
	// detectOPF invocations short-circuit before shelling out. Without this,
	// a broken OPF install (binary missing, persistently timing out, etc.)
	// makes every redaction call pay the full timeout — turning one
	// condensation (or `entire doctor bundle`) into N × 30s waits instead of
	// a single warning and graceful fallback. Process-scoped so a fresh
	// invocation re-attempts; the typical flow (post-commit hook) is a fresh
	// process per commit, so users still get one chance per commit.
	opfBreakerTripped atomic.Bool
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
	opfConfig = &cfgCopy
	opfConfigMu.Unlock()
	// Reconfiguration resets the breaker — new command / timeout might fix
	// whatever broke the previous one.
	opfBreakerTripped.Store(false)
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
	opfConfig = &cfgCopy
	opfConfigMu.Unlock()
	opfBreakerTripped.Store(false)
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
	opfConfig = nil
	opfConfigMu.Unlock()
	opfBreakerTripped.Store(false)
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

// IsKnownOPFCategory reports whether name is one of the OPF native labels the
// CLI knows how to tag and render. Exported so the settings layer can reject
// typos at parse time instead of silently dropping them at detection time.
func IsKnownOPFCategory(name string) bool {
	_, ok := opfLabelMap[name]
	return ok
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
//
// Performance note: OPF shell-out has ~2s cold-start per invocation.
// JSONLContentWithPrivacyFilter batches all eligible leaf strings into one
// RedactBatch call, amortizing the cold-start across the transcript. The
// has-space gate further eliminates structural leaf strings (paths, IDs,
// snake_case keys) before they enter the batch — keeping inference input
// small without losing prose-shaped content that could contain names,
// emails, addresses, etc. Single-token personal-data shapes (e.g. an
// isolated email) are left to the regex layers, which already cover them.
// Daemon mode (future) makes the cold-start gate unnecessary entirely.
func detectOPF(ctx context.Context, cfg *OPFConfig, s string) []taggedRegion {
	if cfg == nil || !cfg.Enabled || cfg.runtime == nil || s == "" {
		return nil
	}
	if opfBreakerTripped.Load() {
		return nil // a prior call in this process already failed; skip silently
	}
	if !strings.ContainsRune(s, ' ') {
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
// Trips the process-scoped circuit breaker so subsequent detectOPF calls
// short-circuit, logs via slog.WarnContext, and prints a user-facing message
// to opfStderr via formatOPFFailure — all on the FIRST failure only.
//
// TODO(opf-block-mode): cfg.OnFailure is currently always "warn" because the
// settings layer (validateOPFSettings) rejects "block" at parse time. When
// the block-mode runtime wiring is implemented — plumbing a hard error
// through JSONLBytesWithPrivacyFilter / JSONLContentWithPrivacyFilter; the
// other two entry points return non-error types and can't surface a block —
// the parse-time rejection in settings.validateOPFSettings should be
// relaxed in lockstep, and this function should branch on cfg.OnFailure to
// return a real error to the caller.
func handleOPFFailure(ctx context.Context, cfg *OPFConfig, err error) {
	// CompareAndSwap so only the FIRST failure in this process emits the
	// user-facing warning. Subsequent detectOPF calls short-circuit before
	// shelling out (see detectOPF) and never reach this path, so the swap
	// is mostly a guard against concurrent first-call races.
	first := opfBreakerTripped.CompareAndSwap(false, true)
	if !first {
		return
	}
	slog.WarnContext(ctx, "OpenAI Privacy Filter call failed; disabling OPF for the rest of this process",
		componentAttr,
		slog.String("command", cfg.Command),
		slog.String("on_failure", cfg.OnFailure),
		slog.String("error", err.Error()),
	)
	if opfStderr != nil {
		fmt.Fprintln(opfStderr, formatOPFFailure(err, cfg.Command))
	}
}
