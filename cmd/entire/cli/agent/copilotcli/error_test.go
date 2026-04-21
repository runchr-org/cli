package copilotcli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// fixtureCopilot401Stderr exercises the shared HTTP-status baseline — Copilot's
// Classifier has no per-agent phrases, so this test is what proves auth still
// classifies correctly when the upstream (GitHub/OpenAI) returns 401. The
// exact wording is illustrative, not verbatim; classification depends on the
// "401" substring regardless of the surrounding text.
const fixtureCopilot401Stderr = `error: request failed with status 401 Unauthorized`

func TestClassifier_AuthViaHTTPStatusBaseline(t *testing.T) {
	t.Parallel()
	res := agent.ExecResult{Stderr: []byte(fixtureCopilot401Stderr), ExitCode: 1}
	err := Classifier.Classify(context.Background(), res, errors.New("exit 1"))
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("want *agent.TextGenError; got %v", err)
	}
	if tge.Kind != agent.TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth", tge.Kind)
	}
	if tge.Provider != agent.AgentNameCopilotCLI {
		t.Errorf("Provider = %q; want copilot-cli", tge.Provider)
	}
	if !strings.Contains(tge.Message, "401") {
		t.Errorf("Message does not preserve HTTP status marker: %q", tge.Message)
	}
}
