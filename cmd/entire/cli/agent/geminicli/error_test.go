package geminicli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// fixtureGeminiAuthStderr: real stderr from gemini-cli 0.34.0 in the
// 2026-04-20 research pass with no credentials configured. Captured
// verbatim; includes the pre-auth ENOENT noise that prepends the actual
// auth-method guidance on fresh profiles (see spec Risk #2).
const fixtureGeminiAuthStderr = `Failed to save project registry to /tmp/home/.gemini/projects.json: Error: ENOENT: no such file or directory
  errno: -2,
  code: 'ENOENT',
  syscall: 'rename',
  path: '/tmp/home/.gemini/projects.json.tmp',
  dest: '/tmp/home/.gemini/projects.json'
}
Please set an Auth method in your /tmp/home/.gemini/settings.json or specify one of the following environment variables before running: GEMINI_API_KEY, GOOGLE_GENAI_USE_VERTEXAI, GOOGLE_GENAI_USE_GCA`

func TestClassifier_AuthFromCapturedStderr(t *testing.T) {
	t.Parallel()
	res := agent.ExecResult{Stderr: []byte(fixtureGeminiAuthStderr), ExitCode: 41}
	err := Classifier.Classify(context.Background(), res, errors.New("exit 41"))
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("want *agent.TextGenError; got %v", err)
	}
	if tge.Kind != agent.TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth", tge.Kind)
	}
	if tge.Provider != agent.AgentNameGemini {
		t.Errorf("Provider = %q; want gemini", tge.Provider)
	}
	if !strings.Contains(tge.Message, "Please set an Auth method") {
		t.Errorf("Message does not preserve Gemini's auth guidance: %q", tge.Message)
	}
}
