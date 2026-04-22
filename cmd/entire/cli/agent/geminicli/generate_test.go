package geminicli

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// TestGenerateText_AuthPhraseFromGeminiStderr covers the gemini-only path
// where stderr contains "Please set an Auth method" (no HTTP status). The
// shared HTTP baseline in agent.HandleTextGenResult would classify this as
// Unknown; geminicli's extraClassify hook upgrades it to Auth. The generic
// scenarios (CLIMissing, 401, empty, success) are exercised across all
// non-Claude agents by TestGenerateText_Matrix in agent/.
func TestGenerateText_AuthPhraseFromGeminiStderr(t *testing.T) {
	t.Parallel()
	ag := &GeminiCLIAgent{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c",
				`printf '%s' 'Please set an Auth method in your settings.json or specify one of: GEMINI_API_KEY' 1>&2; exit 41`)
		},
	}
	_, err := ag.GenerateText(context.Background(), "prompt", "")
	var tge *agent.TextGenError
	if !errors.As(err, &tge) {
		t.Fatalf("err = %v; want *agent.TextGenError", err)
	}
	if tge.Kind != agent.TextGenErrorAuth {
		t.Errorf("Kind = %q; want auth (from inline phrase)", tge.Kind)
	}
}
