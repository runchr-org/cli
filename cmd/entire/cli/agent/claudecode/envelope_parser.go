package claudecode

import (
	"fmt"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// classifyClaudeEnvelope inspects Claude CLI stdout for the structured JSON
// envelope and returns a typed *TextGenError when the envelope represents a
// failure, nil otherwise.
//
// Return contract:
//   - nil: no envelope to classify. Reached when stdout is empty (subprocess
//     produced no output — e.g. CLIMissing or SIGKILL before any writes) OR
//     when the envelope parsed cleanly with is_error:false (success path).
//   - non-nil: structured failure — either an is_error:true envelope
//     classified by api_error_status / auth-phrase heuristic, or a malformed-
//     JSON case (stdout non-empty but unparseable).
//
// The empty-stdout carveout preserves 963's fallthrough semantics: when the
// subprocess died before emitting anything, the envelope path must not
// preempt CLIMissing / stderr / ctx-sentinel classification done by the
// caller.
func classifyClaudeEnvelope(stdout []byte) *agent.TextGenError {
	if len(stdout) == 0 {
		return nil
	}
	result, envelope, parseErr := parseGenerateTextResponse(stdout)
	if parseErr != nil {
		return &agent.TextGenError{
			Kind:     agent.TextGenErrorUnknown,
			Provider: agent.AgentNameClaudeCode,
			Message:  fmt.Sprintf("failed to parse claude CLI response: %v", parseErr),
		}
	}
	if envelope == nil || !envelope.IsError {
		return nil
	}
	apiStatus := 0
	if envelope.APIErrorStatus != nil {
		apiStatus = *envelope.APIErrorStatus
	}
	e := &agent.TextGenError{
		Provider:  agent.AgentNameClaudeCode,
		Message:   result,
		APIStatus: apiStatus,
	}
	switch {
	case apiStatus == 401, apiStatus == 403:
		e.Kind = agent.TextGenErrorAuth
	case apiStatus == 429:
		e.Kind = agent.TextGenErrorRateLimit
	case apiStatus >= 400 && apiStatus < 500:
		e.Kind = agent.TextGenErrorConfig
	case apiStatus == 0 && containsAuthPhrase(result):
		// Last-resort heuristic for envelopes that carry is_error:true
		// without a structured api_error_status. Small, evidence-based list
		// from 963.
		e.Kind = agent.TextGenErrorAuth
	default:
		e.Kind = agent.TextGenErrorUnknown
	}
	return e
}

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
