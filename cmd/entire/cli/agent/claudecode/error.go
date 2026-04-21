package claudecode

import (
	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Classifier is the declarative classification config for the Claude CLI.
// Consumed by the shared agent.Classifier.Classify engine.
//
// Claude's primary failure mode is exit 0 with is_error:true in the JSON
// envelope on stdout — ParseEnvelope exists to express that semantic. The
// small Phrases list is a best-effort fallback for crashes that exit non-zero
// before the envelope is produced.
var Classifier = &agent.Classifier{
	Provider: agent.AgentNameClaudeCode,
	Phrases: []agent.PhraseRule{
		{Kind: agent.TextGenErrorAuth, Phrase: "invalid api key"},
		{Kind: agent.TextGenErrorAuth, Phrase: "not logged in"},
	},
	ParseEnvelope: parseClaudeEnvelope,
}
