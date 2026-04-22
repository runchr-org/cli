package cli

import (
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

func TestRenderTextGenError_ClaudeWordingMatches963(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		in    *agent.TextGenError
		want  string // substring (Contains) or exact match when exact=true
		exact bool   // if true, assert ==; else assert Contains
	}{
		{
			name: "claude auth, envelope provides message",
			in:   &agent.TextGenError{Kind: agent.TextGenErrorAuth, Provider: agent.AgentNameClaudeCode, Message: "Invalid API key"},
			want: "Claude authentication failed: Invalid API key\nRun `claude login` and retry",
		},
		{
			name: "claude auth, empty message falls back to 963 wording",
			in:   &agent.TextGenError{Kind: agent.TextGenErrorAuth, Provider: agent.AgentNameClaudeCode},
			want: "Claude authentication failed\nRun `claude login` and retry",
		},
		{
			name: "claude rate limit, with message",
			in:   &agent.TextGenError{Kind: agent.TextGenErrorRateLimit, Provider: agent.AgentNameClaudeCode, Message: "429"},
			want: "Claude rejected the summary request due to rate limits or quota: 429\nWait and retry",
		},
		{
			name: "claude config, with message",
			in:   &agent.TextGenError{Kind: agent.TextGenErrorConfig, Provider: agent.AgentNameClaudeCode, Message: "model not found"},
			want: "Claude rejected the summary request: model not found\nCheck your Claude CLI config and selected model",
		},
		{
			name: "claude CLI missing (no message, no model)",
			in:   &agent.TextGenError{Kind: agent.TextGenErrorCLIMissing, Provider: agent.AgentNameClaudeCode},
			want: "Claude CLI is not installed or not on PATH",
		},
		// Unknown-branch outputs are pinned exactly — the new wording ("Claude API
		// returned HTTP N", "Claude CLI exited with code N") is a minor evolution
		// from 963 ("Anthropic API", lowercase "claude CLI"). Accepting this drift
		// normalizes capitalization with the other Claude-prefixed branches.
		{
			name:  "claude unknown with APIStatus renders HTTP status (exact)",
			in:    &agent.TextGenError{Kind: agent.TextGenErrorUnknown, Provider: agent.AgentNameClaudeCode, APIStatus: 500},
			want:  "Claude failed to generate the summary (Claude API returned HTTP 500)",
			exact: true,
		},
		{
			name:  "claude unknown with ExitCode renders exit code (exact)",
			in:    &agent.TextGenError{Kind: agent.TextGenErrorUnknown, Provider: agent.AgentNameClaudeCode, ExitCode: 137},
			want:  "Claude failed to generate the summary (Claude CLI exited with code 137)",
			exact: true,
		},
		{
			name:  "claude unknown with negative ExitCode renders abnormal (exact)",
			in:    &agent.TextGenError{Kind: agent.TextGenErrorUnknown, Provider: agent.AgentNameClaudeCode, ExitCode: -1},
			want:  "Claude failed to generate the summary (Claude CLI terminated abnormally — no exit code captured)",
			exact: true,
		},
		{
			name:  "claude all-zero unknown renders diagnostic sentinel (exact)",
			in:    &agent.TextGenError{Kind: agent.TextGenErrorUnknown, Provider: agent.AgentNameClaudeCode},
			want:  "Claude failed to generate the summary (no diagnostic detail available from Claude CLI)",
			exact: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := renderTextGenError(tc.in)
			if tc.exact {
				if got.Error() != tc.want {
					t.Errorf("renderTextGenError() =\n  %q\nwant exactly:\n  %q", got.Error(), tc.want)
				}
			} else {
				if !strings.Contains(got.Error(), tc.want) {
					t.Errorf("renderTextGenError() = %q\nwant to contain: %q", got.Error(), tc.want)
				}
			}
		})
	}
}

// TestRenderTextGenError_NonClaudeProvidersUseStderrVerbatim pins the
// non-Claude rendering rule EXACTLY (not via Contains) because this is the
// behavioral divergence from Claude's 963-style synthesis append. For these
// providers with Message present, the output must be exactly
// "<prefix>: <msg>" with no trailing synthesized remediation line — the CLI's
// own stderr already carries its authoritative remediation.
func TestRenderTextGenError_NonClaudeProvidersUseStderrVerbatim(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   *agent.TextGenError
		want string // EXACT match — no Contains
	}{
		// The four non-Claude providers share the same "no synthesis appended"
		// rule. One representative case pins it exactly; other providers are
		// covered by their package's generate_test.go and the shared matrix.
		{
			name: "non-Claude auth renders stderr verbatim with no fallback appended",
			in: &agent.TextGenError{
				Kind:     agent.TextGenErrorAuth,
				Provider: agent.AgentNameGemini,
				Message:  "Please set an Auth method in your settings.json or specify one of: GEMINI_API_KEY",
			},
			want: "Gemini authentication failed: Please set an Auth method in your settings.json or specify one of: GEMINI_API_KEY",
		},
		{
			name: "codex CLI missing (no message, no model)",
			in:   &agent.TextGenError{Kind: agent.TextGenErrorCLIMissing, Provider: agent.AgentNameCodex},
			want: "Codex CLI is not installed or not on PATH",
		},
		{
			name: "gemini empty-message auth falls back to generic synthesis",
			in:   &agent.TextGenError{Kind: agent.TextGenErrorAuth, Provider: agent.AgentNameGemini},
			want: "Gemini authentication failed\nCheck your Gemini CLI authentication",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := renderTextGenError(tc.in)
			if got.Error() != tc.want {
				t.Errorf("renderTextGenError() =\n  %q\nwant exactly:\n  %q", got.Error(), tc.want)
			}
		})
	}
}
