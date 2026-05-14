package opf_runtime

import (
	"context"
	"os/exec"
)

// Runtime is the abstraction over the OpenAI Privacy Filter binary or
// daemon. Implementations must be safe for concurrent use.
type Runtime interface {
	// Redact runs OPF on a single text input. Returns spans against the
	// input's byte offsets.
	Redact(ctx context.Context, text string, categories []string) ([]Span, error)

	// RedactBatch runs OPF on multiple inputs in a single invocation.
	// The returned slice has the same length as inputs; result[i] are
	// the spans for inputs[i] against that input's own byte offsets.
	// Used by JSONLContentWithPrivacyFilter to amortize the per-call
	// cold-start cost across all eligible leaf strings in a transcript.
	RedactBatch(ctx context.Context, inputs []string, categories []string) ([][]Span, error)

	Close() error
}

// Span is a redaction region returned by OPF, with byte-offset boundaries
// against the input text. Half-open interval: text[Start:End] is the redacted
// substring. OPF itself reports character (rune) offsets via its JSON output;
// the shellout adapter translates those to byte offsets before returning Spans
// so that callers can slice []byte input directly without re-walking runes.
type Span struct {
	Start int
	End   int
	Label string // OPF native label, e.g. "private_person"
}

// NewShellOut returns a Runtime that shells out to the OPF binary on each
// Redact call. command is the path or name of the opf binary;
// timeoutSeconds is the per-call timeout.
func NewShellOut(command string, timeoutSeconds int) Runtime {
	return &shellOut{
		command:        command,
		timeoutSeconds: timeoutSeconds,
		CommandRunner:  exec.CommandContext,
	}
}
