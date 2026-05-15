package investigate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// slugRE matches one-or-more characters that are NOT (lowercase) ascii
// alphanumerics. Anything else is squashed to a single dash. Mirrors marvin's
// slugifyTopic regex, with one adjustment: marvin pre-lowercases the input
// before applying the regex, so we do the same.
var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// SlugifyTopic converts an arbitrary topic string into a filesystem-safe slug.
// Result is lowercase, ASCII-alphanumeric with single dashes, no leading or
// trailing dash, and no longer than 60 characters. Empty/non-mappable input
// returns "investigation" (entire's analog to marvin's "plan" fallback).
func SlugifyTopic(topic string) string {
	slug := slugRE.ReplaceAllString(strings.ToLower(topic), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = strings.TrimRight(slug[:60], "-")
	}
	if slug == "" {
		return "investigation"
	}
	return slug
}

// DeriveTopicFromSeed extracts a human-readable topic from a seed-doc body.
// Order of precedence:
//
//  1. The first line shaped like `# Investigation: <topic>` — the
//     scaffold's own title format. Round-trips a finished findings doc
//     cleanly.
//  2. The first markdown H1 (`# anything`) — covers prompt-doc seeds that
//     don't follow the scaffold but do have a title.
//  3. fallbackFilename without its extension — last-resort fallback so a
//     plain seed file still produces a readable topic.
func DeriveTopicFromSeed(body []byte, fallbackFilename string) string {
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "# Investigation:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(rest)
		}
	}
	base := filepath.Base(fallbackFilename)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// BootstrapInput carries the data needed to produce the initial findings
// doc on disk.
//
// Exactly one of SeedDoc / Topic / IssueLinkSeed must be set:
//   - SeedDoc:       the user passed a positional [seed-doc] path; copy
//     its bytes verbatim into the findings doc and derive
//     the topic from the body (or filename).
//   - Topic only:    the user passed --topic; render the scaffold.
//   - IssueLinkSeed: the user passed --issue-link; ResolveIssueLink
//     already produced a markdown body — use it as the
//     seed and use IssueLinkTopic as the topic.
type BootstrapInput struct {
	// SeedDoc is the absolute path to a user-provided seed file. Empty
	// when no seed was passed.
	SeedDoc string

	// Topic is the user-provided --topic value. Empty when not set.
	Topic string

	// IssueLinkSeed is the markdown bytes produced by ResolveIssueLink.
	// Empty when --issue-link was not used.
	IssueLinkSeed []byte

	// IssueLinkTopic is the topic derived from the resolved issue/PR
	// title. Used only when IssueLinkSeed is non-empty.
	IssueLinkTopic string

	// FindingsDoc is the absolute path the findings doc must be written
	// to.
	FindingsDoc string

	// PriorEntireContext, if non-empty, is rendered as a "## Prior
	// Entire Context" block in the topic-only scaffold. Ignored when a
	// seed doc is supplied (we never inject extra content into the
	// user's seed).
	PriorEntireContext string
}

// BootstrapResult reports what was produced.
type BootstrapResult struct {
	// Topic is the resolved topic — used downstream for slug derivation,
	// manifest entries, and prompt rendering.
	Topic string

	// FindingsDoc is the absolute path the findings doc was written to
	// (echoes BootstrapInput.FindingsDoc).
	FindingsDoc string
}

// Bootstrap writes the initial findings doc to disk.
//
// File-write semantics: the function creates parent directories as needed
// and writes the findings file unconditionally. Callers that want "skip
// if findings doc exists" semantics should stat the path themselves;
// Bootstrap is intentionally idempotent at the byte level (same input →
// same output) but does not protect existing files. This mirrors the role
// of "the loop driver gives me an empty doc to seed" — protecting an
// existing investigation belongs to a layer above this one.
func Bootstrap(ctx context.Context, in BootstrapInput) (BootstrapResult, error) {
	_ = ctx // Reserved for future use (e.g. cancellation during long renders).

	if in.FindingsDoc == "" {
		return BootstrapResult{}, errors.New("FindingsDoc is required")
	}

	var (
		topic string
		body  []byte
	)

	switch {
	case in.SeedDoc != "":
		seedBytes, err := os.ReadFile(in.SeedDoc)
		if err != nil {
			return BootstrapResult{}, fmt.Errorf("read seed doc: %w", err)
		}
		body = seedBytes
		topic = DeriveTopicFromSeed(seedBytes, in.SeedDoc)

	case len(in.IssueLinkSeed) > 0:
		body = in.IssueLinkSeed
		topic = in.IssueLinkTopic
		if topic == "" {
			topic = DeriveTopicFromSeed(in.IssueLinkSeed, in.FindingsDoc)
		}

	case in.Topic != "":
		topic = in.Topic
		body = []byte(renderInvestigationScaffold(in.Topic, time.Now().UTC().Format("2006-01-02"), in.PriorEntireContext))

	default:
		return BootstrapResult{}, errors.New("Bootstrap: one of SeedDoc, Topic, or IssueLinkSeed is required")
	}

	if err := os.MkdirAll(filepath.Dir(in.FindingsDoc), 0o750); err != nil {
		return BootstrapResult{}, fmt.Errorf("create findings dir: %w", err)
	}
	//nolint:gosec // FindingsDoc is a caller-provided path; the loop driver controls it.
	if err := os.WriteFile(in.FindingsDoc, body, 0o600); err != nil {
		return BootstrapResult{}, fmt.Errorf("write findings doc: %w", err)
	}

	return BootstrapResult{
		Topic:       topic,
		FindingsDoc: in.FindingsDoc,
	}, nil
}

// renderInvestigationScaffold returns the topic-only scaffold body.
//
// The doc is a living document the agents edit in place each turn — not a
// chronological log of attempts. It has exactly three content sections:
// Current understanding (the team's best answer right now), Supporting
// evidence (claims tied to concrete refs), and Disputed / unverified
// (open questions and unverified claims).
func renderInvestigationScaffold(topic, createdISODate, priorEntireContext string) string {
	priorSection := ""
	if strings.TrimSpace(priorEntireContext) != "" {
		priorSection = "## Prior Entire Context\n\n" + strings.TrimSpace(priorEntireContext) + "\n\n"
	}
	return fmt.Sprintf(`# Investigation: %s

**Status:** investigating
**Started:** %s

%s## Current understanding

<!-- The converged best answer to the question, in prose. Updated every
turn to reflect what the agents collectively believe right now. Hedge
with words like "likely" or "preliminary" until consensus; once agents
converge, state the answer directly and flip Status to "converged". -->

## Supporting evidence

<!-- Bullets, each tying a claim to concrete evidence. Format:
- <claim> — <file:line | command output | test result>
Add evidence as you verify it. Remove or correct evidence that turns
out to be wrong. Re-order so the most load-bearing items come first. -->

## Disputed / unverified

<!-- Anything an agent claims that another agent has not (or cannot)
verify, plus open questions that haven't been resolved. Each item
should explain WHY it's disputed or what evidence would resolve it.
Move items to Supporting evidence when verified; delete items that
are no longer relevant. -->
`, topic, createdISODate, priorSection)
}
