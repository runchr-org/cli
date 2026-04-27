package cli

import (
	"strings"
	"testing"
)

// TestCollectUsableReviews_FiltersStatusAndEmpty pins that the
// synthesis input only includes successful agents with non-empty
// post-filter narrative. Failed/cancelled/empty entries would dilute
// the prompt with stub text the LLM has nothing useful to do with.
func TestCollectUsableReviews_FiltersStatusAndEmpty(t *testing.T) {
	t.Parallel()
	tasks := []MultiAgentTask{
		{Name: "ok-a"},
		{Name: "failed"},
		{Name: "cancelled"},
		{Name: "empty"},
		{Name: "ok-b"},
	}
	results := []AgentRunResult{
		{Name: "ok-a", Status: AgentRunDone, FinalOutput: []byte("first finding\n")},
		{Name: "failed", Status: AgentRunFailed, ExitCode: 1, FinalOutput: []byte("partial output")},
		{Name: "cancelled", Status: AgentRunCancelled, FinalOutput: []byte("partial output")},
		{Name: "empty", Status: AgentRunDone, FinalOutput: []byte("")},
		{Name: "ok-b", Status: AgentRunDone, FinalOutput: []byte("second finding\n")},
	}

	got := collectUsableReviews(tasks, results)
	if len(got) != 2 {
		t.Fatalf("expected 2 usable reviews, got %d: %+v", len(got), got)
	}
	if got[0].Name != "ok-a" || got[1].Name != "ok-b" {
		t.Errorf("usable agents = [%s, %s], want [ok-a, ok-b]", got[0].Name, got[1].Name)
	}
	for _, r := range got {
		if strings.TrimSpace(r.Content) == "" {
			t.Errorf("usable review %s has empty content", r.Name)
		}
	}
}

// TestBuildVerdictSynthesisPrompt_StructureAndAttribution pins that the
// prompt fed to the summarizer (a) names every agent so the LLM can
// attribute findings, (b) embeds each agent's content under a clear
// delimiter, and (c) requests the four-section verdict shape the user
// sees in the rendered output.
func TestBuildVerdictSynthesisPrompt_StructureAndAttribution(t *testing.T) {
	t.Parallel()
	reviews := []reviewForSynthesis{
		{Name: "claude-code", Content: "issue X in file.go:10"},
		{Name: "codex", Content: "issue Y in file.go:20"},
	}
	prompt := buildVerdictSynthesisPrompt(reviews)

	wantSubstrs := []string{
		"2 independent code reviews", // count anchors the framing
		"─── claude-code ───",
		"─── codex ───",
		"issue X in file.go:10",
		"issue Y in file.go:20",
		"Common findings",
		"Unique findings",
		"Disagreements",
		"Priority order",
	}
	for _, want := range wantSubstrs {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q in:\n%s", want, prompt)
		}
	}

	// No preamble: agents being asked to be terse must themselves see a
	// terse prompt. Guard against accidental "Hi assistant," style
	// preambles being added in the future.
	if strings.HasPrefix(prompt, "Hi") || strings.HasPrefix(prompt, "Hello") {
		t.Errorf("prompt should not open with a greeting; got: %q", prompt[:50])
	}
}
