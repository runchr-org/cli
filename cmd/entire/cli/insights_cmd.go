package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/insights"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
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

		// Compute session quality scores.
		data := insights.SessionData{
			TotalTokens:   row.TotalTokens,
			FilesCount:    len(meta.FilesTouched),
			FrictionCount: len(meta.Summary.Friction),
			TurnCount:     row.TurnCount,
			OpenItemCount: len(meta.Summary.OpenItems),
			HasSummary:    true,
		}
		breakdown := insights.ScoreSession(data)
		row.OverallScore = insights.ComputeOverall(breakdown)
		row.ScoreTokenEff = breakdown.TokenEfficiency
		row.ScoreFirstPass = breakdown.FirstPassSuccess
		row.ScoreFriction = breakdown.FrictionScore
		row.ScoreFocus = breakdown.FocusScore
	}

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
		})
	}
	return scores
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
		fmt.Fprintln(w, s.Render(s.Gray, statsLine))
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

		if ac.TopStrength != "" {
			fmt.Fprintln(w, s.Render(s.Green, "  + "+ac.TopStrength))
		}
		if ac.TopWeakness != "" {
			fmt.Fprintln(w, s.Render(s.Red, "  - "+ac.TopWeakness))
		}
		fmt.Fprintln(w)
	}
}
