package redact

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os/exec"
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
	if !strings.Contains(got, "[entire] redaction: openai privacy filter completed in 1.8s") {
		t.Errorf("missing non-tty completed line: %q", got)
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

// TestFailureMessage_RealNotFoundErr exercises the production error path:
// shellout.go does not wrap with the synthetic errOPFNotFound sentinel —
// real exec failures surface exec.ErrNotFound or os.ErrNotExist instead.
// formatOPFFailure must recognize those too, otherwise the actionable
// "install opf" guidance never reaches users.
func TestFailureMessage_RealNotFoundErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
	}{
		{"exec.ErrNotFound", &exec.Error{Name: "opf", Err: exec.ErrNotFound}},
		{"os.PathError-ENOENT", &fs.PathError{Op: "fork/exec", Path: "/nonexistent/opf", Err: fs.ErrNotExist}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatOPFFailure(tc.err, "opf")
			if !strings.Contains(got, "not found on PATH") {
				t.Errorf("real not-found error not classified: %q", got)
			}
			if !strings.Contains(got, "pip install opf") {
				t.Errorf("install instruction missing for real not-found error: %q", got)
			}
		})
	}
}

// TestFailureMessage_RealTimeoutErr exercises the production timeout path:
// shellout.go wraps context.DeadlineExceeded with %w, not the synthetic
// opfTimeoutError type. formatOPFFailure must catch the wrapped form.
func TestFailureMessage_RealTimeoutErr(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("opf timeout after 30s: %w", context.DeadlineExceeded)
	got := formatOPFFailure(wrapped, "opf")
	if !strings.Contains(got, "Consider raising") {
		t.Errorf("real timeout not classified: %q", got)
	}
}

func TestHandleOPFFailure_WritesFormattedMessageToStderr(t *testing.T) {
	// Cannot t.Parallel — mutates opfStderr global.
	var buf bytes.Buffer
	origStderr := opfStderr
	opfStderr = &buf
	t.Cleanup(func() { opfStderr = origStderr })

	handleOPFFailure(context.Background(), &OPFConfig{
		Command:   "opf",
		OnFailure: "warn",
	}, errOPFNotFound)

	got := buf.String()
	if !strings.Contains(got, "'opf' not found on PATH") {
		t.Errorf("stderr message missing not-found phrase: %q", got)
	}
	if !strings.Contains(got, "Falling back to default redaction layers.") {
		t.Errorf("stderr message missing fallback receipt: %q", got)
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
	// Use "completed" rather than "done" — opfStderr in tests is *bytes.Buffer,
	// not *os.File, so isTTYWriter returns false and we exit via the non-TTY
	// branch which says "completed in N.Ns". TTY-mode wording uses "done (N.Ns)".
	if !strings.Contains(got, "completed in") {
		t.Errorf("missing completed message: %q", got)
	}
}
