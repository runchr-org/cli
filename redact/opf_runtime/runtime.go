package opf_runtime

import "context"

// Runtime is the abstraction over the OpenAI Privacy Filter binary or
// daemon. Implementations must be safe for concurrent use.
type Runtime interface {
	Redact(ctx context.Context, text string, categories []string) ([]Span, error)
	Close() error
}

// Span is a redaction region returned by OPF, with character-offset
// boundaries against the input text.
type Span struct {
	Start int
	End   int
	Label string // OPF native label, e.g. "private_person"
}

// NewShellOut returns a v1 shell-out runtime. The struct and method
// bodies live in shellout.go (created in Chunk 2). Until that chunk
// lands, this returns a placeholder that any caller would see as a
// no-op runtime — but no production caller invokes this in Chunk 1.
func NewShellOut(_ string, _ int) Runtime {
	return noopRuntime{}
}

// noopRuntime is the placeholder returned by NewShellOut while
// shellout.go does not yet exist. Chunk 2 replaces the NewShellOut body
// to return a real *shellOut; noopRuntime is then deleted.
type noopRuntime struct{}

func (noopRuntime) Redact(_ context.Context, _ string, _ []string) ([]Span, error) {
	return nil, nil
}
func (noopRuntime) Close() error { return nil }
