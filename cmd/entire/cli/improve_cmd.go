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
	"github.com/entireio/cli/cmd/entire/cli/improve"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
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

	// Phase 1: Query SQLite index for recurring friction themes (2+ occurrences).
	frictionThemes, err := idb.QueryRecurringFriction(ctx, 2)
	if err != nil {
		return fmt.Errorf("query recurring friction: %w", err)
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

	if dryRun {
		if outputJSON {
			return renderImproveJSONDryRun(w, frictionThemes, len(rows), frictionTotal)
		}
		renderImproveTerminalDryRun(w, frictionThemes, len(rows), frictionTotal)
		return nil
	}

	// Phase 2: Deep-read transcripts for top friction themes.
	patterns := buildFrictionPatterns(frictionThemes)
	if err = attachTranscriptExcerpts(ctx, idb, patterns, worktreeRoot); err != nil {
		// Non-fatal: proceed without transcript excerpts.
		_ = err
	}

	// Phase 3: Detect context files.
	contextFiles := improve.DetectContextFiles(worktreeRoot)

	// Phase 4: Build analysis from session data + friction patterns, then generate.
	summaries := sessionRowsToSummaries(rows)
	analysis := improve.AnalyzePatterns(summaries)
	// Overlay the transcript excerpts we fetched into the analysis.
	applyExcerpts(analysis.RepeatedFriction, patterns)

	gen := improve.Generator{Runner: &llmcli.Runner{}}
	suggestions, err := gen.Generate(ctx, analysis, contextFiles)
	if err != nil {
		return fmt.Errorf("generate suggestions: %w", err)
	}

	report := improve.ImprovementReport{
		ContextFiles:  contextFiles,
		Suggestions:   suggestions,
		SessionsUsed:  len(rows),
		FrictionTotal: frictionTotal,
		PatternsFound: len(analysis.RepeatedFriction),
	}

	if outputJSON {
		return renderImproveJSON(w, report)
	}
	renderImproveTerminal(w, report)
	return nil
}

// buildFrictionPatterns converts insightsdb friction themes to improve.FrictionPattern values.
func buildFrictionPatterns(themes []insightsdb.FrictionTheme) []improve.FrictionPattern {
	patterns := make([]improve.FrictionPattern, 0, len(themes))
	for _, t := range themes {
		patterns = append(patterns, improve.FrictionPattern{
			Theme:            t.Text,
			Count:            t.Count,
			AffectedSessions: t.Sessions,
		})
	}
	return patterns
}

// attachTranscriptExcerpts fetches transcript excerpts for top friction themes and
// attaches them to the corresponding patterns in-place.
// Errors are non-fatal; unreadable sessions are silently skipped.
func attachTranscriptExcerpts(ctx context.Context, idb *insightsdb.InsightsDB, patterns []improve.FrictionPattern, _ string) error {
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
		cpIDs, queryErr := idb.QuerySessionsWithFriction(ctx, "%"+patterns[i].Theme+"%")
		if queryErr != nil {
			continue
		}

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

// applyExcerpts copies TranscriptExcerpt values from src patterns into dst patterns
// by matching on theme text.
func applyExcerpts(dst []improve.FrictionPattern, src []improve.FrictionPattern) {
	excerptByTheme := make(map[string]string, len(src))
	for _, p := range src {
		if p.TranscriptExcerpt != "" {
			excerptByTheme[p.Theme] = p.TranscriptExcerpt
		}
	}
	for i := range dst {
		if excerpt, ok := excerptByTheme[dst[i].Theme]; ok {
			dst[i].TranscriptExcerpt = excerpt
		}
	}
}

// sessionRowsToSummaries converts insightsdb rows into improve.SessionSummaryData values.
func sessionRowsToSummaries(rows []insightsdb.SessionRow) []improve.SessionSummaryData {
	summaries := make([]improve.SessionSummaryData, 0, len(rows))
	for _, r := range rows {
		s := improve.SessionSummaryData{
			CheckpointID: r.CheckpointID,
			Friction:     r.Friction,
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

// renderImproveJSONDryRun marshals the dry-run friction data to JSON and writes it to w.
func renderImproveJSONDryRun(w io.Writer, themes []insightsdb.FrictionTheme, sessionCount, frictionTotal int) error {
	type dryRunReport struct {
		SessionsAnalyzed int                        `json:"sessions_analyzed"`
		FrictionTotal    int                        `json:"friction_total"`
		RecurringThemes  []insightsdb.FrictionTheme `json:"recurring_themes"`
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
	fmt.Fprintln(w, s.SectionRule(fmt.Sprintf("Suggestions (%d)", len(report.Suggestions))))
	if len(report.Suggestions) == 0 {
		fmt.Fprintln(w, "  No suggestions generated.")
	}
	for i, sug := range report.Suggestions {
		// Title line with priority.
		titleLine := fmt.Sprintf("  %d. %s  %s", i+1, sug.Title, sug.Priority)
		fmt.Fprintln(w, s.Render(s.Bold, titleLine))

		// Category and file.
		metaLine := fmt.Sprintf("     %s → %s", sug.Category, sug.FileType)
		fmt.Fprintln(w, s.Render(s.Dim, metaLine))

		// Description.
		if sug.Description != "" {
			fmt.Fprintf(w, "     %s\n", sug.Description)
		}

		// Diff rendering.
		if sug.Diff != "" {
			fmt.Fprintln(w)
			renderDiff(w, s, sug.Diff)
		}
		fmt.Fprintln(w)
	}
}

// renderImproveTerminalDryRun writes a styled terminal view of the dry-run friction data.
func renderImproveTerminalDryRun(w io.Writer, themes []insightsdb.FrictionTheme, sessionCount, frictionTotal int) {
	s := termstyle.New(w)

	fmt.Fprintln(w, s.Render(s.Bold, "Entire Improve (dry run)"))
	fmt.Fprintf(w, "Analyzed %d sessions | %d friction points | %d patterns found\n\n",
		sessionCount, frictionTotal, len(themes))

	fmt.Fprintln(w, s.SectionRule("Recurring Friction"))
	if len(themes) == 0 {
		fmt.Fprintln(w, "  No recurring friction patterns found.")
		return
	}
	for _, t := range themes {
		countLine := fmt.Sprintf("  [%dx] %s", t.Count, t.Text)
		fmt.Fprintln(w, s.Render(s.Bold, countLine))
		if len(t.Sessions) > 0 {
			fmt.Fprintln(w, s.Render(s.Gray, "       sessions: "+strings.Join(t.Sessions, ", ")))
		}
	}
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
