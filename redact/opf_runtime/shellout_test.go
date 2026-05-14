package opf_runtime

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// shCmd returns a Cmd that runs the given shell snippet via `sh -c`.
// Matches the claudecode test pattern.
func shCmd(ctx context.Context, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", script)
}

func TestShellOut_Success(t *testing.T) {
	t.Parallel()
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 5,
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			out := `{"detected_spans":[{"label":"private_person","start":0,"end":5,"text":"Alice"}]}`
			return shCmd(ctx, "printf '%s' '"+out+"'")
		},
	}
	spans, err := rt.Redact(context.Background(), "Alice is here.", []string{"private_person"})
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	if spans[0].Label != "private_person" || spans[0].Start != 0 || spans[0].End != 5 {
		t.Errorf("span mismatch: %+v", spans[0])
	}
}

func TestShellOut_NonZeroExit(t *testing.T) {
	t.Parallel()
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 5,
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return shCmd(ctx, "echo boom >&2; exit 2")
		},
	}
	_, err := rt.Redact(context.Background(), "x", []string{"private_person"})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err: want stderr passthrough, got %q", err)
	}
}

func TestShellOut_GarbledOutput(t *testing.T) {
	t.Parallel()
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 5,
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return shCmd(ctx, "printf 'not json'")
		},
	}
	_, err := rt.Redact(context.Background(), "x", []string{"private_person"})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

func TestShellOut_Timeout(t *testing.T) {
	t.Parallel()
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 1,
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return shCmd(ctx, "sleep 5")
		},
	}
	start := time.Now()
	_, err := rt.Redact(context.Background(), "x", []string{"private_person"})
	if err == nil {
		t.Fatalf("want timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("timeout did not cancel quickly enough: %v", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "timeout") {
		t.Errorf("err: want timeout, got %q", err)
	}
}

func TestShellOut_CommandNotFound(t *testing.T) {
	t.Parallel()
	// Use real NewShellOut (no injection) so we exercise exec.CommandContext
	// against a non-existent path.
	rt := NewShellOut("/nonexistent/opf-binary", 5)
	_, err := rt.Redact(context.Background(), "x", []string{"private_person"})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

func TestShellOut_ParentContextCanceled(t *testing.T) {
	t.Parallel()
	// Parent-context cancellation must produce a clear "canceled" message,
	// not be misreported as a generic "opf exited with error" (the bug we
	// fixed). The child callCtx will surface context.Canceled via cascade,
	// not context.DeadlineExceeded, so the timeout branch alone is insufficient.
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 30,
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return shCmd(ctx, "sleep 5")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := rt.Redact(ctx, "x", []string{"private_person"})
	if err == nil {
		t.Fatalf("want cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err: want context.Canceled in chain, got %q", err)
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Errorf("err: want 'canceled' message, got %q", err)
	}
}

// TestShellOut_RedactBatch_PartitionsSpansByInput verifies that batched
// calls correctly partition opf's single JSON output back to per-input
// span slices. The concatenated batch input has known boundaries; spans
// returned by opf are mapped to whichever input contains them.
//
// Inputs: ["Alice here", "no match", "secret abc"]
// Concatenation: "Alice here" + sep + "no match" + sep + "secret abc"
//   - "Alice here"  starts at 0, ends at 10
//   - sep at        10
//   - "no match"    starts at 11 (sep is 1 byte), ends at 19
//   - sep at        19
//   - "secret abc"  starts at 20, ends at 30
//
// Simulate opf returning spans at 0-5 (Alice) and 27-30 (abc).
func TestShellOut_RedactBatch_PartitionsSpansByInput(t *testing.T) {
	t.Parallel()
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 5,
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			// Span 1: "Alice" at [0,5] → input 0
			// Span 2: "abc" at [27,30] → input 2 (base 20, local [7,10])
			out := `{"detected_spans":[{"label":"private_person","start":0,"end":5},{"label":"secret","start":27,"end":30}]}`
			return shCmd(ctx, "printf '%s' "+shellQuote(out))
		},
	}
	inputs := []string{"Alice here", "no match", "secret abc"}
	batches, err := rt.RedactBatch(context.Background(), inputs, []string{"private_person", "secret"})
	if err != nil {
		t.Fatalf("RedactBatch: %v", err)
	}
	if len(batches) != 3 {
		t.Fatalf("want 3 output slices, got %d", len(batches))
	}
	if len(batches[0]) != 1 || batches[0][0].Label != "private_person" ||
		batches[0][0].Start != 0 || batches[0][0].End != 5 {
		t.Errorf("input 0: want private_person at [0,5], got %+v", batches[0])
	}
	if len(batches[1]) != 0 {
		t.Errorf("input 1: want empty, got %+v", batches[1])
	}
	if len(batches[2]) != 1 || batches[2][0].Label != "secret" ||
		batches[2][0].Start != 7 || batches[2][0].End != 10 {
		t.Errorf("input 2: want secret at local [7,10], got %+v", batches[2])
	}
}

// TestShellOut_RedactBatch_DropsSpansCrossingBoundary verifies that a
// span overlapping the batch separator (which shouldn't happen in
// practice but could if opf hallucinates) is dropped rather than
// mis-assigned to either input.
func TestShellOut_RedactBatch_DropsSpansCrossingBoundary(t *testing.T) {
	t.Parallel()
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 5,
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			// Concatenated: "Alice here\x1eno match" (lengths 10+1+8)
			// Span [8,13] crosses the separator at offset 10.
			out := `{"detected_spans":[{"label":"private_person","start":8,"end":13}]}`
			return shCmd(ctx, "printf '%s' "+shellQuote(out))
		},
	}
	batches, err := rt.RedactBatch(context.Background(),
		[]string{"Alice here", "no match"}, []string{"private_person"})
	if err != nil {
		t.Fatalf("RedactBatch: %v", err)
	}
	if len(batches[0]) != 0 || len(batches[1]) != 0 {
		t.Errorf("cross-boundary span should be dropped; got %+v / %+v", batches[0], batches[1])
	}
}

// TestShellOut_RedactBatch_EmbeddedNewlinesFlattened verifies that
// inputs with internal newlines are sent flattened (newlines → spaces)
// so opf doesn't split them across multiple outputs. Byte offsets stay
// 1:1 with the caller's original string since '\n' and ' ' are both 1 byte.
func TestShellOut_RedactBatch_EmbeddedNewlinesFlattened(t *testing.T) {
	t.Parallel()
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 5,
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			out := `{"detected_spans":[]}`
			return shCmd(ctx, "cat > /dev/null; printf '%s' "+shellQuote(out))
		},
	}
	multiline := "line one\nline two\nline three"
	batches, err := rt.RedactBatch(context.Background(), []string{multiline}, []string{"private_person"})
	if err != nil {
		t.Fatalf("RedactBatch: %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("want exactly 1 output slice for 1 logical input, got %d", len(batches))
	}
}

// shellQuote single-quotes s for safe interpolation into a `sh -c` arg.
// Embedded single quotes are escaped by closing+reopening.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// TestShellOut_CategoriesNotPassedToOPF locks in the design that opf is
// invoked without category filtering — opf has no CLI flag for it, so all
// categories are returned and detectOPF filters post-call. Regression
// guard against re-introducing a bogus --labels flag (or equivalent) that
// the real opf binary would reject.
func TestShellOut_CategoriesNotPassedToOPF(t *testing.T) {
	t.Parallel()
	var (
		mu      sync.Mutex
		gotArgs []string
	)
	rt := &shellOut{
		command:        "opf",
		timeoutSeconds: 5,
		CommandRunner: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			mu.Lock()
			gotArgs = append([]string(nil), args...)
			mu.Unlock()
			out := `{"detected_spans":[]}`
			return shCmd(ctx, "printf '%s' '"+out+"'")
		},
	}
	if _, err := rt.Redact(context.Background(), "x", []string{"private_person", "secret"}); err != nil {
		t.Fatalf("Redact: %v", err)
	}
	mu.Lock()
	joined := strings.Join(gotArgs, " ")
	mu.Unlock()
	if strings.Contains(joined, "private_person") || strings.Contains(joined, "secret") {
		t.Errorf("categories should NOT be passed to opf (opf has no category flag): %v", gotArgs)
	}
	// Verify the production flags we DO pass are present.
	for _, want := range []string{"--device", "cpu", "--output-mode", "typed", "--format", "json", "--no-print-color-coded-text"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing required arg %q in opf invocation: %v", want, gotArgs)
		}
	}
}
