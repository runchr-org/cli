package opf_runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// shellOut runs the user-installed `opf` binary per call. Cold-start every
// invocation is intentional for v1 — see the spec's runtime decision section.
// Daemon mode (a future implementation of Runtime) reuses this same wire
// format so callers do not change.
type shellOut struct {
	command        string
	timeoutSeconds int

	// CommandRunner builds the *exec.Cmd that is run for each Redact call.
	// Tests override this with a closure that returns a Cmd wrapping a shell
	// snippet, mirroring the pattern in cmd/entire/cli/agent/claudecode.
	// Production callers go through NewShellOut, which defaults this to
	// exec.CommandContext.
	CommandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// opfOutput is the subset of OPF's typed JSON output we consume.
// The `text` field on each span (and the top-level `redacted_text`)
// is intentionally omitted — callers reconstruct redacted text from the
// input plus Start/End offsets so the rendering style stays consistent
// with the other seven redaction layers.
type opfOutput struct {
	DetectedSpans []struct {
		Label string `json:"label"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	} `json:"detected_spans"`
}

func (s *shellOut) Redact(ctx context.Context, text string, categories []string) ([]Span, error) {
	if len(categories) == 0 {
		return nil, nil
	}
	timeout := time.Duration(s.timeoutSeconds) * time.Second
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"--device", "cpu",
		"--output-mode", "typed",
		"--labels", strings.Join(categories, ","),
	}

	cmd := s.CommandRunner(callCtx, s.command, args...)
	cmd.Stdin = strings.NewReader(text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		switch {
		case errors.Is(callCtx.Err(), context.DeadlineExceeded):
			return nil, fmt.Errorf("opf timeout after %s: %w", timeout, callCtx.Err())
		case errors.Is(ctx.Err(), context.Canceled):
			// Distinguish parent-context cancellation from child timeout;
			// the generic exit-error branch below produces a misleading
			// "context canceled" message that hides the real cause.
			return nil, fmt.Errorf("opf canceled: %w", ctx.Err())
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("opf exited with error: %s", errMsg)
	}

	var parsed opfOutput
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return nil, fmt.Errorf("opf output not parseable as JSON: %w (stdout: %q)", err, stdout.String())
	}

	out := make([]Span, 0, len(parsed.DetectedSpans))
	for _, p := range parsed.DetectedSpans {
		out = append(out, Span{Start: p.Start, End: p.End, Label: p.Label})
	}
	return out, nil
}

func (s *shellOut) Close() error { return nil }
