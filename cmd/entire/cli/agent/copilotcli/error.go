package copilotcli

import (
	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Classifier holds the declarative classification data for the GitHub Copilot CLI.
// Consumed by agent.Classifier.Classify via copilotcli.GenerateText.
//
// No per-agent phrases are declared: Copilot's CLI passes through the upstream
// GitHub/OpenAI HTTP status on failure, so the shared HTTP-status baseline in
// text_gen_error.go catches the load-bearing cases (401/403 → auth,
// 429 → rate_limit, 400/404 → config). Anything else falls through to
// Unknown, where renderTextGenError still shows Copilot's own stderr verbatim
// via TextGenError.Message — so the user sees the real error text regardless
// of classification.
var Classifier = &agent.Classifier{
	Provider: agent.AgentNameCopilotCLI,
}
