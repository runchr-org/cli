package redact

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/redact/opf_runtime"
)

func TestProgressMessage_TTY(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := newProgressWriter(&out, true /* tty */, false /* accessible */)
	p.Start("scanning transcript")
	p.Finish(1800 * time.Millisecond)
	got := out.String()
	if !strings.Contains(got, "→ OpenAI Privacy Filter: scanning transcript") {
		t.Errorf("missing start line: %q", got)
	}
	if !strings.Contains(got, "✓ OpenAI Privacy Filter: done (1.8s)") {
		t.Errorf("missing done line: %q", got)
	}
}

func TestProgressMessage_NonTTY(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := newProgressWriter(&out, false, false)
	p.Start("scanning transcript")
	p.Finish(1800 * time.Millisecond)
	got := out.String()
	if !strings.Contains(got, "[entire] redaction: openai privacy filter scanning transcript") {
		t.Errorf("missing non-tty start: %q", got)
	}
}

func TestProgressMessage_Accessible(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := newProgressWriter(&out, true, true /* accessible */)
	p.Start("scanning")
	p.Finish(1 * time.Second)
	got := out.String()
	if strings.ContainsAny(got, "\r→✓") {
		t.Errorf("accessible mode contains terminal control chars: %q", got)
	}
}

func TestFailureMessage_NotOnPath(t *testing.T) {
	t.Parallel()
	got := formatOPFFailure(errOPFNotFound, "opf")
	if !strings.Contains(got, "'opf' not found on PATH") {
		t.Errorf("missing not-found phrase: %q", got)
	}
	if !strings.Contains(got, "pip install opf") {
		t.Errorf("missing install instruction: %q", got)
	}
	if !strings.Contains(got, "Falling back to default redaction layers.") {
		t.Errorf("missing fallback receipt: %q", got)
	}
}

func TestFailureMessage_Timeout(t *testing.T) {
	t.Parallel()
	got := formatOPFFailure(opfErrTimeout(45), "opf")
	if !strings.Contains(got, "exceeded 45s timeout") {
		t.Errorf("missing timeout phrasing: %q", got)
	}
}

func TestDetectOPF_EmitsProgressOnSuccess(t *testing.T) {
	resetOPFConfig()
	t.Cleanup(resetOPFConfig)

	var buf bytes.Buffer
	origStderr := opfStderr
	opfStderr = &buf
	t.Cleanup(func() { opfStderr = origStderr })

	fake := &fakeRuntime{spans: []opf_runtime.Span{{Start: 0, End: 5, Label: "private_person"}}}
	ConfigurePrivacyFilterWithRuntime(OPFConfig{
		Enabled:    true,
		Categories: map[string]bool{"private_person": true},
	}, fake)

	_ = detectOPF(context.Background(), getOPFConfig(), "Alice is here.")

	got := buf.String()
	if !strings.Contains(got, "scanning transcript") {
		t.Errorf("missing start message: %q", got)
	}
	if !strings.Contains(got, "done") {
		t.Errorf("missing done message: %q", got)
	}
}
