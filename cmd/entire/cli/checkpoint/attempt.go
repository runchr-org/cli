package checkpoint

import "time"

// AttemptHooks observes per-attempt progress when a function internally
// tries multiple fetch/read strategies. OnStart fires before each attempt;
// OnFinish fires after with the elapsed time and the attempt's error
// (nil = success, non-nil = the chain will move on to the next strategy).
// Either field may be nil to opt out. The zero value is a no-op.
type AttemptHooks struct {
	OnStart  func(label string)
	OnFinish func(label string, duration time.Duration, err error)
}

// WithLabel wraps a single attempt: emits OnStart, runs fn, emits OnFinish
// with fn's error. Callers that need the attempt's error capture it via
// outer-var assignment inside fn; WithLabel itself returns nothing so
// `hooks.WithLabel(...)` doesn't require a discard expression.
// Safe to call on the zero value (nil hooks become no-ops).
func (h AttemptHooks) WithLabel(label string, fn func() error) {
	if h.OnStart != nil {
		h.OnStart(label)
	}
	started := time.Now()
	err := fn()
	if h.OnFinish != nil {
		h.OnFinish(label, time.Since(started), err)
	}
}
