package investigate

import (
	"fmt"
	"strings"
)

// Files holds the absolute paths to the documents shared across an
// investigation run.
type Files struct {
	// Findings is the absolute path to the findings document the agent
	// reads, edits, and adds evidence to.
	Findings string
	// State is the absolute path to the run's state.json file. The agent
	// records its stance there via the `pending_turn` field.
	State string
}

// ComposeInput is the per-turn data needed to render an investigate prompt.
//
// The struct is intentionally kept narrow: the loop driver passes only what
// the prompt template uses. Marvin's prompt also surfaces prior decisions,
// claims, and fixes from its memory store; entire does not have an
// equivalent surface yet, so callers may pass arbitrary text via
// PriorContext (e.g. checkpoint search excerpts) for rendering.
type ComposeInput struct {
	// Topic is the human-readable subject of the investigation. Used in
	// the body of the prompt as plain text — never as a section heading,
	// since the rendered findings doc owns that.
	Topic string

	// AgentName is the agent the prompt is being rendered for (e.g.
	// "claude-code").
	AgentName string

	// Round is the 1-indexed round number in the loop.
	Round int

	// Turn is the 1-indexed overall turn number across rounds.
	Turn int

	// AlwaysPrompt, if non-empty, is appended verbatim at the end of the
	// rendered prompt. Mirrors ReviewConfig.Prompt so users can inject
	// project-specific guardrails into every turn via settings.
	AlwaysPrompt string

	// Files holds the findings + state absolute paths the agent must
	// read and edit.
	Files Files

	// PriorContext, if non-empty, is rendered as a "## Prior context"
	// block ahead of the main task instructions. Useful for surfacing
	// checkpoint excerpts, search hits, or other historical context that
	// is run-specific rather than baked into the prompt template.
	PriorContext string
}

// ComposeInvestigatePrompt renders the full prompt sent to one agent for one
// turn of an investigate run.
//
// The findings doc is a collaborative living document — a single converged
// answer to the topic that each agent reviews, verifies, and edits in
// place. It is NOT a chronological log of per-agent attempts. The agent
// records its stance by writing the `pending_turn` field of Files.State to
// a JSON object of the form
// {"stance":"approve|request-changes|abstain","note":"<one-line>"}.
// The agent must not modify any other field of state.json.
func ComposeInvestigatePrompt(in ComposeInput) string {
	var b strings.Builder

	if pc := strings.TrimSpace(in.PriorContext); pc != "" {
		b.WriteString("## Prior context\n\n")
		b.WriteString(pc)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, `You are agent: %s, working on a collaborative multi-agent investigation.

Topic: %s
Files:
  Findings: %s
  State:    %s

This is round %d, turn %d overall. The findings doc represents the
investigation team's current best answer to the topic. Your job this
turn is to review it, verify what you can, and edit anything inaccurate
so the final version is the most accurate it can be.

## Your task

1. Read the findings doc in full. Read it as if you'd never seen the
   topic before — what claims sound shaky, lack evidence, or could be
   wrong? You are a skeptical investigator: verify or push back.

2. Investigate. Read files, run git/grep, run tests. You have full agent
   powers EXCEPT: do not modify any file other than the findings doc and
   the run's state.json.

3. Edit the findings doc in place:
   - Update "Current understanding" so it reflects the team's best
     answer right now. Hedge with "likely" / "preliminary" until you
     are confident; flip Status to "converged" only when you would
     stake your reputation on the answer.
   - Add to "Supporting evidence" any claim you have verified — with
     concrete refs (file:line, command output, test result).
   - Correct or remove evidence that is wrong or stale.
   - Add to "Disputed / unverified" anything you suspect but cannot
     confirm, or anything a prior agent claimed that doesn't hold up
     to your review.
   - Move items between sections as confidence changes. Edit
     inaccuracies. Correct prior agents' wording when it misleads.
   - Do not attribute changes to yourself or any agent. The doc is
     collective; provenance lives in git + session transcripts, not in
     the doc.

4. Report your stance by setting ONLY the `+"`pending_turn`"+` field of
   state.json at:

     %s

   to a JSON object of the form

     {"stance": "approve" | "request-changes" | "abstain", "note": "<one-line explanation>"}

   Do NOT modify any other field of state.json — the loop owns
   everything else.

## Stance rules

   - "approve" — the doc is correct and complete; you'd ship this answer.
   - "request-changes" — claims remain unverified, hypotheses unexplored,
     or evidence is missing.
   - "abstain" — you cannot form an opinion (e.g. insufficient context,
     out-of-scope expertise) — explain why so the next agent can address
     the gap.

Do NOT commit anything to git. Do NOT run destructive commands. Exit
once you've written your `+"`pending_turn`"+` to state.json.
`,
		in.AgentName,
		in.Topic,
		in.Files.Findings,
		in.Files.State,
		in.Round, in.Turn,
		in.Files.State,
	)

	if ap := strings.TrimSpace(in.AlwaysPrompt); ap != "" {
		b.WriteString("\n")
		b.WriteString(ap)
		b.WriteString("\n")
	}

	return b.String()
}
