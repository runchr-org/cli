package pi

import (
	"context"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/review"
	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// Compile-time interface check: ReviewerTemplate implements AgentReviewer.
var _ reviewtypes.AgentReviewer = (*reviewtypes.ReviewerTemplate)(nil)

const wantAgentName = "pi"

// TestReviewer_NameMatchesRegistryKey locks the reviewer's name to the agent
// registry's stable key. adoptReviewEnv compares ENTIRE_REVIEW_AGENT against
// string(ag.Name()); drift here silently breaks review-session self-tagging.
func TestReviewer_NameMatchesRegistryKey(t *testing.T) {
	t.Parallel()
	if wantAgentName != string(agent.AgentNamePi) {
		t.Fatalf("wantAgentName = %q, agent.AgentNamePi = %q — keep these aligned",
			wantAgentName, string(agent.AgentNamePi))
	}
	if got := NewReviewer().Name(); got != wantAgentName {
		t.Errorf("Name() = %q, want %q", got, wantAgentName)
	}
}

func TestReviewer_EnvVarsSet(t *testing.T) {
	t.Parallel()
	cfg := reviewtypes.RunConfig{
		Skills:       []string{"/pr-review-toolkit:review-pr"},
		PerRunPrompt: "Focus on the auth module.",
		StartingSHA:  "abc123def456",
	}
	cmd := buildReviewCmd(context.Background(), cfg)

	envMap := make(map[string]string)
	for _, e := range cmd.Env {
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}
	for _, key := range []string{review.EnvSession, review.EnvAgent, review.EnvSkills, review.EnvPrompt, review.EnvStartingSHA} {
		if _, ok := envMap[key]; !ok {
			t.Errorf("env var %s not set on cmd", key)
		}
	}
	if envMap[review.EnvSession] != "1" {
		t.Errorf("%s = %q, want \"1\"", review.EnvSession, envMap[review.EnvSession])
	}
	if envMap[review.EnvAgent] != wantAgentName {
		t.Errorf("%s = %q, want %q", review.EnvAgent, envMap[review.EnvAgent], wantAgentName)
	}
	if envMap[review.EnvStartingSHA] != "abc123def456" {
		t.Errorf("%s = %q, want %q", review.EnvStartingSHA, envMap[review.EnvStartingSHA], "abc123def456")
	}
}

func TestReviewer_ArgvShape(t *testing.T) {
	t.Parallel()
	// Without a model: pi --mode json <prompt>
	cmd := buildReviewCmd(context.Background(), reviewtypes.RunConfig{PerRunPrompt: "extra"})
	if len(cmd.Args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(cmd.Args), cmd.Args)
	}
	if cmd.Args[0] != "pi" || cmd.Args[1] != "--mode" || cmd.Args[2] != "json" {
		t.Errorf("argv prefix = %v, want [pi --mode json ...]", cmd.Args[:3])
	}
	if cmd.Args[3] == "" {
		t.Error("Args[3] (prompt) is empty")
	}
	if cmd.Stdin != nil {
		t.Errorf("cmd.Stdin = %v, want nil (pi receives prompt via argv)", cmd.Stdin)
	}

	// With a model: --model <pattern> is appended.
	cmd = buildReviewCmd(context.Background(), reviewtypes.RunConfig{Model: "sonnet:high"})
	if len(cmd.Args) != 6 || cmd.Args[4] != "--model" || cmd.Args[5] != "sonnet:high" {
		t.Errorf("argv = %v, want [... --model sonnet:high]", cmd.Args)
	}
}

// collectEvents drains the parser channel into a slice for assertions.
func collectEvents(ch <-chan reviewtypes.Event) []reviewtypes.Event {
	var evs []reviewtypes.Event
	for e := range ch {
		evs = append(evs, e)
	}
	return evs
}

func TestParsePiOutput_FullStream(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`{"type":"session","version":3,"id":"uuid","cwd":"/repo"}`,
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"tool_execution_start","toolCallId":"t1","toolName":"read","args":{"path":"main.go"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Found an issue."}],"usage":{"input":100,"output":20,"cacheRead":10}}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Here is the fix."}],"usage":{"input":150,"output":30,"cacheRead":40}}}`,
		`{"type":"agent_end","messages":[]}`,
	}, "\n")

	evs := collectEvents(parsePiOutput(strings.NewReader(stream)))

	if len(evs) == 0 {
		t.Fatal("no events produced")
	}
	if _, ok := evs[0].(reviewtypes.Started); !ok {
		t.Errorf("first event = %T, want Started", evs[0])
	}

	var texts []string
	var sawToolRead bool
	var tokens reviewtypes.Tokens
	var finished reviewtypes.Finished
	var sawTokens, sawFinished bool
	for _, e := range evs {
		switch ev := e.(type) {
		case reviewtypes.AssistantText:
			texts = append(texts, ev.Text)
		case reviewtypes.ToolCall:
			if ev.Name == "read" {
				sawToolRead = true
			}
		case reviewtypes.Tokens:
			tokens, sawTokens = ev, true
		case reviewtypes.Finished:
			finished, sawFinished = ev, true
		}
	}

	if got := strings.Join(texts, "|"); got != "Found an issue.|Here is the fix." {
		t.Errorf("assistant text = %q, want both messages in order", got)
	}
	if !sawToolRead {
		t.Error("expected ToolCall for read")
	}
	if !sawTokens {
		t.Fatal("expected a Tokens event")
	}
	// Output summed across messages: 20+30 = 50; input from last message: 150+40 = 190.
	if tokens.Out != 50 {
		t.Errorf("tokens.Out = %d, want 50", tokens.Out)
	}
	if tokens.In != 190 {
		t.Errorf("tokens.In = %d, want 190", tokens.In)
	}
	if !sawFinished || !finished.Success {
		t.Errorf("Finished = %+v (saw=%v), want Success=true", finished, sawFinished)
	}
}

// TestParsePiOutput_NoAgentEndIsFailure: a torn stream that never reaches
// agent_end must report Finished{Success:false}.
func TestParsePiOutput_NoAgentEndIsFailure(t *testing.T) {
	t.Parallel()
	stream := `{"type":"agent_start"}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"partial"}]}}`

	evs := collectEvents(parsePiOutput(strings.NewReader(stream)))
	last := evs[len(evs)-1]
	fin, ok := last.(reviewtypes.Finished)
	if !ok {
		t.Fatalf("last event = %T, want Finished", last)
	}
	if fin.Success {
		t.Error("Finished.Success = true, want false (no agent_end)")
	}
}

// TestParsePiOutput_StringContent: assistant content may be a plain string
// rather than an array of blocks.
func TestParsePiOutput_StringContent(t *testing.T) {
	t.Parallel()
	stream := `{"type":"message_end","message":{"role":"assistant","content":"plain string body"}}
{"type":"agent_end"}`

	var got string
	for _, e := range collectEvents(parsePiOutput(strings.NewReader(stream))) {
		if at, ok := e.(reviewtypes.AssistantText); ok {
			got = at.Text
		}
	}
	if got != "plain string body" {
		t.Errorf("assistant text = %q, want %q", got, "plain string body")
	}
}

// TestParsePiOutput_IgnoresUserMessages: tool-result echoes (role != assistant)
// must not surface as assistant text.
func TestParsePiOutput_IgnoresUserMessages(t *testing.T) {
	t.Parallel()
	stream := `{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"file contents"}]}}
{"type":"agent_end"}`

	for _, e := range collectEvents(parsePiOutput(strings.NewReader(stream))) {
		if _, ok := e.(reviewtypes.AssistantText); ok {
			t.Errorf("non-assistant message leaked as AssistantText: %+v", e)
		}
	}
}
