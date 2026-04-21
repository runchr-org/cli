package claudecode

import (
	"fmt"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// parseClaudeEnvelope inspects Claude CLI stdout for the structured JSON
// envelope and reports a classified EnvelopeResult when the envelope
// represents a failure.
//
// Return contract:
//   - (nil, false): no envelope to classify. Reached when stdout is empty
//     (subprocess produced no output — e.g., CLIMissing or SIGKILL before
//     any writes) OR when the envelope parsed cleanly with is_error:false
//     (success path).
//   - (*EnvelopeResult, true): structured failure — either an is_error:true
//     envelope classified by api_error_status / result-phrase heuristic, or
//     a malformed-JSON case (stdout non-empty but unparseable) synthesized
//     as TextGenErrorUnknown with 963's "failed to parse claude CLI
//     response" wording.
//
// The empty-stdout carveout preserves 963's fallthrough semantics: when the
// subprocess died before emitting anything, the envelope path must not
// preempt CLIMissing / stderr-phrase / stderr-passthrough classification.
// 963's equivalent gate was `parseErr == nil && env != nil && env.IsError`
// applied at the caller; here we lift that gate into the parser as
// len(stdout) == 0 so the shared Classifier engine needs no special case.
func parseClaudeEnvelope(stdout []byte) (*agent.EnvelopeResult, bool) {
	if len(stdout) == 0 {
		return nil, false
	}
	result, envelope, parseErr := parseGenerateTextResponse(stdout)
	if parseErr != nil {
		// Case (c): malformed JSON → synthesize Unknown with 963's wording.
		return &agent.EnvelopeResult{
			Kind:    agent.TextGenErrorUnknown,
			Message: fmt.Sprintf("failed to parse claude CLI response: %v", parseErr),
		}, true
	}
	if envelope == nil || !envelope.IsError {
		return nil, false // case (b): success
	}
	// Case (a): structured error — classify by api_error_status or phrase.
	apiStatus := 0
	if envelope.APIErrorStatus != nil {
		apiStatus = *envelope.APIErrorStatus
	}
	env := &agent.EnvelopeResult{Message: result, APIStatus: apiStatus}
	switch {
	case env.APIStatus == 401, env.APIStatus == 403:
		env.Kind = agent.TextGenErrorAuth
	case env.APIStatus == 429:
		env.Kind = agent.TextGenErrorRateLimit
	case env.APIStatus >= 400 && env.APIStatus < 500:
		env.Kind = agent.TextGenErrorConfig
	case env.APIStatus == 0 && containsAuthPhrase(result):
		env.Kind = agent.TextGenErrorAuth
	default:
		env.Kind = agent.TextGenErrorUnknown
	}
	return env, true
}

// envelopeAuthPhrases is the small phrase list used as a last-resort heuristic
// when the envelope carries is_error:true without a structured
// api_error_status. Mirrors 963's authStderrPhrases.
var envelopeAuthPhrases = []string{"invalid api key", "not logged in"}

func containsAuthPhrase(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range envelopeAuthPhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
