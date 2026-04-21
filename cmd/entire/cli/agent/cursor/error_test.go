package cursor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// fixtureCursor401Stderr is a representative 401 passthrough pattern Cursor's
// `agent` CLI surfaces when the upstream model provider rejects credentials.
// No verbatim fixture has been captured for Cursor yet; once one is, replace
// this with the real stderr from a 2026-xx-xx research pass and update this
// comment accordingly. The HTTP-status baseline (shared across all providers)
// is what makes this test pass — the Classifier has no cursor-specific auth
// phrases yet.
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
