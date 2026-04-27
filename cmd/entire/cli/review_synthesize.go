package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/logging"
)

// synthesizeCombinedVerdictTimeout caps how long we wait for the
// summarizer to respond. Generous because cross-review synthesis is
// genuinely useful work, but bounded so a stuck CLI doesn't wedge the
// user's terminal indefinitely after the agents have already finished.
const synthesizeCombinedVerdictMinAgents = 2

// maybeSynthesizeCombinedVerdict offers the user an opt-in cross-agent
// synthesis after the per-agent dumps and the counts line. Skipped
// silently when:
//   - run was cancelled (user already opted out of finishing this work);
//   - fewer than 2 agents produced usable output (nothing to synthesize);
//   - stdin isn't a TTY (CI or piped output — nobody to prompt);
//   - the user answers no to the confirm prompt.
//
// Failures from the prompt provider are non-fatal — synthesis is value-
// added, not load-bearing — they print a one-line note and fall through.
func maybeSynthesizeCombinedVerdict(ctx context.Context, out io.Writer, tasks []MultiAgentTask, results []AgentRunResult, cancelled bool) {
	if cancelled {
		return
	}
	usable := collectUsableReviews(tasks, results)
	if len(usable) < synthesizeCombinedVerdictMinAgents {
		return
	}
	if !interactive.CanPromptInteractively() {
		return
	}

	confirm := false
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Synthesize a combined verdict across agents?").
			Description("Calls your configured summary provider once. Skip to commit reviews as-is.").
			Affirmative("Yes").
			Negative("No").
			Value(&confirm),
	))
	if err := form.Run(); err != nil {
		// Form errors here are typically the user pressing ctrl+c at the
		// prompt — treat as "no synthesis" and continue. Log at debug so
		// real errors aren't lost.
		logging.Debug(ctx, "synthesize verdict: prompt cancelled or failed",
			slog.String("error", err.Error()))
		return
	}
	if !confirm {
		return
	}

	verdict, err := synthesizeCombinedVerdict(ctx, out, usable)
	if err != nil {
		// Soft failure — the agents already did their work; missing
		// synthesis is a degradation, not a crash. Tell the user once
		// and move on.
		fmt.Fprintf(out, "\n  Synthesis unavailable: %v\n", err)
		return
	}
	fmt.Fprintln(out, "\n─────── combined verdict ───────")
	fmt.Fprintln(out, strings.TrimSpace(verdict))
}

// reviewForSynthesis pairs an agent name with the cleaned narrative
// fed into the synthesis prompt. Mirrors the shape dumpPerAgentReviews
// emits to the user so the LLM sees the same content the user did.
type reviewForSynthesis struct {
	Name    string
	Content string
}

// collectUsableReviews filters results down to ones that produced
// meaningful output. Failed/cancelled/empty agents are excluded — the
// synthesis prompt is more useful with two real reviews than with one
// real review and one "agent failed" stub.
func collectUsableReviews(tasks []MultiAgentTask, results []AgentRunResult) []reviewForSynthesis {
	usable := make([]reviewForSynthesis, 0, len(results))
	for i, r := range results {
		if r.Status != AgentRunDone {
			continue
		}
		cleaned := applyOutputFilter(tasks[i].Name, r.FinalOutput)
		final := strings.TrimSpace(extractFinalMessage(tasks[i].Name, string(cleaned)))
		if final == "" {
			continue
		}
		usable = append(usable, reviewForSynthesis{Name: tasks[i].Name, Content: final})
	}
	return usable
}

// synthesizeCombinedVerdict resolves the configured summary provider
// (claude/codex/gemini whichever is installed; see
// resolveCheckpointSummaryProvider) and asks it to synthesize a single
// verdict from the per-agent reviews.
func synthesizeCombinedVerdict(ctx context.Context, w io.Writer, reviews []reviewForSynthesis) (string, error) {
	provider, err := resolveCheckpointSummaryProvider(ctx, w)
	if err != nil {
		return "", fmt.Errorf("resolve summary provider: %w", err)
	}
	ag, err := getSummaryAgent(provider.Name)
	if err != nil {
		return "", fmt.Errorf("get summary agent %s: %w", provider.Name, err)
	}
	tg, ok := agent.AsTextGenerator(ag)
	if !ok {
		return "", fmt.Errorf("agent %s does not support text generation", provider.Name)
	}
	prompt := buildVerdictSynthesisPrompt(reviews)
	out, err := tg.GenerateText(ctx, prompt, provider.Model)
	if err != nil {
		return "", fmt.Errorf("generate verdict via %s: %w", provider.Name, err)
	}
	if strings.TrimSpace(out) == "" {
		return "", errors.New("synthesis returned empty output")
	}
	return out, nil
}

// buildVerdictSynthesisPrompt composes the LLM prompt that turns N
// per-agent reviews into a single unified verdict. Structured to favor
// agreement (high confidence), divergence (single-source claims), and
// priority — what a user actually needs to act on after a review.
func buildVerdictSynthesisPrompt(reviews []reviewForSynthesis) string {
	var sb strings.Builder
	sb.WriteString("You are synthesizing ")
	fmt.Fprintf(&sb, "%d", len(reviews))
	sb.WriteString(" independent code reviews of the same change. Each agent reviewed the same diff and produced findings independently below.\n\n")
	for _, r := range reviews {
		fmt.Fprintf(&sb, "─── %s ───\n%s\n\n", r.Name, r.Content)
	}
	sb.WriteString("Produce a unified verdict with these sections:\n")
	sb.WriteString("1. **Common findings** — issues multiple agents flagged (high confidence). Include the originating agents in parens.\n")
	sb.WriteString("2. **Unique findings** — issues only one agent raised. Group by agent. Note whether they look meaningful or noise.\n")
	sb.WriteString("3. **Disagreements** — places where agents took conflicting positions. Resolve if you can; flag if you can't.\n")
	sb.WriteString("4. **Priority order** — what the user should act on first, ordered by impact.\n\n")
	sb.WriteString("Be concise. Use bullet points. No preamble, no closing pleasantries. Reference filenames and line numbers when the underlying reviews provided them.")
	return sb.String()
}
