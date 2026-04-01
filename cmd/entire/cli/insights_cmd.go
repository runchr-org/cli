package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	checkpointid "github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/insights"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/summarize"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/spf13/cobra"
)

func newInsightsCmd() *cobra.Command {
	var last int
	var agent string
	var outputJSON bool

	cmd := &cobra.Command{
		Use:   "insights",
		Short: "Show session quality scores, trends, and agent comparisons",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			if checkDisabledGuard(ctx, w) {
				return nil
			}

			if !settings.IsSummarizeEnabled(ctx) {
				fmt.Fprintln(w, "Summarization is required for insights. Enable it in .entire/settings.json:")
				fmt.Fprintln(w, `  { "strategy_options": { "summarize": { "enabled": true } } }`)
				return nil
			}

			return runInsights(ctx, w, last, agent, outputJSON)
		},
	}

	cmd.Flags().IntVar(&last, "last", 10, "number of recent sessions to analyze")
	cmd.Flags().StringVar(&agent, "agent", "", "filter by agent name (e.g. \"Claude Code\")")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "output as JSON instead of styled terminal output")

	return cmd
}

// runInsights fetches session data from the SQLite cache, refreshing it if stale,
// then computes quality scores and renders output.
func runInsights(ctx context.Context, w io.Writer, last int, agentFilter string, outputJSON bool) error {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return fmt.Errorf("not in a git repository: %w", err)
	}
	entireDir := filepath.Join(worktreeRoot, paths.EntireDir)

	idb, err := insightsdb.Open(filepath.Join(entireDir, "insights.db"))
	if err != nil {
		return fmt.Errorf("open insights cache: %w", err)
	}
	defer func() { _ = idb.Close() }()

	// Non-fatal: continue with whatever is in the cache.
	// If the cache is empty the command will show an empty report.
	refreshCacheIfStale(ctx, idb) //nolint:errcheck,gosec // Non-fatal; continue with stale cache

	// Generate summaries for recent sessions that lack them.
	backfillSummaries(ctx, w, idb, last)
	backfillFacets(ctx, w, idb, last)

	var rows []insightsdb.SessionRow
	if agentFilter != "" {
		rows, err = idb.QueryByAgent(ctx, agentFilter, last)
	} else {
		rows, err = idb.QueryLastNSessions(ctx, last)
	}
	if err != nil {
		return fmt.Errorf("query sessions: %w", err)
	}

	scores := sessionRowsToScores(rows)
	trends := insights.ComputeTrends(scores)
	comparisons := insights.ComputeAgentComparisons(scores)

	period := fmt.Sprintf("last %d sessions", last)
	if agentFilter != "" {
		period = fmt.Sprintf("last %d sessions for %s", last, agentFilter)
	}

	report := insights.Report{
		GeneratedAt:  time.Now(),
		Period:       period,
		Sessions:     scores,
		Trends:       trends,
		Comparisons:  comparisons,
		SessionCount: len(scores),
	}

	if outputJSON {
		return renderInsightsJSON(w, report)
	}
	renderInsightsTerminal(w, report)
	return nil
}

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

// backfillSummaries generates summaries for the last N sessions that lack them.
// It reads transcripts from the checkpoint store, calls Claude to summarize,
// and updates the cache. Errors on individual sessions are logged and skipped.
func backfillSummaries(ctx context.Context, w io.Writer, idb *insightsdb.InsightsDB, lastN int) {
	rows, err := idb.QueryLastNSessions(ctx, lastN)
	if err != nil {
		logging.Warn(ctx, "backfillSummaries: query failed", "error", err)
		return
	}

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
			logging.Debug(ctx, "backfillSummaries: invalid checkpoint ID",
				"checkpoint_id", row.CheckpointID, "error", parseErr)
			skipped++
			continue
		}

		content, readErr := store.ReadSessionContent(ctx, cpID, row.SessionIndex)
		if readErr != nil {
			logging.Debug(ctx, "backfillSummaries: read session content failed",
				"checkpoint_id", row.CheckpointID, "session_index", row.SessionIndex, "error", readErr)
			skipped++
			continue
		}
		if len(content.Transcript) == 0 {
			logging.Debug(ctx, "backfillSummaries: empty transcript",
				"checkpoint_id", row.CheckpointID, "session_index", row.SessionIndex)
			skipped++
			continue
		}

		condensed, buildErr := summarize.BuildCondensedTranscriptFromBytes(content.Transcript, content.Metadata.Agent)
		if buildErr != nil {
			logging.Debug(ctx, "backfillSummaries: condense transcript failed",
				"checkpoint_id", row.CheckpointID, "agent", content.Metadata.Agent, "error", buildErr)
			skipped++
			continue
		}
		if len(condensed) == 0 {
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
			logging.Debug(ctx, "backfillSummaries: generate summary failed",
				"checkpoint_id", row.CheckpointID, "error", genErr)
			skipped++
			continue
		}
		if summary == nil {
			logging.Debug(ctx, "backfillSummaries: nil summary returned",
				"checkpoint_id", row.CheckpointID)
			skipped++
			continue
		}

		// Rebuild the row with summary data.
		content.Metadata.Summary = summary
		updated := metadataToSessionRow(row.CheckpointID, row.SessionIndex, &content.Metadata)

		if updateErr := idb.UpdateSessionSummary(ctx, updated); updateErr != nil {
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
			parts = append(parts, fmt.Sprintf("skipped %d (set ENTIRE_LOG_LEVEL=DEBUG for details)", skipped))
		}
		fmt.Fprintf(w, "  %s\n\n", strings.Join(parts, ", "))
	}
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

// sessionRowsToScores converts database rows into insights.SessionScore values.
func sessionRowsToScores(rows []insightsdb.SessionRow) []insights.SessionScore {
	scores := make([]insights.SessionScore, 0, len(rows))
	for _, r := range rows {
		scores = append(scores, insights.SessionScore{
			CheckpointID: r.CheckpointID,
			SessionID:    r.SessionID,
			Agent:        types.AgentType(r.Agent),
			Model:        r.Model,
			CreatedAt:    r.CreatedAt,
			Overall:      r.OverallScore,
			Breakdown: insights.ScoreBreakdown{
				TokenEfficiency:  r.ScoreTokenEff,
				FirstPassSuccess: r.ScoreFirstPass,
				FrictionScore:    r.ScoreFriction,
				FocusScore:       r.ScoreFocus,
			},
			TokensUsed:    r.TotalTokens,
			TurnCount:     r.TurnCount,
			FilesCount:    len(r.FilesTouched),
			FrictionCount: len(r.Friction),
			HasSummary:    r.HasSummary,
			ToolCallCount: totalToolCalls(r.ToolCounts),
			TopTools:      topToolNames(r.ToolCounts, 3),
			SkillsUsed:    skillNames(r.ToolCounts),
		})
	}
	return scores
}

func totalToolCalls(counts map[string]int) int {
	total := 0
	for _, c := range counts {
		total += c
	}
	return total
}

func topToolNames(counts map[string]int, n int) []string {
	if len(counts) == 0 {
		return nil
	}
	type kv struct {
		name  string
		count int
	}
	sorted := make([]kv, 0, len(counts))
	for k, v := range counts {
		if k == "Skill" {
			continue // skills shown separately
		}
		sorted = append(sorted, kv{k, v})
	}
	// Simple selection sort for small N.
	for i := range min(n, len(sorted)) {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	limit := min(n, len(sorted))
	names := make([]string, limit)
	for i := range limit {
		names[i] = sorted[i].name
	}
	return names
}

func skillNames(counts map[string]int) []string {
	// Skill tool calls are counted under "Skill" in tool counts.
	// We can't distinguish individual skill names from counts alone,
	// so we just indicate whether skills were used.
	if counts["Skill"] > 0 {
		return []string{fmt.Sprintf("%d invocations", counts["Skill"])}
	}
	return nil
}

// renderInsightsJSON marshals the report to JSON and writes it to w.
func renderInsightsJSON(w io.Writer, report insights.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("marshal insights report: %w", err)
	}
	return nil
}

// renderInsightsTerminal writes a styled terminal view of the insights report.
func renderInsightsTerminal(w io.Writer, report insights.Report) {
	s := termstyle.New(w)

	fmt.Fprintln(w, s.Render(s.Bold, "Entire Insights"))
	fmt.Fprintf(w, "Period: %s\n\n", report.Period)

	// Session Scores section.
	fmt.Fprintln(w, s.SectionRule("Session Scores"))
	if len(report.Sessions) == 0 {
		fmt.Fprintln(w, "  No sessions found.")
	}
	for _, ss := range report.Sessions {
		shortID := ss.SessionID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		scoreLine := fmt.Sprintf("  %5.1f  %s  %s",
			ss.Overall,
			string(ss.Agent),
			shortID,
		)
		fmt.Fprintln(w, s.Render(s.Bold, scoreLine))

		breakdownLine := fmt.Sprintf("  Token Efficiency: %.0f  First-Pass: %.0f  Friction: %.0f  Focus: %.0f",
			ss.Breakdown.TokenEfficiency,
			ss.Breakdown.FirstPassSuccess,
			ss.Breakdown.FrictionScore,
			ss.Breakdown.FocusScore,
		)
		fmt.Fprintln(w, s.Render(s.Dim, breakdownLine))

		statsLine := fmt.Sprintf("  %s tokens  %d turns  %d files  %d friction",
			termstyle.FormatTokenCount(ss.TokensUsed),
			ss.TurnCount,
			ss.FilesCount,
			ss.FrictionCount,
		)
		if !ss.HasSummary {
			statsLine += "  (no summary)"
		}
		fmt.Fprintln(w, s.Render(s.Gray, statsLine))

		if len(ss.TopTools) > 0 {
			toolsLine := "  Tools: " + strings.Join(ss.TopTools, ", ")
			if len(ss.SkillsUsed) > 0 {
				toolsLine += "  Skills: " + strings.Join(ss.SkillsUsed, ", ")
			}
			fmt.Fprintln(w, s.Render(s.Gray, toolsLine))
		}
		fmt.Fprintln(w)
	}

	// Trends section.
	fmt.Fprintln(w, s.SectionRule("Trends"))
	for _, t := range report.Trends {
		arrow := "→"
		style := s.Gray
		dirLabel := "stable"
		switch t.Direction {
		case "improving":
			arrow = "↑"
			style = s.Green
			dirLabel = fmt.Sprintf("+%.1f%%", t.ChangePercent)
		case "declining":
			arrow = "↓"
			style = s.Red
			dirLabel = fmt.Sprintf("-%.1f%%", t.ChangePercent)
		}
		line := fmt.Sprintf("  %s %s (%s)", arrow, t.Metric, dirLabel)
		fmt.Fprintln(w, s.Render(style, line))
	}
	fmt.Fprintln(w)

	// Agent Comparison section.
	fmt.Fprintln(w, s.SectionRule("Agent Comparison"))
	if len(report.Comparisons) == 0 {
		fmt.Fprintln(w, "  Not enough data for comparison.")
	}
	for _, ac := range report.Comparisons {
		headerLine := fmt.Sprintf("  %5.1f  %s (%d sessions)",
			ac.AvgScore,
			string(ac.Agent),
			ac.SessionCount,
		)
		fmt.Fprintln(w, s.Render(s.Bold, headerLine))

		statsLine := fmt.Sprintf("  avg %s tokens  %.1f turns  %.1f friction",
			termstyle.FormatTokenCount(ac.AvgTokens),
			ac.AvgTurns,
			ac.AvgFriction,
		)
		fmt.Fprintln(w, s.Render(s.Gray, statsLine))

		if len(ac.TopTools) > 0 {
			toolLine := fmt.Sprintf("  Top tools: %s | Avg %.0f tool calls/session",
				strings.Join(ac.TopTools, ", "), ac.AvgToolCalls)
			fmt.Fprintln(w, s.Render(s.Gray, toolLine))
		}
		if ac.TopStrength != "" {
			fmt.Fprintln(w, s.Render(s.Green, "  + "+ac.TopStrength))
		}
		if ac.TopWeakness != "" {
			fmt.Fprintln(w, s.Render(s.Red, "  - "+ac.TopWeakness))
		}
		fmt.Fprintln(w)
	}
}
