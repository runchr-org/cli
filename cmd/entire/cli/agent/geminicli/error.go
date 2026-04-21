package geminicli

import (
	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Classifier holds the declarative classification data for the Gemini CLI.
// Consumed by agent.Classifier.Classify via geminicli.GenerateText.
//
// Phrase selection is evidence-based: the auth phrases are verbatim from the
// 2026-04-20 research pass (see spec Appendix A). Rate-limit phrases are
// seeded conservatively from common HTTP conventions; the shared HTTP-status
// baseline catches "429"/etc. independently.
var Classifier = &agent.Classifier{
	Provider: agent.AgentNameGemini,
	Phrases: []agent.PhraseRule{
		{Kind: agent.TextGenErrorAuth, Phrase: "Please set an Auth method"},
		{Kind: agent.TextGenErrorAuth, Phrase: "GEMINI_API_KEY"},
		{Kind: agent.TextGenErrorRateLimit, Phrase: "rate limit"},
		{Kind: agent.TextGenErrorRateLimit, Phrase: "quota"},
	},
}
