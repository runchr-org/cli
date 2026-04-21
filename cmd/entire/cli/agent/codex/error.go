package codex

import (
	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Classifier holds the declarative classification data for the Codex CLI.
// Consumed by agent.Classifier.Classify via codex.GenerateText.
//
// Phrase selection is evidence-based: the Auth phrase is verbatim from the
// 2026-04-20 research pass (see spec Appendix A). Rate-limit and Config
// phrases are seeded conservatively from common HTTP conventions; the shared
// HTTP-status baseline catches "401"/"429"/etc. independently, so these are
// a best-effort supplement until real-world failures inform refinements.
var Classifier = &agent.Classifier{
	Provider: agent.AgentNameCodex,
	Phrases: []agent.PhraseRule{
		{Kind: agent.TextGenErrorAuth, Phrase: "Missing bearer or basic authentication"},
		{Kind: agent.TextGenErrorRateLimit, Phrase: "rate limit"},
		{Kind: agent.TextGenErrorRateLimit, Phrase: "quota"},
		{Kind: agent.TextGenErrorConfig, Phrase: "model not found"},
	},
}
