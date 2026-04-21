package cursor

import (
	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Classifier holds the declarative classification data for the Cursor CLI
// (`agent` binary). Consumed by agent.Classifier.Classify via cursor.GenerateText.
//
// Phrase selection note: no verbatim Cursor stderr fixtures have been captured
// yet, so this Classifier intentionally ships with no cursor-specific auth
// phrases. The shared HTTP-status baseline in text_gen_error.go catches
// "401"/"403"/"429"/etc. independently, which is the load-bearing signal for
// Cursor's upstream (it proxies to various model providers and passes their
// HTTP status through on failure). Rate-limit/config phrases below are
// conservative common-convention seeds and should be refined once real
// failures inform the rules.
var Classifier = &agent.Classifier{
	Provider: agent.AgentNameCursor,
	Phrases: []agent.PhraseRule{
		{Kind: agent.TextGenErrorRateLimit, Phrase: "rate limit"},
		{Kind: agent.TextGenErrorRateLimit, Phrase: "quota"},
	},
}
