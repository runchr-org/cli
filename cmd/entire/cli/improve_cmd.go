package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	checkpointid "github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/improve"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/stringutil"
	"github.com/entireio/cli/cmd/entire/cli/summarize"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
	"github.com/spf13/cobra"
)

func newImproveCmd() *cobra.Command {
	var last int
	var dryRun bool
	var outputJSON bool

	cmd := &cobra.Command{
		Use:   "improve",
		Short: "Suggest improvements to project context files based on session patterns",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			if checkDisabledGuard(ctx, w) {
				return nil
			}

			if !settings.IsSummarizeEnabled(ctx) {
				fmt.Fprintln(w, "Summarization is required for improve. Enable it in .entire/settings.json:")
				fmt.Fprintln(w, `  { "strategy_options": { "summarize": { "enabled": true } } }`)
				return nil
			}

			return runImprove(ctx, w, last, dryRun, outputJSON)
		},
	}

	cmd.Flags().IntVar(&last, "last", 10, "number of recent sessions to analyze")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show friction patterns only, no AI call, no transcript read")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "output as JSON instead of styled terminal output")

	return cmd
}

// runImprove fetches session data from the SQLite cache, refreshes it if stale,
// then analyzes friction patterns and optionally generates context file improvements.
func runImprove(ctx context.Context, w io.Writer, last int, dryRun bool, outputJSON bool) error {
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
	refreshCacheIfStale(ctx, idb) //nolint:errcheck,gosec // Non-fatal; continue with stale cache

	// Generate summaries for recent sessions that lack them.
	if !dryRun {
		backfillSummaries(ctx, w, idb, last, false, "")
		backfillFacets(ctx, w, idb, last, false, "")
	}

	// Fetch the last N sessions for summary stats.
	rows, err := idb.QueryLastNSessions(ctx, last)
	if err != nil {
		return fmt.Errorf("query sessions: %w", err)
	}

	// Count total friction items across all sessions.
	frictionTotal := 0
	for _, r := range rows {
		frictionTotal += len(r.Friction)
	}

	// Use keyword-based theme classification for pattern detection.
	// This groups friction by theme (lint, api, conflict, etc.) instead of exact text match.
	summaries := sessionRowsToSummaries(rows)
	analysis := improve.AnalyzePatterns(summaries)

	if dryRun {
		if outputJSON {
			return renderImproveJSONDryRunThemes(w, analysis, len(rows), frictionTotal)
		}
		renderImproveTerminalDryRun(w, analysis, len(rows), frictionTotal)
		return nil
	}

	// Deep-read transcripts for top friction themes.
	if err = attachTranscriptExcerpts(ctx, idb, analysis.RepeatedFriction, worktreeRoot); err != nil {
		_ = err // Non-fatal: proceed without transcript excerpts.
	}

	// Detect context files.
	contextFiles := improve.DetectContextFiles(worktreeRoot)

	gen := improve.Generator{Runner: &llmcli.Runner{}}
	result, err := gen.Generate(ctx, analysis, contextFiles)
	if err != nil {
		return fmt.Errorf("generate suggestions: %w", err)
	}

	report := improve.ImprovementReport{
		ContextFiles:  contextFiles,
		Suggestions:   result.Suggestions,
		Facets:        facetSummary(analysis),
		FacetCounts:   facetCounts(analysis),
		SessionsUsed:  len(rows),
		FrictionTotal: frictionTotal,
		PatternsFound: analysisPatternCount(analysis),
	}

	if outputJSON {
		return renderImproveJSON(w, report)
	}
	renderImproveTerminal(w, report)
	if result.Usage != nil {
		renderUsageLine(w, result.Usage)
	}
	return nil
}

// attachTranscriptExcerpts fetches transcript excerpts for top friction patterns and
// attaches them in-place. Uses the pattern's AffectedSessions to find relevant checkpoints.
// Errors are non-fatal; unreadable sessions are silently skipped.
func attachTranscriptExcerpts(ctx context.Context, _ *insightsdb.InsightsDB, patterns []improve.FrictionPattern, _ string) error {
	repo, err := openRepository(ctx)
	if err != nil {
		return fmt.Errorf("open git repository: %w", err)
	}
	store := checkpoint.NewGitStore(repo)

	// Limit to top 3 friction themes.
	limit := 3
	if len(patterns) < limit {
		limit = len(patterns)
	}

	for i := range patterns[:limit] {
		cpIDs := patterns[i].AffectedSessions

		// Limit to top 2 sessions per theme.
		sessionLimit := 2
		if len(cpIDs) < sessionLimit {
			sessionLimit = len(cpIDs)
		}

		var excerpts []string
		for _, cpIDStr := range cpIDs[:sessionLimit] {
			cpID, parseErr := checkpointid.NewCheckpointID(cpIDStr)
			if parseErr != nil {
				continue
			}

			content, readErr := store.ReadSessionContent(ctx, cpID, 0)
			if readErr != nil {
				continue
			}

			condensed, buildErr := summarize.BuildCondensedTranscriptFromBytes(content.Transcript, content.Metadata.Agent)
			if buildErr != nil || len(condensed) == 0 {
				continue
			}

			formatted := summarize.FormatCondensedTranscript(summarize.Input{Transcript: condensed})
			excerpt := truncateString(formatted, 2000)
			if excerpt != "" {
				excerpts = append(excerpts, excerpt)
			}
		}

		if len(excerpts) > 0 {
			patterns[i].TranscriptExcerpt = strings.Join(excerpts, "\n---\n")
		}
	}

	return nil
}

// sessionRowsToSummaries converts insightsdb rows into improve.SessionSummaryData values.
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

// truncateString truncates s to at most maxLen runes, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	return stringutil.TruncateRunes(s, maxLen, "...")
}

// renderImproveJSON marshals the full report to JSON and writes it to w.
func renderImproveJSON(w io.Writer, report improve.ImprovementReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("marshal improve report: %w", err)
	}
	return nil
}

// renderImproveJSONDryRunThemes marshals the dry-run theme-grouped friction data to JSON.
func renderImproveJSONDryRunThemes(w io.Writer, analysis improve.PatternAnalysis, sessionCount, frictionTotal int) error {
	type themeJSON struct {
		Theme    string   `json:"theme"`
		Count    int      `json:"count"`
		Examples []string `json:"examples"`
		Sessions []string `json:"sessions"`
	}
	type dryRunReport struct {
		SessionsAnalyzed int         `json:"sessions_analyzed"`
		FrictionTotal    int         `json:"friction_total"`
		RecurringThemes  []themeJSON `json:"recurring_themes"`
	}
	themes := make([]themeJSON, 0, len(analysis.RepeatedFriction))
	for _, p := range analysis.RepeatedFriction {
		themes = append(themes, themeJSON{
			Theme:    p.Theme,
			Count:    p.Count,
			Examples: p.Examples,
			Sessions: p.AffectedSessions,
		})
	}
	report := dryRunReport{
		SessionsAnalyzed: sessionCount,
		FrictionTotal:    frictionTotal,
		RecurringThemes:  themes,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("marshal dry-run report: %w", err)
	}
	return nil
}

// renderImproveTerminal writes a styled terminal view of the improvement report.
func renderImproveTerminal(w io.Writer, report improve.ImprovementReport) {
	s := termstyle.New(w)

	fmt.Fprintln(w, s.Render(s.Bold, "Entire Improve"))
	fmt.Fprintf(w, "Analyzed %d sessions | %d friction points | %d patterns found\n\n",
		report.SessionsUsed, report.FrictionTotal, report.PatternsFound)

	// Context Files section.
	fmt.Fprintln(w, s.SectionRule("Context Files"))
	for _, cf := range report.ContextFiles {
		if cf.Exists {
			line := fmt.Sprintf("  %s  %d bytes", cf.Type, cf.SizeBytes)
			fmt.Fprintln(w, s.Render(s.Bold, line))
		} else {
			line := fmt.Sprintf("  %s  missing", cf.Type)
			fmt.Fprintln(w, s.Render(s.Gray, line))
		}
	}
	fmt.Fprintln(w)

	// Suggestions section.
	fmt.Fprintln(w, s.SectionRule("Top Recommendations"))
	if len(report.Suggestions) == 0 {
		fmt.Fprintln(w, "  No suggestions generated.")
	}
	for i, sug := range report.Suggestions {
		// Title line with priority.
		titleLine := fmt.Sprintf("  %d. %s  %s", i+1, sug.Title, sug.Priority)
		fmt.Fprintln(w, s.Render(s.Bold, titleLine))

		target := sug.TargetKind
		switch {
		case sug.SkillName != "":
			target = sug.SkillName
		case sug.FilePath != "":
			target = sug.FilePath
		case sug.FileType != "":
			target = string(sug.FileType)
		}
		metaLine := fmt.Sprintf("     %s → %s", sug.Category, target)
		fmt.Fprintln(w, s.Render(s.Dim, metaLine))

		if sug.Description != "" {
			fmt.Fprintf(w, "     %s\n", sug.Description)
		}
		if sug.CopyablePrompt != "" {
			fmt.Fprintf(w, "     Prompt: %s\n", sug.CopyablePrompt)
		}
		if sug.SuggestedInstruction != "" {
			fmt.Fprintf(w, "     Instruction: %s\n", sug.SuggestedInstruction)
		}

		if sug.Diff != "" {
			fmt.Fprintln(w)
			renderDiff(w, s, sug.Diff)
		}
		fmt.Fprintln(w)
	}

	renderSuggestionGroup(w, s, "Prompt Changes", report.Suggestions, "prompt_recommendation")
	renderSuggestionGroup(w, s, "Project Skill Updates", report.Suggestions, "skill_recommendation")
}

// renderImproveTerminalDryRun writes a styled terminal view of the dry-run friction data.
func renderImproveTerminalDryRun(w io.Writer, analysis improve.PatternAnalysis, sessionCount, frictionTotal int) {
	s := termstyle.New(w)

	fmt.Fprintln(w, s.Render(s.Bold, "Entire Improve (dry run)"))
	fmt.Fprintf(w, "Analyzed %d sessions | %d friction points | %d signals found\n\n",
		sessionCount, frictionTotal, analysisPatternCount(analysis))

	fmt.Fprintln(w, s.SectionRule("Recurring Friction Themes"))
	if len(analysis.RepeatedFriction) == 0 {
		fmt.Fprintln(w, "  No recurring friction themes found.")
		return
	}
	for _, p := range analysis.RepeatedFriction {
		headerLine := fmt.Sprintf("  [%dx] %s (%d sessions)", p.Count, p.Theme, len(p.AffectedSessions))
		fmt.Fprintln(w, s.Render(s.Bold, headerLine))
		limit := 3
		if len(p.Examples) < limit {
			limit = len(p.Examples)
		}
		for _, ex := range p.Examples[:limit] {
			fmt.Fprintln(w, s.Render(s.Gray, "    "+ex))
		}
	}

	renderRecurringSignals(w, s, "Repeated Instructions", analysis.RepeatedInstructions)
	renderRecurringSignals(w, s, "Missing Context", analysis.MissingContextSignals)

	fmt.Fprintln(w)
	fmt.Fprintln(w, s.SectionRule("Project Skill Updates"))
	if len(analysis.SkillOpportunities) == 0 {
		fmt.Fprintln(w, "  No recurring skill-related opportunities found.")
		return
	}
	for _, opportunity := range analysis.SkillOpportunities {
		fmt.Fprintf(w, "  [%dx] %s\n", opportunity.Count, opportunity.SkillName)
		if opportunity.MissingInstruction != "" {
			fmt.Fprintf(w, "    %s\n", opportunity.MissingInstruction)
		}
	}
}

// renderUsageLine prints a single-line cost/token summary after the report.
func renderUsageLine(w io.Writer, usage *llmcli.UsageInfo) {
	s := termstyle.New(w)
	tokens := termstyle.FormatTokenCount(usage.InputTokens + usage.OutputTokens)
	line := fmt.Sprintf("\nCost: $%.4f (%s tokens)", usage.TotalCostUSD, tokens)
	fmt.Fprintln(w, s.Render(s.Dim, line))
}

// renderDiff writes a unified diff with colored lines to w.
// Lines starting with '+' are rendered in green, '-' in red, '@@' in cyan,
// and all other lines in dim.
func renderDiff(w io.Writer, s termstyle.Styles, diff string) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			fmt.Fprintln(w, s.Render(s.Cyan, line))
		case strings.HasPrefix(line, "+"):
			fmt.Fprintln(w, s.Render(s.Green, line))
		case strings.HasPrefix(line, "-"):
			fmt.Fprintln(w, s.Render(s.Red, line))
		default:
			fmt.Fprintln(w, s.Render(s.Dim, line))
		}
	}
}

func renderSuggestionGroup(w io.Writer, s termstyle.Styles, title string, suggestions []improve.Suggestion, targetKind string) {
	fmt.Fprintln(w, s.SectionRule(title))
	count := 0
	for _, suggestion := range suggestions {
		if suggestion.TargetKind != targetKind {
			continue
		}
		count++
		fmt.Fprintf(w, "  %d. %s\n", count, suggestion.Title)
	}
	if count == 0 {
		fmt.Fprintln(w, "  No suggestions in this category.")
	}
	fmt.Fprintln(w)
}

func renderRecurringSignals(w io.Writer, s termstyle.Styles, title string, signals []improve.RecurringSignal) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, s.SectionRule(title))
	if len(signals) == 0 {
		fmt.Fprintln(w, "  None found.")
		return
	}
	for _, signal := range signals {
		fmt.Fprintf(w, "  [%dx] %s\n", signal.Count, signal.Value)
	}
}

func analysisPatternCount(analysis improve.PatternAnalysis) int {
	return len(analysis.RepeatedFriction) +
		len(analysis.RepeatedInstructions) +
		len(analysis.MissingContextSignals) +
		len(analysis.FailureLoops) +
		len(analysis.SkillOpportunities)
}

func facetCounts(analysis improve.PatternAnalysis) improve.FacetCounts {
	return improve.FacetCounts{
		RepeatedInstructions: len(analysis.RepeatedInstructions),
		MissingContext:       len(analysis.MissingContextSignals),
		FailureLoops:         len(analysis.FailureLoops),
		SkillSignals:         len(analysis.SkillOpportunities),
	}
}

func facetSummary(analysis improve.PatternAnalysis) improve.FacetSummary {
	return improve.FacetSummary{
		RepeatedInstructions: analysis.RepeatedInstructions,
		MissingContext:       analysis.MissingContextSignals,
		FailureLoops:         analysis.FailureLoops,
		SkillOpportunities:   analysis.SkillOpportunities,
	}
}

func backfillFacets(ctx context.Context, w io.Writer, idb *insightsdb.InsightsDB, lastN int, debug bool, debugHint string) {
	rows, err := idb.QueryLastNSessions(ctx, lastN)
	if err != nil {
		logging.Warn(ctx, "backfillFacets: query failed", "error", err)
		return
	}

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
			if debug {
				parts = append(parts, fmt.Sprintf("skipped %d", skipped))
			} else if debugHint != "" {
				parts = append(parts, debugHint)
			} else {
				parts = append(parts, fmt.Sprintf("skipped %d", skipped))
			}
		}
		fmt.Fprintf(w, "  %s\n\n", strings.Join(parts, ", "))
	}
}
