package memorylooptui

import (
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

// stateLoadedMsg is sent when the memoryloop state is loaded from disk.
type stateLoadedMsg struct {
	state *memoryloop.State
	err   error
}

// lifecycleActionMsg requests a lifecycle transition on a memory record.
type lifecycleActionMsg struct {
	id     string
	action memoryloop.LifecycleAction
}

// addMemoryMsg requests adding a new manual memory record.
type addMemoryMsg struct {
	input memoryloop.ManualRecordInput
}

// pruneMsg requests pruning stale/ineffective records.
type pruneMsg struct{}

// settingsChangedMsg indicates mode, policy, or max_injected was changed.
type settingsChangedMsg struct {
	mode             *memoryloop.Mode
	activationPolicy *memoryloop.ActivationPolicy
	maxInjected      *int
}

// testPromptMsg requests a prompt relevance test.
type testPromptMsg struct {
	prompt string
}

// testPromptResultMsg contains the results of a prompt test.
type testPromptResultMsg struct {
	matches []memoryloop.Match
}

// refreshStartedMsg indicates a refresh has begun.
type refreshStartedMsg struct{}

// refreshProgressMsg reports refresh progress text.
//
//nolint:unused // used in later task (history tab implementation)
type refreshProgressMsg struct {
	text string
}

// refreshDoneMsg indicates a refresh has completed.
//
//nolint:unused // used in later task (history tab implementation)
type refreshDoneMsg struct {
	state *memoryloop.State
	err   error
}

// errorFlashMsg shows a temporary error message in the status bar.
type errorFlashMsg struct {
	text string
}

// clearErrorMsg clears the error flash after a timeout.
type clearErrorMsg struct{}
