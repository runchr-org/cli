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
	"unicode/utf8"
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

// Redact runs OPF on a single input. Convenience wrapper around RedactBatch
// for callers that only have one text to redact.
func (s *shellOut) Redact(ctx context.Context, text string, categories []string) ([]Span, error) {
	if len(categories) == 0 {
		return nil, nil
	}
	batch, err := s.RedactBatch(ctx, []string{text}, categories)
	if err != nil {
		return nil, err
	}
	if len(batch) == 0 {
		return nil, nil
	}
	return batch[0], nil
}

// batchSeparator joins inputs into a single opf invocation. opf treats
// '\n' as a per-input delimiter and runs a fresh inference pass per line —
// so a newline-joined batch processes each input as a separate forward
// pass through the model, which is no faster than per-call shell-out.
// Joining with a non-newline separator instead causes opf to treat the
// concatenation as ONE input and do ONE inference pass, amortizing the
// model load and forward-pass cost across all inputs.
//
// The chosen separator must:
//  1. Not appear in real text (so it doesn't collide with real content)
//  2. Look like whitespace to opf's tokenizer (so it doesn't confuse
//     span boundaries)
//
// ASCII RECORD SEPARATOR (U+001E) satisfies both: it's a control char
// not used in normal prose, and tokenizers generally treat control chars
// as whitespace.
const batchSeparator = "\x1e"

// RedactBatch sends multiple inputs to opf as a single invocation, in a
// single inference pass. The inputs are joined with batchSeparator and
// any embedded newlines are flattened to spaces (1-byte → 1-byte, so
// offsets stay valid). opf emits one JSON object covering the whole
// concatenated text; we partition its spans back to per-input slices
// based on the boundary positions we tracked.
//
// Why not just join with '\n' and decode N JSON objects? Empirically,
// opf processes a newline-joined batch as N sequential inference passes
// (one per line), each taking ~2s on CPU. For a 500-leaf transcript
// that's 1000+ seconds — same wall-clock as per-call shell-out, only
// with one cold-start saved. Single-pass concatenation drops the cost to
// 1 inference pass total. Spans crossing the separator boundary are
// dropped (rare in practice).
func (s *shellOut) RedactBatch(ctx context.Context, inputs []string, categories []string) ([][]Span, error) {
	if len(inputs) == 0 || len(categories) == 0 {
		return nil, nil
	}
	timeout := time.Duration(s.timeoutSeconds) * time.Second
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build the concatenated input + record where each input begins so we
	// can partition spans back. flatten internal newlines so a multi-line
	// leaf doesn't confuse opf's tokenizer.
	var buf strings.Builder
	starts := make([]int, len(inputs))
	for i, in := range inputs {
		if i > 0 {
			buf.WriteString(batchSeparator)
		}
		starts[i] = buf.Len()
		buf.WriteString(strings.ReplaceAll(in, "\n", " "))
	}
	batched := buf.String()

	args := []string{
		"--device", "cpu",
		"--output-mode", "typed",
		"--format", "json",
		"--no-print-color-coded-text",
	}

	cmd := s.CommandRunner(callCtx, s.command, args...)
	cmd.Stdin = strings.NewReader(batched)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		switch {
		case errors.Is(callCtx.Err(), context.DeadlineExceeded):
			return nil, fmt.Errorf("opf timeout after %s: %w", timeout, callCtx.Err())
		case errors.Is(ctx.Err(), context.Canceled):
			return nil, fmt.Errorf("opf canceled: %w", ctx.Err())
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			return nil, fmt.Errorf("opf exited with error: %w", err)
		}
		return nil, fmt.Errorf("opf exited with error: %s: %w", errMsg, err)
	}

	// Single concatenated input → opf emits one JSON object.
	var parsed opfOutput
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return nil, fmt.Errorf("opf output not parseable as JSON: %w (stdout: %q)", err, stdout.String())
	}

	// OPF returns character offsets (Python str slicing), not byte
	// offsets. For ASCII-only text the two agree; for multi-byte UTF-8
	// (e.g. box-drawing characters in Claude Code's `★ Insight ─────`
	// formatting) they diverge by N bytes per multi-byte char ahead of
	// the span. We must translate char→byte before partitioning, since
	// `starts` is byte-indexed (built from buf.Len()) and applyRegions
	// in the redact package also slices by byte.
	out := make([][]Span, len(inputs))
	for _, p := range parsed.DetectedSpans {
		byteStart := charToByteOffset(batched, p.Start)
		byteEnd := charToByteOffset(batched, p.End)
		if byteStart < 0 || byteEnd < 0 {
			continue // offset out of range — drop
		}
		idx := partitionIndex(starts, byteStart, byteEnd, batchSeparator)
		if idx < 0 {
			continue // span crosses a separator boundary — drop it
		}
		base := starts[idx]
		out[idx] = append(out[idx], Span{
			Start: byteStart - base,
			End:   byteEnd - base,
			Label: p.Label,
		})
	}
	return out, nil
}

// charToByteOffset converts a 0-based character (rune) offset into a byte
// offset within s. Returns -1 if charOff is past the end of s. For
// charOff == 0 this returns 0; for charOff equal to the number of runes
// in s, this returns len(s) (the end-of-string position).
func charToByteOffset(s string, charOff int) int {
	if charOff < 0 {
		return -1
	}
	if charOff == 0 {
		return 0
	}
	byteOff := 0
	for i := range charOff {
		if byteOff >= len(s) {
			if i == charOff-1 && byteOff == len(s) {
				return byteOff
			}
			return -1
		}
		_, size := utf8.DecodeRuneInString(s[byteOff:])
		byteOff += size
	}
	return byteOff
}

// partitionIndex returns the input index that contains the [spanStart, spanEnd)
// region within the concatenated batch input, or -1 if the region crosses
// a separator boundary or is outside any input.
//
// starts[i] is the byte offset where inputs[i] begins in the concatenation.
// Each input ends at starts[i+1] - len(sep) (the separator follows it), or
// at the end of the batched string for the last input.
func partitionIndex(starts []int, spanStart, spanEnd int, sep string) int {
	if spanStart < 0 || spanEnd <= spanStart {
		return -1
	}
	for i := range starts {
		base := starts[i]
		var end int
		if i+1 < len(starts) {
			end = starts[i+1] - len(sep)
		} else {
			// last input: we don't track its end byte. Caller's input
			// length isn't known here, but spanEnd <= some bound is
			// implied by opf returning valid spans. Accept any span
			// that starts >= base.
			end = 1 << 31
		}
		if spanStart >= base && spanEnd <= end {
			return i
		}
		if spanStart < base {
			return -1 // before the first input
		}
	}
	return -1
}

func (s *shellOut) Close() error { return nil }
