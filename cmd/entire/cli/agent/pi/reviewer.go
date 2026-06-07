package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/entireio/cli/cmd/entire/cli/review"
	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// piReviewMaxScannerBuf is the bufio.Scanner cap for the Pi review parser.
// 64MB matches the claude-code and codex parsers so all three tolerate the
// same worst-case line length (a tool result packed into one JSON envelope).
const piReviewMaxScannerBuf = 64 * 1024 * 1024

// NewReviewer returns the AgentReviewer for the Pi coding agent.
//
// Argv shape: pi --mode json <prompt> [--model <pattern>]. The prompt is a
// positional argument; stdin is unused. Stdout is newline-delimited JSON
// event envelopes (one event per line, see Pi's docs/json.md), which the
// parser decodes into the review Event stream.
//
// Pi spawned this way is a child process of `entire review`, so it inherits
// the ENTIRE_REVIEW_* env vars set by AppendReviewEnv. Pi's lifecycle hook
// then self-tags the session as a review via the shared adoptReviewEnv path —
// no marker file and no `entire attach` are needed.
func NewReviewer() *reviewtypes.ReviewerTemplate {
	return &reviewtypes.ReviewerTemplate{
		AgentName: "pi",
		BuildCmd:  buildReviewCmd,
		Parser:    parsePiOutput,
	}
}

// buildReviewCmd builds the exec.Cmd for a Pi review run.
// Exposed at package level for test inspection of argv and env.
func buildReviewCmd(ctx context.Context, cfg reviewtypes.RunConfig) *exec.Cmd {
	prompt := review.ComposeReviewPrompt(cfg)
	args := []string{"--mode", "json", prompt}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	cmd := exec.CommandContext(ctx, "pi", args...)
	cmd.Env = review.AppendReviewEnv(os.Environ(), "pi", cfg, prompt)
	return cmd
}

// piStreamEnvelope is one line of `pi --mode json` stdout. Only the fields the
// review parser consumes are declared; unknown fields and event types are
// ignored so new Pi event types don't break parsing.
type piStreamEnvelope struct {
	Type     string           `json:"type"`
	ToolName string           `json:"toolName"`
	Args     json.RawMessage  `json:"args"`
	Message  *piStreamMessage `json:"message"`
}

type piStreamMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *piStreamUsage  `json:"usage"`
}

// piStreamUsage mirrors pi-ai's Usage shape (token counts only).
type piStreamUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

type piStreamContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// parsePiOutput converts `pi --mode json` stdout into a stream of review
// Events. Each stdout line is one JSON envelope:
//   - {"type":"session",...}            session header; swallowed
//   - {"type":"agent_start"}            covered by the leading Started event
//   - {"type":"message_end","message":{role:"assistant",content:[...],usage}}
//     assistant text → AssistantText; usage tallied for the final Tokens event
//   - {"type":"tool_execution_start","toolName":..,"args":..} → ToolCall
//   - {"type":"agent_end",...}          marks a clean completion
//
// Emits Started first, Finished{Success:...} last. Success is true only when
// an agent_end envelope was observed. On a scanner error (torn stream) it
// emits RunError then Finished{Success:false}.
//
// Token math: Pi reports per-message usage on each assistant message. Output
// tokens are summed across messages; input is taken from the last assistant
// message (its context already includes prior turns, so summing input would
// double-count). These are advisory live totals — the authoritative figures
// are hydrated from the persisted transcript after the run.
func parsePiOutput(r io.Reader) <-chan reviewtypes.Event {
	return parsePiOutputBuf(r, piReviewMaxScannerBuf)
}

// parsePiOutputBuf is the parameterized variant of parsePiOutput, used by
// tests to shrink the scanner cap so the "token too long" branch can be
// exercised without writing 64MB of fixture data.
func parsePiOutputBuf(r io.Reader, maxBuf int) <-chan reviewtypes.Event {
	out := make(chan reviewtypes.Event, 32)
	go func() {
		defer close(out)
		out <- reviewtypes.Started{}
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, min(1024*1024, maxBuf)), maxBuf)
		var sawEnd bool
		var sumOut, lastIn int
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var env piStreamEnvelope
			if err := json.Unmarshal(line, &env); err != nil {
				out <- reviewtypes.RunError{Err: fmt.Errorf("pi json stream: %w", err)}
				continue
			}
			switch env.Type {
			case "message_end":
				if env.Message == nil || env.Message.Role != "assistant" {
					continue
				}
				for _, text := range assistantTextBlocks(env.Message.Content) {
					if text != "" {
						out <- reviewtypes.AssistantText{Text: text}
					}
				}
				if u := env.Message.Usage; u != nil {
					sumOut += u.Output
					lastIn = u.Input + u.CacheRead
				}
			case "tool_execution_start":
				out <- reviewtypes.ToolCall{Name: env.ToolName, Args: string(env.Args)}
			case "agent_end":
				sawEnd = true
			}
		}
		if err := scanner.Err(); err != nil {
			out <- reviewtypes.RunError{Err: fmt.Errorf("read stdout: %w", err)}
			out <- reviewtypes.Finished{Success: false}
			return
		}
		out <- reviewtypes.Tokens{In: lastIn, Out: sumOut}
		out <- reviewtypes.Finished{Success: sawEnd}
	}()
	return out
}

// assistantTextBlocks extracts the text from a Pi assistant message's content,
// which is normally an array of content blocks but may be a plain JSON string.
// Non-text blocks (tool calls, etc.) are ignored — tool activity is surfaced
// via the dedicated tool_execution_start envelope instead.
func assistantTextBlocks(content json.RawMessage) []string {
	if len(content) == 0 {
		return nil
	}
	var blocks []piStreamContentBlock
	if err := json.Unmarshal(content, &blocks); err == nil {
		var texts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		return texts
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil && s != "" {
		return []string{s}
	}
	return nil
}
