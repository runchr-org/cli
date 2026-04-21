package claudecode

import (
	"strconv"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

func TestParseClaudeEnvelope_NonErrorReturnsNoStructuredError(t *testing.T) {
	t.Parallel()
	stdout := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`)
	env, ok := parseClaudeEnvelope(stdout)
	if ok {
		t.Errorf("want (nil, false); got (%#v, true)", env)
	}
}

func TestParseClaudeEnvelope_IsErrorWithHTTP401MapsToAuth(t *testing.T) {
	t.Parallel()
	stdout := []byte(`{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"Auth required"}`)
	env, ok := parseClaudeEnvelope(stdout)
	if !ok {
		t.Fatal("want (result, true)")
	}
	if env.Kind != agent.TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth", env.Kind)
	}
	if env.Message != "Auth required" {
		t.Errorf("Message = %q; want envelope result text", env.Message)
	}
	if env.APIStatus != 401 {
		t.Errorf("APIStatus = %d; want 401", env.APIStatus)
	}
}

func TestParseClaudeEnvelope_AuthFromResultWhenStatusMissing(t *testing.T) {
	t.Parallel()
	stdout := []byte(`{"type":"result","is_error":true,"result":"Invalid API key provided"}`)
	env, ok := parseClaudeEnvelope(stdout)
	if !ok {
		t.Fatal("want (result, true)")
	}
	if env.Kind != agent.TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth via phrase heuristic", env.Kind)
	}
}

func TestParseClaudeEnvelope_MalformedJSONAtExit0Preserves963Wording(t *testing.T) {
	t.Parallel()
	stdout := []byte(`garbage not json`)
	env, ok := parseClaudeEnvelope(stdout)
	if !ok {
		t.Fatal("want (result, true) on malformed JSON — caller at exit 0 needs a non-nil error")
	}
	if env.Kind != agent.TextGenErrorUnknown {
		t.Errorf("Kind = %q; want unknown for malformed JSON", env.Kind)
	}
	if !strings.HasPrefix(env.Message, "failed to parse claude CLI response") {
		t.Errorf("Message = %q; want to start with 963's parse-failure wording", env.Message)
	}
}

func TestParseClaudeEnvelope_HTTPStatusMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   int
		wantKind agent.TextGenErrorKind
	}{
		{"Auth401", 401, agent.TextGenErrorAuth},
		{"Auth403", 403, agent.TextGenErrorAuth},
		{"RateLimit429", 429, agent.TextGenErrorRateLimit},
		{"Config400", 400, agent.TextGenErrorConfig},
		{"Config404", 404, agent.TextGenErrorConfig},
		{"Unknown5xx", 503, agent.TextGenErrorUnknown}, // 5xx is not Config per 963
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stdout := []byte(
				`{"type":"result","is_error":true,"api_error_status":` + strconv.Itoa(tc.status) + `,"result":"err"}`,
			)
			env, ok := parseClaudeEnvelope(stdout)
			if !ok {
				t.Fatal("want (result, true)")
			}
			if env.Kind != tc.wantKind {
				t.Errorf("status=%d Kind = %q; want %q", tc.status, env.Kind, tc.wantKind)
			}
		})
	}
}
