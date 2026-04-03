package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	checkpointid "github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/improve"
	"github.com/entireio/cli/cmd/entire/cli/insights"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/summarize"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
	"github.com/go-git/go-git/v6/plumbing"
)

// refreshCacheIfStale checks whether the insights cache is up-to-date with the
// entire/checkpoints/v1 branch and rebuilds it if not.
func refreshCacheIfStale(ctx context.Context, idb *insightsdb.InsightsDB) error {
	repo, err := openRepository(ctx)
	if err != nil {
		return fmt.Errorf("open git repository: %w", err)
	}

	// Resolve the current tip of entire/checkpoints/v1.
	refName := plumbing.NewBranchReferenceName(paths.MetadataBranchName)
	ref, resolveErr := repo.Reference(refName, true)
	if resolveErr != nil {
		// Branch doesn't exist yet — nothing to cache.
		return nil //nolint:nilerr // Missing branch is expected, not an error
	}
	currentTip := ref.Hash().String()

	cachedTip, err := idb.GetBranchTip(ctx)
	if err != nil {
		return fmt.Errorf("get cached branch tip: %w", err)
	}

	if cachedTip == currentTip {
		return nil // Cache is up-to-date.
	}

	// Cache is stale — rebuild from git.
	store := checkpoint.NewGitStore(repo)
	committedList, err := store.ListCommitted(ctx)
	if err != nil {
		return fmt.Errorf("list committed checkpoints: %w", err)
	}

	for _, info := range committedList {
		cpIDStr := info.CheckpointID.String()

		// Check whether we already have this checkpoint cached.
		has, hasErr := idb.HasCheckpoint(ctx, cpIDStr)
		if hasErr != nil {
			return fmt.Errorf("check checkpoint %s: %w", cpIDStr, hasErr)
		}
		if has {
			continue
		}

		// Read the checkpoint summary to find how many sessions it has.
		summary, readErr := store.ReadCommitted(ctx, info.CheckpointID)
		if readErr != nil {
			continue // Skip unreadable checkpoints; don't abort the whole refresh.
		}

		for i := range summary.Sessions {
			content, contentErr := store.ReadSessionContent(ctx, info.CheckpointID, i)
			if contentErr != nil {
				continue
			}
			row := metadataToSessionRow(cpIDStr, i, &content.Metadata)
			row.ToolCounts = extractToolCounts(content.Transcript, content.Metadata.Agent)
			if insertErr := idb.InsertSession(ctx, row); insertErr != nil {
				return fmt.Errorf("insert session %s/%d: %w", cpIDStr, i, insertErr)
			}
		}
	}

	if err := idb.SetBranchTip(ctx, currentTip); err != nil {
		return fmt.Errorf("set branch tip: %w", err)
	}
	return nil
}

// extractToolCounts parses a transcript and counts tool invocations by name.
// Returns nil if the transcript can't be parsed.
func extractToolCounts(transcript []byte, agentType types.AgentType) map[string]int {
	entries, err := summarize.BuildCondensedTranscriptFromBytes(transcript, agentType)
	if err != nil || len(entries) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, e := range entries {
		if e.Type == summarize.EntryTypeTool && e.ToolName != "" {
			counts[e.ToolName]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

// metadataToSessionRow converts CommittedMetadata into an insightsdb.SessionRow,
// computing quality scores where summary data is available.
func metadataToSessionRow(cpID string, sessionIndex int, meta *checkpoint.CommittedMetadata) insightsdb.SessionRow {
	row := insightsdb.SessionRow{
		CheckpointID: cpID,
		SessionID:    meta.SessionID,
		SessionIndex: sessionIndex,
		Agent:        string(meta.Agent),
		Model:        meta.Model,
		Branch:       meta.Branch,
		OwnerName:    meta.OwnerName,
		OwnerID:      meta.OwnerID,
		OwnerEmail:   meta.OwnerEmail,
		CreatedAt:    meta.CreatedAt,
	}

	if meta.TokenUsage != nil {
		row.InputTokens = meta.TokenUsage.InputTokens + meta.TokenUsage.CacheCreationTokens + meta.TokenUsage.CacheReadTokens
		row.OutputTokens = meta.TokenUsage.OutputTokens
		row.TotalTokens = termstyle.TotalTokens(meta.TokenUsage)
		row.APICallCount = meta.TokenUsage.APICallCount
	}

	if meta.SessionMetrics != nil {
		row.DurationMs = meta.SessionMetrics.DurationMs
		row.TurnCount = meta.SessionMetrics.TurnCount
	}

	if meta.Summary != nil {
		row.HasSummary = true
		row.Intent = meta.Summary.Intent
		row.Outcome = meta.Summary.Outcome
		row.Friction = meta.Summary.Friction
		row.ImplementationRationale = meta.Summary.ImplementationRationale
		row.Tradeoffs = meta.Summary.Tradeoffs
		row.CodebasePatterns = meta.Summary.CodebasePatterns

		for _, l := range meta.Summary.Learnings.Repo {
			row.Learnings = append(row.Learnings, insightsdb.LearningRow{Scope: "repo", Finding: l})
		}
		for _, l := range meta.Summary.Learnings.Workflow {
			row.Learnings = append(row.Learnings, insightsdb.LearningRow{Scope: "workflow", Finding: l})
		}
		for _, l := range meta.Summary.Learnings.Code {
			row.Learnings = append(row.Learnings, insightsdb.LearningRow{Scope: "code", Finding: l.Finding, Path: l.Path})
		}
	}

	// Always compute scores — token efficiency and focus work without summaries.
	// Friction/first-pass default to neutral when no summary exists.
	data := insights.SessionData{
		TotalTokens:   row.TotalTokens,
		FilesCount:    len(meta.FilesTouched),
		FrictionCount: len(row.Friction),
		TurnCount:     row.TurnCount,
		HasSummary:    row.HasSummary,
	}
	if meta.Summary != nil {
		data.OpenItemCount = len(meta.Summary.OpenItems)
	}
	breakdown := insights.ScoreSession(data)
	row.OverallScore = insights.ComputeOverall(breakdown)
	row.ScoreTokenEff = breakdown.TokenEfficiency
	row.ScoreFirstPass = breakdown.FirstPassSuccess
	row.ScoreFriction = breakdown.FrictionScore
	row.ScoreFocus = breakdown.FocusScore

	row.FilesTouched = meta.FilesTouched
	return row
}

func backfillSummariesForRows(ctx context.Context, w io.Writer, idb *insightsdb.InsightsDB, rows []insightsdb.SessionRow, debug bool, debugHint string) {
	// Partition sessions into cached vs needing summaries.
	var unsummarized []insightsdb.SessionRow
	cached := 0
	for _, r := range rows {
		if r.HasSummary {
			cached++
		} else {
			unsummarized = append(unsummarized, r)
		}
	}

	s := termstyle.New(w)
	if len(unsummarized) == 0 {
		fmt.Fprintf(w, "%s %d of %d sessions already have summaries\n",
			s.Render(s.Dim, "i"), cached, len(rows))
		return
	}

	fmt.Fprintf(w, "%s Generating summaries for %d sessions (%d already cached)...\n",
		s.Render(s.Dim, "i"), len(unsummarized), cached)

	repo, err := openRepository(ctx)
	if err != nil {
		logging.Warn(ctx, "backfillSummaries: open repository failed", "error", err)
		return
	}
	store := checkpoint.NewGitStore(repo)
	gen := &summarize.ClaudeGenerator{}

	generated := 0
	skipped := 0

	for i, row := range unsummarized {
		cpID, parseErr := checkpointid.NewCheckpointID(row.CheckpointID)
		if parseErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: invalid checkpoint ID %q: %v\n", row.CheckpointID, parseErr)
			}
			logging.Debug(ctx, "backfillSummaries: invalid checkpoint ID",
				"checkpoint_id", row.CheckpointID, "error", parseErr)
			skipped++
			continue
		}

		content, readErr := store.ReadSessionContent(ctx, cpID, row.SessionIndex)
		if readErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: read session content failed for %s[%d]: %v\n", row.CheckpointID, row.SessionIndex, readErr)
			}
			logging.Debug(ctx, "backfillSummaries: read session content failed",
				"checkpoint_id", row.CheckpointID, "session_index", row.SessionIndex, "error", readErr)
			skipped++
			continue
		}
		if len(content.Transcript) == 0 {
			if debug {
				fmt.Fprintf(w, "    debug: empty transcript for %s[%d]\n", row.CheckpointID, row.SessionIndex)
			}
			logging.Debug(ctx, "backfillSummaries: empty transcript",
				"checkpoint_id", row.CheckpointID, "session_index", row.SessionIndex)
			skipped++
			continue
		}

		condensed, buildErr := summarize.BuildCondensedTranscriptFromBytes(content.Transcript, content.Metadata.Agent)
		if buildErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: condense transcript failed for %s (%s): %v\n", row.CheckpointID, content.Metadata.Agent, buildErr)
			}
			logging.Debug(ctx, "backfillSummaries: condense transcript failed",
				"checkpoint_id", row.CheckpointID, "agent", content.Metadata.Agent, "error", buildErr)
			skipped++
			continue
		}
		if len(condensed) == 0 {
			if debug {
				fmt.Fprintf(w, "    debug: condensed transcript empty for %s (%s)\n", row.CheckpointID, content.Metadata.Agent)
			}
			logging.Debug(ctx, "backfillSummaries: condensed transcript empty",
				"checkpoint_id", row.CheckpointID, "agent", content.Metadata.Agent)
			skipped++
			continue
		}

		input := summarize.Input{
			Transcript:   condensed,
			FilesTouched: row.FilesTouched,
		}
		summary, genErr := gen.Generate(ctx, input)
		if genErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: generate summary failed for %s: %v\n", row.CheckpointID, genErr)
			}
			logging.Debug(ctx, "backfillSummaries: generate summary failed",
				"checkpoint_id", row.CheckpointID, "error", genErr)
			skipped++
			continue
		}
		if summary == nil {
			if debug {
				fmt.Fprintf(w, "    debug: nil summary returned for %s\n", row.CheckpointID)
			}
			logging.Debug(ctx, "backfillSummaries: nil summary returned",
				"checkpoint_id", row.CheckpointID)
			skipped++
			continue
		}

		// Rebuild the row with summary data.
		content.Metadata.Summary = summary
		updated := metadataToSessionRow(row.CheckpointID, row.SessionIndex, &content.Metadata)

		if updateErr := idb.UpdateSessionSummary(ctx, updated); updateErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: update session summary failed for %s: %v\n", row.CheckpointID, updateErr)
			}
			logging.Debug(ctx, "backfillSummaries: update session failed",
				"checkpoint_id", row.CheckpointID, "error", updateErr)
			skipped++
			continue
		}

		generated++
		fmt.Fprintf(w, "  %s %s (%d/%d)\n",
			s.Render(s.Green, "✓"), row.CheckpointID[:12], i+1, len(unsummarized))
	}

	if generated > 0 || skipped > 0 {
		parts := []string{fmt.Sprintf("Generated %d summaries", generated)}
		if skipped > 0 {
			switch {
			case debug:
				parts = append(parts, fmt.Sprintf("skipped %d", skipped))
			case debugHint != "":
				parts = append(parts, debugHint)
			default:
				parts = append(parts, fmt.Sprintf("skipped %d", skipped))
			}
		}
		fmt.Fprintf(w, "  %s\n\n", strings.Join(parts, ", "))
	}
}

func backfillFacetsForRows(ctx context.Context, w io.Writer, idb *insightsdb.InsightsDB, rows []insightsdb.SessionRow, debug bool, debugHint string) {
	// Partition sessions into cached vs needing facets.
	var needsFacets []insightsdb.SessionRow
	cached := 0
	for _, row := range rows {
		if row.HasFacets {
			cached++
		} else {
			needsFacets = append(needsFacets, row)
		}
	}

	s := termstyle.New(w)
	if len(needsFacets) == 0 {
		fmt.Fprintf(w, "%s %d of %d sessions already have facets\n",
			s.Render(s.Dim, "i"), cached, len(rows))
		return
	}

	fmt.Fprintf(w, "%s Extracting facets for %d sessions (%d already cached)...\n",
		s.Render(s.Dim, "i"), len(needsFacets), cached)

	repo, err := openRepository(ctx)
	if err != nil {
		logging.Warn(ctx, "backfillFacets: open repository failed", "error", err)
		return
	}

	store := checkpoint.NewGitStore(repo)
	extractor := &facets.Extractor{Runner: &llmcli.Runner{}}

	extracted := 0
	skipped := 0

	for i, row := range needsFacets {
		cpID, parseErr := checkpointid.NewCheckpointID(row.CheckpointID)
		if parseErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: invalid checkpoint ID %q: %v\n", row.CheckpointID, parseErr)
			}
			logging.Debug(ctx, "backfillFacets: invalid checkpoint ID",
				"checkpoint_id", row.CheckpointID, "error", parseErr)
			skipped++
			continue
		}

		content, readErr := store.ReadSessionContent(ctx, cpID, row.SessionIndex)
		if readErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: read session content failed for %s[%d]: %v\n", row.CheckpointID, row.SessionIndex, readErr)
			}
			logging.Debug(ctx, "backfillFacets: read session content failed",
				"checkpoint_id", row.CheckpointID, "session_index", row.SessionIndex, "error", readErr)
			skipped++
			continue
		}
		if len(content.Transcript) == 0 {
			if debug {
				fmt.Fprintf(w, "    debug: empty transcript for %s[%d]\n", row.CheckpointID, row.SessionIndex)
			}
			logging.Debug(ctx, "backfillFacets: empty transcript",
				"checkpoint_id", row.CheckpointID, "session_index", row.SessionIndex)
			skipped++
			continue
		}

		condensed, buildErr := summarize.BuildCondensedTranscriptFromBytes(content.Transcript, content.Metadata.Agent)
		if buildErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: condense transcript failed for %s: %v\n", row.CheckpointID, buildErr)
			}
			logging.Debug(ctx, "backfillFacets: condense transcript failed",
				"checkpoint_id", row.CheckpointID, "error", buildErr)
			skipped++
			continue
		}
		if len(condensed) == 0 {
			if debug {
				fmt.Fprintf(w, "    debug: condensed transcript empty for %s\n", row.CheckpointID)
			}
			logging.Debug(ctx, "backfillFacets: condensed transcript empty",
				"checkpoint_id", row.CheckpointID)
			skipped++
			continue
		}

		formatted := summarize.FormatCondensedTranscript(summarize.Input{
			Transcript:   condensed,
			FilesTouched: row.FilesTouched,
		})

		facetResult, _, extractErr := extractor.Extract(ctx, formatted)
		if extractErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: extract facets failed for %s: %v\n", row.CheckpointID, extractErr)
			}
			logging.Debug(ctx, "backfillFacets: extract facets failed",
				"checkpoint_id", row.CheckpointID, "error", extractErr)
			skipped++
			continue
		}
		if facetResult == nil {
			if debug {
				fmt.Fprintf(w, "    debug: nil facets returned for %s\n", row.CheckpointID)
			}
			logging.Debug(ctx, "backfillFacets: nil facets returned",
				"checkpoint_id", row.CheckpointID)
			skipped++
			continue
		}

		row.Facets = *facetResult
		row.HasFacets = true
		if updateErr := idb.UpdateSessionFacets(ctx, row); updateErr != nil {
			if debug {
				fmt.Fprintf(w, "    debug: update session facets failed for %s: %v\n", row.CheckpointID, updateErr)
			}
			logging.Debug(ctx, "backfillFacets: update session failed",
				"checkpoint_id", row.CheckpointID, "error", updateErr)
			skipped++
			continue
		}

		extracted++
		fmt.Fprintf(w, "  %s %s (%d/%d)\n",
			s.Render(s.Green, "✓"), row.CheckpointID[:12], i+1, len(needsFacets))
	}

	if extracted > 0 || skipped > 0 {
		parts := []string{fmt.Sprintf("Extracted %d facets", extracted)}
		if skipped > 0 {
			switch {
			case debug:
				parts = append(parts, fmt.Sprintf("skipped %d", skipped))
			case debugHint != "":
				parts = append(parts, debugHint)
			default:
				parts = append(parts, fmt.Sprintf("skipped %d", skipped))
			}
		}
		fmt.Fprintf(w, "  %s\n\n", strings.Join(parts, ", "))
	}
}

func sessionRowsToSummaries(rows []insightsdb.SessionRow) []improve.SessionSummaryData {
	summaries := make([]improve.SessionSummaryData, 0, len(rows))
	for _, r := range rows {
		s := improve.SessionSummaryData{
			CheckpointID: r.CheckpointID,
			Friction:     r.Friction,
			Facets:       r.Facets,
		}
		for _, l := range r.Learnings {
			s.Learnings = append(s.Learnings, improve.LearningEntry{
				Scope:   l.Scope,
				Finding: l.Finding,
				Path:    l.Path,
			})
		}
		summaries = append(summaries, s)
	}
	return summaries
}

func renderUsageLine(w io.Writer, usage *llmcli.UsageInfo) {
	s := termstyle.New(w)
	tokens := termstyle.FormatTokenCount(usage.InputTokens + usage.OutputTokens)
	line := fmt.Sprintf("\nCost: $%.4f (%s tokens)", usage.TotalCostUSD, tokens)
	fmt.Fprintln(w, s.Render(s.Dim, line))
}
