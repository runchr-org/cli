package cursor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// fixtureCursor401Stderr exercises the shared HTTP-status baseline — Cursor's
// Classifier has no per-agent phrases, so this test is what proves auth still
// classifies correctly when the upstream provider returns 401. The exact
// wording is illustrative, not verbatim; classification depends on the "401"
// substring regardless of the surrounding text.
const fixtureCursor401Stderr = `ERROR: upstream request failed: 401 Unauthorized`

func TestClassifier_AuthViaHTTPStatusBaseline(t *testing.T) {
	t.Parallel()
	res := agent.ExecResult{Stderr: []byte(fixtureCursor401Stderr), ExitCode: 1}
	err := Classifier.Classify(context.Background(), res, errors.New("exit 1"))
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("want *agent.TextGenError; got %v", err)
	}
	if tge.Kind != agent.TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth", tge.Kind)
	}
	if tge.Provider != agent.AgentNameCursor {
		t.Errorf("Provider = %q; want cursor", tge.Provider)
	}
	if !strings.Contains(tge.Message, "401") {
		t.Errorf("Message does not preserve HTTP status marker: %q", tge.Message)
	}
}
