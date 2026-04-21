package copilotcli

import (
	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Classifier holds the declarative classification data for the GitHub Copilot CLI.
// Consumed by agent.Classifier.Classify via copilotcli.GenerateText.
//
// Phrase selection note: no verbatim Copilot CLI stderr fixtures have been
// captured yet, so this Classifier intentionally ships with no copilot-specific
// auth phrases. The shared HTTP-status baseline in text_gen_error.go catches
// "401"/"403"/"429"/etc. independently, which is the load-bearing signal for
// Copilot's upstream GitHub/OpenAI APIs. Rate-limit/config phrases below are
// conservative common-convention seeds and should be refined once real
// failures inform the rules.
var Classifier = &agent.Classifier{
	Provider: agent.AgentNameCopilotCLI,
	Phrases: []agent.PhraseRule{
		{Kind: agent.TextGenErrorRateLimit, Phrase: "rate limit"},
		{Kind: agent.TextGenErrorRateLimit, Phrase: "quota"},
	},
}
