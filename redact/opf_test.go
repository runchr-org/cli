package redact

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/entireio/cli/redact/opf_runtime"
)

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

type fakeRuntime struct {
	spans []opf_runtime.Span
	err   error
	calls int
}

func (f *fakeRuntime) Redact(_ context.Context, _ string, _ []string) ([]opf_runtime.Span, error) {
	f.calls++
	return f.spans, f.err
}

func (f *fakeRuntime) Close() error { return nil }

func TestDetectOPF_DisabledReturnsNil(t *testing.T) {
	resetOPFConfig()
	t.Cleanup(resetOPFConfig)

	fake := &fakeRuntime{spans: []opf_runtime.Span{{Start: 0, End: 5, Label: "private_person"}}}
	ConfigurePrivacyFilterWithRuntime(OPFConfig{Enabled: false, Categories: map[string]bool{"private_person": true}}, fake)

	got := detectOPF(context.Background(), getOPFConfig(), "Alice is here.")
	if got != nil {
		t.Errorf("got %v, want nil for disabled config", got)
	}
	if fake.calls != 0 {
		t.Errorf("runtime called %d times, want 0", fake.calls)
	}
}

func TestDetectOPF_MapsLabelsCorrectly(t *testing.T) {
	// Pure mapping test — does not touch globals; can use t.Parallel.
	t.Parallel()
	cases := []struct {
		opfLabel    string
		wantLabel   string
		wantTokenIn string
	}{
		{"private_person", "PERSON", "[REDACTED_PERSON]"},
		{"private_email", "EMAIL", "[REDACTED_EMAIL]"},
		{"private_phone", "PHONE", "[REDACTED_PHONE]"},
		{"private_address", "ADDRESS", "[REDACTED_ADDRESS]"},
		{"private_url", "URL", "[REDACTED_URL]"},
		{"private_date", "DATE", "[REDACTED_DATE]"},
		{"account_number", "ACCOUNT_NUMBER", "[REDACTED_ACCOUNT_NUMBER]"},
		{"secret", "", "REDACTED"},
	}
	for _, tc := range cases {
		t.Run(tc.opfLabel, func(t *testing.T) {
			t.Parallel()
			if got := mapOPFLabel(tc.opfLabel); got != tc.wantLabel {
				t.Errorf("mapOPFLabel(%q): got %q, want %q", tc.opfLabel, got, tc.wantLabel)
			}
			if got := replacementToken(tc.wantLabel); got != tc.wantTokenIn {
				t.Errorf("replacementToken(%q): got %q, want %q", tc.wantLabel, got, tc.wantTokenIn)
			}
		})
	}
}

func TestDetectOPF_FiltersDisabledCategories(t *testing.T) {
	resetOPFConfig()
	t.Cleanup(resetOPFConfig)

	fake := &fakeRuntime{spans: []opf_runtime.Span{
		{Start: 0, End: 5, Label: "private_person"},
		{Start: 6, End: 11, Label: "private_email"},
	}}
	ConfigurePrivacyFilterWithRuntime(OPFConfig{
		Enabled:    true,
		Categories: map[string]bool{"private_person": true, "private_email": false},
	}, fake)

	got := detectOPF(context.Background(), getOPFConfig(), "Alice email")
	if len(got) != 1 {
		t.Fatalf("got %d regions, want 1 (private_email should be filtered out)", len(got))
	}
	if got[0].label != "PERSON" {
		t.Errorf("region label: got %q, want PERSON", got[0].label)
	}
}

func TestDetectOPF_FailureWithWarn_ReturnsNil(t *testing.T) {
	resetOPFConfig()
	t.Cleanup(resetOPFConfig)

	// Suppress the user-facing stderr message handleOPFFailure prints —
	// otherwise it pollutes `go test -v` output. Format-specific assertions
	// live in TestHandleOPFFailure_WritesFormattedMessageToStderr.
	origStderr := opfStderr
	opfStderr = io.Discard
	t.Cleanup(func() { opfStderr = origStderr })

	fake := &fakeRuntime{err: errors.New("boom")}
	ConfigurePrivacyFilterWithRuntime(OPFConfig{
		Enabled:    true,
		Categories: map[string]bool{"private_person": true},
		OnFailure:  "warn",
	}, fake)

	got := detectOPF(context.Background(), getOPFConfig(), "Alice")
	if got != nil {
		t.Errorf("warn mode: got %v, want nil regions after failure", got)
	}
}

func TestDetectOPF_DropsMalformedSpans(t *testing.T) {
	resetOPFConfig()
	t.Cleanup(resetOPFConfig)

	const text = "Alice" // len = 5
	fake := &fakeRuntime{spans: []opf_runtime.Span{
		{Start: 0, End: 5, Label: "private_person"},   // valid — kept
		{Start: -1, End: 3, Label: "private_person"},  // negative Start — dropped
		{Start: 3, End: 100, Label: "private_person"}, // End past len(s) — dropped
		{Start: 4, End: 4, Label: "private_person"},   // Start == End — dropped
		{Start: 5, End: 3, Label: "private_person"},   // Start > End — dropped
	}}
	ConfigurePrivacyFilterWithRuntime(OPFConfig{
		Enabled:    true,
		Categories: map[string]bool{"private_person": true},
	}, fake)

	got := detectOPF(context.Background(), getOPFConfig(), text)
	if len(got) != 1 {
		t.Fatalf("got %d regions, want 1 (only valid span survives)", len(got))
	}
	if got[0].start != 0 || got[0].end != 5 {
		t.Errorf("region: got [%d,%d), want [0,5)", got[0].start, got[0].end)
	}
}
