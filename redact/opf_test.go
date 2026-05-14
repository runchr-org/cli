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
	// batchCalls counts RedactBatch invocations independently of Redact.
	// Used by tests that need to assert OPF is batched (one call per
	// transcript) rather than per-leaf-string.
	batchCalls int
}

func (f *fakeRuntime) Redact(_ context.Context, _ string, _ []string) ([]opf_runtime.Span, error) {
	f.calls++
	return f.spans, f.err
}

// RedactBatch returns the same canned spans for every input — sufficient
// for behavior tests. For tests that need per-input spans, use the more
// detailed fakeBatchRuntime below.
func (f *fakeRuntime) RedactBatch(_ context.Context, inputs []string, _ []string) ([][]opf_runtime.Span, error) {
	f.batchCalls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]opf_runtime.Span, len(inputs))
	for i := range inputs {
		out[i] = f.spans
	}
	return out, nil
}

func (f *fakeRuntime) Close() error { return nil }

// fakeBatchRuntime returns per-input spans rather than a single canned
// slice repeated for every input. Use it for tests that need different
// OPF responses for different leaf strings.
type fakeBatchRuntime struct {
	spansByInput map[string][]opf_runtime.Span
	err          error
	batchCalls   int
	lastInputs   []string
}

func (f *fakeBatchRuntime) Redact(ctx context.Context, text string, cats []string) ([]opf_runtime.Span, error) {
	batches, err := f.RedactBatch(ctx, []string{text}, cats)
	if err != nil {
		return nil, err
	}
	if len(batches) == 0 {
		return nil, nil
	}
	return batches[0], nil
}

func (f *fakeBatchRuntime) RedactBatch(_ context.Context, inputs []string, _ []string) ([][]opf_runtime.Span, error) {
	f.batchCalls++
	f.lastInputs = append([]string(nil), inputs...)
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]opf_runtime.Span, len(inputs))
	for i, in := range inputs {
		out[i] = f.spansByInput[in]
	}
	return out, nil
}

func (f *fakeBatchRuntime) Close() error { return nil }

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

	// Must contain a space — detectOPF skips OPF for non-prose inputs
	// (perf gate, see detectOPF doc).
	got := detectOPF(context.Background(), getOPFConfig(), "Alice was here")
	if got != nil {
		t.Errorf("warn mode: got %v, want nil regions after failure", got)
	}
}

func TestDetectOPF_DropsMalformedSpans(t *testing.T) {
	resetOPFConfig()
	t.Cleanup(resetOPFConfig)

	// Text must contain a space — detectOPF skips OPF for non-prose
	// inputs (perf gate, see detectOPF doc).
	const text = "Alice here" // len = 10
	fake := &fakeRuntime{spans: []opf_runtime.Span{
		{Start: 0, End: 5, Label: "private_person"},    // valid — kept
		{Start: -1, End: 3, Label: "private_person"},   // negative Start — dropped
		{Start: 3, End: 1000, Label: "private_person"}, // End past len(s) — dropped
		{Start: 4, End: 4, Label: "private_person"},    // Start == End — dropped
		{Start: 5, End: 3, Label: "private_person"},    // Start > End — dropped
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

// TestDetectOPF_SkipsNonProseStrings locks in the perf gate that prevents
// detectOPF from invoking opf on structural strings (paths, IDs, type tags,
// snake_case keys). Without this gate, JSONLContentWithPrivacyFilter calls
// opf for hundreds of leaf strings per transcript and condensation becomes
// effectively unusable on realistic inputs.
func TestDetectOPF_SkipsNonProseStrings(t *testing.T) {
	resetOPFConfig()
	t.Cleanup(resetOPFConfig)

	fake := &fakeRuntime{spans: []opf_runtime.Span{{Start: 0, End: 5, Label: "private_person"}}}
	ConfigurePrivacyFilterWithRuntime(OPFConfig{
		Enabled:    true,
		Categories: map[string]bool{"private_person": true},
	}, fake)

	cases := []struct {
		name string
		in   string
	}{
		{"file path", "src/main.go"},
		{"snake_case key", "user_prompt_submit"},
		{"kebab-case", "tool-use-id"},
		{"hex id", "a3b2c4d5e6f7"},
		{"single word", "Alice"},
		{"camelCase", "userPromptSubmit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			callsBefore := fake.calls
			got := detectOPF(context.Background(), getOPFConfig(), tc.in)
			if got != nil {
				t.Errorf("got %v regions for non-prose %q, want nil", got, tc.in)
			}
			if fake.calls != callsBefore {
				t.Errorf("runtime invoked on non-prose %q (calls went from %d to %d)", tc.in, callsBefore, fake.calls)
			}
		})
	}
}
