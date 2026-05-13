package opf_runtime

import (
	"context"
	"os/exec"
)

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
