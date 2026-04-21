package codex

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// fixtureCodexAuthStderr is the actual stderr Codex v0.121.0 produced in the
// 2026-04-20 research pass when run with no credentials. Captured verbatim
// from the spec's Appendix A. A diff here signals the CLI changed its error
// text and the classifier needs a data update.
const fixtureCodexAuthStderr = `WARNING: proceeding, even though we could not update PATH: Refusing to create helper binaries under temporary dir
OpenAI Codex v0.121.0 (research preview)
--------
workdir: /tmp
model: gpt-5.3-codex
provider: openai
--------
user
hi

2026-04-20T20:40:34.968182Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket: HTTP error: 401 Unauthorized
ERROR: unexpected status 401 Unauthorized: Missing bearer or basic authentication in header, url: https://api.openai.com/v1/responses, cf-ray: 9ef6f76c1c01e172-LAX, request id: req_1cc0f45d5316470d943504217ba72f64`

func TestClassifier_AuthFromCapturedStderr(t *testing.T) {
	t.Parallel()
	res := agent.ExecResult{
		Stderr:   []byte(fixtureCodexAuthStderr),
		ExitCode: 1,
	}
	err := Classifier.Classify(context.Background(), res, errors.New("exit 1"))
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("want *agent.TextGenError; got %v", err)
	}
	if tge.Kind != agent.TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth", tge.Kind)
	}
	if tge.Provider != agent.AgentNameCodex {
		t.Errorf("Provider = %q; want codex", tge.Provider)
	}
	if !strings.Contains(tge.Message, "Missing bearer or basic authentication") {
		t.Errorf("Message does not preserve Codex's verbatim stderr: %q", tge.Message)
	}
}
