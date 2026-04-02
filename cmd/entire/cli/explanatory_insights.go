// explanatory_insights.go implements the explanatory insights hook handler.
// When installed as a separate SessionStart hook, it injects context into the
// model to encourage explanatory output — similar to the Claude Code
// explanatory-output-style plugin.
package cli

import (
	"github.com/entireio/cli/cmd/entire/cli/agent"
)

const explanatoryInsightsPrompt = `Explanatory insights mode:
- Briefly explain why you chose an implementation approach when it is not obvious.
- Call out meaningful tradeoffs or constraints that shaped the change.
- Mention repo-specific patterns or conventions that influenced the implementation.
- Keep explanations concise and grounded in the current codebase.`

// handleExplanatoryInsightsHook outputs the explanatory insights prompt as
// additional context via the agent's hook response protocol.
func handleExplanatoryInsightsHook(ag agent.Agent) error {
	if writer, ok := agent.AsHookContextWriter(ag); ok {
		return writer.WriteHookResponseWithContext("", explanatoryInsightsPrompt)
	}
	// Fallback: if agent doesn't support context injection, output as visible message
	if writer, ok := agent.AsHookResponseWriter(ag); ok {
		return writer.WriteHookResponse(explanatoryInsightsPrompt)
	}
	return nil
}
