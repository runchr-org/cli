package memorylooptui

import (
	"context"

	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
)

// stateLoadedMsg is sent when the memoryloop state is loaded from disk.
type stateLoadedMsg struct {
	state *memoryloop.State
	err   error
}

// addMemoryMsg requests adding a new manual memory record.
type addMemoryMsg struct {
	input memoryloop.ManualRecordInput
}

type WizardIntent string

const (
	WizardIntentAdopt    WizardIntent = "adopt"
	WizardIntentApply    WizardIntent = "apply"
	WizardIntentSuppress WizardIntent = "suppress"
	WizardIntentArchive  WizardIntent = "archive"
)

type WizardRequest struct {
	Intent   WizardIntent
	RecordID string
	Scope    memoryloop.ScopeKind
	Location memoryloop.FileLocation
	Targets  []string
}

type WizardActionHandler func(ctx context.Context, request WizardRequest) (string, error)

type RunOptions struct {
	WizardActionHandler WizardActionHandler
}

// wizardOpenMsg opens the memory action wizard for a selected memory.
type wizardOpenMsg struct {
	record memoryloop.MemoryRecord
}

// wizardResultMsg is emitted when the wizard confirms an action.
type wizardResultMsg struct {
	success bool
	flash   string
	request WizardRequest
}

// wizardCloseMsg closes the wizard without submitting a request.
type wizardCloseMsg struct{}

// pruneMsg requests pruning stale/ineffective records.
type pruneMsg struct{}

// settingsChangedMsg indicates mode, policy, max_injected, or injection_scopes was changed.
type settingsChangedMsg struct {
	mode             *memoryloop.Mode
	activationPolicy *memoryloop.ActivationPolicy
	maxInjected      *int
	injectionScopes  *[]memoryloop.ScopeKind
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

// flashMsg shows a temporary status message in the status bar.
type flashMsg struct {
	text    string
	success bool
}

// clearFlashMsg clears the flash message after a timeout.
type clearFlashMsg struct{}
