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
	"github.com/entireio/cli/cmd/entire/cli/githubidentity"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/summarize"
	"github.com/entireio/cli/cmd/entire/cli/summarytui"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
	"github.com/spf13/cobra"
)

type summaryOptions struct {
	Last             int
	Agent            string
	Branch           string
	Me               bool
	OutputJSON       bool
	CheckpointPrefix string
}

const (
	defaultSummarySessionLimit = 10
	maxSummaryRecentSessions   = 200
)

type summarySessionView struct {
	CheckpointID string               `json:"checkpoint_id"`
	SessionID    string               `json:"session_id"`
	Agent        string               `json:"agent"`
	Model        string               `json:"model,omitempty"`
	Branch       string               `json:"branch,omitempty"`
	OwnerID      string               `json:"owner_id,omitempty"`
	CreatedAt    string               `json:"created_at"`
	Tokens       int                  `json:"tokens"`
	Turns        int                  `json:"turns"`
	HasSummary   bool                 `json:"has_summary"`
	HasFacets    bool                 `json:"has_facets"`
	Summary      summaryDetailView    `json:"summary"`
	Facets       facets.SessionFacets `json:"facets"`
}

type summaryDetailView struct {
	Intent                  string                   `json:"intent,omitempty"`
	Outcome                 string                   `json:"outcome,omitempty"`
	Friction                []string                 `json:"friction,omitempty"`
	Learnings               []insightsdb.LearningRow `json:"learnings,omitempty"`
	ImplementationRationale []string                 `json:"implementation_rationale,omitempty"`
	Tradeoffs               []string                 `json:"tradeoffs,omitempty"`
	CodebasePatterns        []string                 `json:"codebase_patterns,omitempty"`
}

var runSummaryTUI = summarytui.RunWithCurrentBranch //nolint:gochecknoglobals // injectable for testing
var summaryRefreshCacheIfStale = refreshCacheIfStale

func newSummaryCmd() *cobra.Command {
	opts := summaryOptions{Last: defaultSummarySessionLimit}

	cmd := &cobra.Command{
		Use:   "summary [checkpoint-id]",
		Short: "Browse source sessions with summaries and facets",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			if checkDisabledGuard(ctx, w) {
				return nil
			}

			if !settings.IsSummarizeEnabled(ctx) {
				fmt.Fprintln(w, "Summarization is required for summary browsing. Enable it in .entire/settings.json:")
				fmt.Fprintln(w, `  { "strategy_options": { "summarize": { "enabled": true } } }`)
				return nil
			}

			if len(args) > 0 {
				opts.CheckpointPrefix = args[0]
			}

			return runSummary(ctx, w, opts)
		},
	}

	cmd.Flags().IntVar(&opts.Last, "last", opts.Last, "number of recent sessions to browse")
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "filter by agent name")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "filter by branch")
	cmd.Flags().BoolVar(&opts.Me, "me", false, "show only sessions for the active GitHub user")
	cmd.Flags().BoolVar(&opts.OutputJSON, "json", false, "output sessions as JSON instead of launching the browser")
	return cmd
}

func runSummary(ctx context.Context, w io.Writer, opts summaryOptions) error {
	rows, err := loadSummarySessions(ctx, opts)
	if err != nil {
		return err
	}

	if opts.OutputJSON {
		return renderSummaryJSON(w, rows)
	}

	if IsAccessibleMode() {
		renderSummaryText(w, rows)
		return nil
	}

	currentBranch := ""
	if branch, err := GetCurrentBranch(ctx); err == nil {
		currentBranch = branch
	}

	return runSummaryTUI(ctx, rows, currentBranch, generateForSession)
}

func loadSummarySessions(ctx context.Context, opts summaryOptions) ([]insightsdb.SessionRow, error) {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("not in a git repository: %w", err)
	}

	idb, err := insightsdb.Open(filepath.Join(worktreeRoot, paths.EntireDir, "insights.db"))
	if err != nil {
		return nil, fmt.Errorf("open insights cache: %w", err)
	}
	defer func() { _ = idb.Close() }()

	outputLimit := normalizedSummarySessionLimit(opts.Last)

	// Checkpoint prefix lookup: skip normal loading, query directly by prefix.
	if opts.CheckpointPrefix != "" {
		rows, err := idb.QueryByCheckpointPrefix(ctx, opts.CheckpointPrefix)
		if err != nil {
			return nil, fmt.Errorf("query by checkpoint: %w", err)
		}
		if len(rows) == 0 {
			// Try refreshing cache in case the checkpoint hasn't been indexed yet.
			if err := summaryRefreshCacheIfStale(ctx, idb); err != nil {
				return nil, fmt.Errorf("refresh insights cache: %w", err)
			}
			rows, err = idb.QueryByCheckpointPrefix(ctx, opts.CheckpointPrefix)
			if err != nil {
				return nil, fmt.Errorf("query by checkpoint after refresh: %w", err)
			}
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("checkpoint not found: %s", opts.CheckpointPrefix)
		}
		return rows, nil
	}

	rows, err := idb.QueryLastNSessions(ctx, maxSummaryRecentSessions)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	if len(rows) == 0 {
		// First-run fallback: populate the cache if we have nothing local yet.
		if err := summaryRefreshCacheIfStale(ctx, idb); err != nil {
			return nil, fmt.Errorf("refresh insights cache: %w", err)
		}
		rows, err = idb.QueryLastNSessions(ctx, maxSummaryRecentSessions)
		if err != nil {
			return nil, fmt.Errorf("query sessions after refresh: %w", err)
		}
	}

	ownerID := ""
	if opts.Me {
		ownerID, err = githubidentity.ResolveUsername(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve github username: %w", err)
		}
	}

	hasCLIFilters := opts.Agent != "" || opts.Branch != "" || ownerID != ""

	filtered := make([]insightsdb.SessionRow, 0, len(rows))
	for _, row := range rows {
		if opts.Agent != "" && !strings.EqualFold(strings.TrimSpace(row.Agent), strings.TrimSpace(opts.Agent)) {
			continue
		}
		if opts.Branch != "" && !strings.EqualFold(strings.TrimSpace(row.Branch), strings.TrimSpace(opts.Branch)) {
			continue
		}
		if ownerID != "" && !strings.EqualFold(strings.TrimSpace(row.OwnerID), ownerID) {
			continue
		}
		filtered = append(filtered, row)
		// Only cap results when CLI filters or JSON output are in use.
		// The TUI has its own branch filter, so it needs the full result set.
		if hasCLIFilters && len(filtered) == outputLimit {
			break
		}
	}

	return filtered, nil
}

func normalizedSummarySessionLimit(last int) int {
	if last <= 0 {
		return defaultSummarySessionLimit
	}
	return min(last, maxSummaryRecentSessions)
}

//nolint:musttag // nested external structs are part of the intended JSON payload
func renderSummaryJSON(w io.Writer, rows []insightsdb.SessionRow) error {
	views := make([]summarySessionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, summarySessionToView(row))
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(views); err != nil {
		return fmt.Errorf("encode summary sessions: %w", err)
	}
	return nil
}

func renderSummaryText(w io.Writer, rows []insightsdb.SessionRow) {
	s := termstyle.New(w)
	fmt.Fprintln(w, s.Render(s.Bold, "Session Summary"))
	if len(rows) == 0 {
		fmt.Fprintln(w, "No sessions found.")
		return
	}

	for _, row := range rows {
		fmt.Fprintf(w, "\n%s  %s  %s\n", row.CreatedAt.Format("2006-01-02 15:04"), row.Agent, row.SessionID)
		if row.OwnerID != "" || row.OwnerName != "" || row.OwnerEmail != "" {
			fmt.Fprintf(w, "Author: %s\n", fallbackText(row.OwnerID, "(unknown)"))
			if row.OwnerName != "" {
				fmt.Fprintf(w, "Author Name: %s\n", row.OwnerName)
			}
			if row.OwnerEmail != "" {
				fmt.Fprintf(w, "Author Email: %s\n", row.OwnerEmail)
			}
		}
		if row.Branch != "" {
			fmt.Fprintf(w, "Branch: %s\n", row.Branch)
		}
		fmt.Fprintf(w, "Checkpoint: %s\n", row.CheckpointID)
		fmt.Fprintf(w, "Summary: %s  Facets: %s\n", yesNo(row.HasSummary), yesNo(row.HasFacets))

		if row.HasSummary {
			fmt.Fprintln(w, "Intent:")
			fmt.Fprintf(w, "  %s\n", fallbackText(row.Intent, "No summary cached"))
			fmt.Fprintln(w, "Outcome:")
			fmt.Fprintf(w, "  %s\n", fallbackText(row.Outcome, "No summary cached"))
			renderStringListSection(w, "Friction", row.Friction, "No friction recorded")
			renderLearningSection(w, row.Learnings)
			renderStringListSection(w, "Implementation Rationale", row.ImplementationRationale, "No implementation rationale recorded")
			renderStringListSection(w, "Tradeoffs", row.Tradeoffs, "No tradeoffs recorded")
			renderStringListSection(w, "Codebase Patterns", row.CodebasePatterns, "No codebase patterns recorded")
		} else {
			fmt.Fprintln(w, "No summary cached")
		}

		renderFacetSections(w, row.Facets, row.HasFacets)
	}
}

func renderLearningSection(w io.Writer, learnings []insightsdb.LearningRow) {
	if len(learnings) == 0 {
		fmt.Fprintln(w, "Learnings:")
		fmt.Fprintln(w, "  No learnings recorded")
		return
	}

	fmt.Fprintln(w, "Learnings:")
	for _, learning := range learnings {
		if learning.Path != "" {
			fmt.Fprintf(w, "  - [%s] %s (%s)\n", learning.Scope, learning.Finding, learning.Path)
			continue
		}
		fmt.Fprintf(w, "  - [%s] %s\n", learning.Scope, learning.Finding)
	}
}

func renderFacetSections(w io.Writer, data facets.SessionFacets, hasFacets bool) {
	if !hasFacets {
		fmt.Fprintln(w, "No facets cached")
		return
	}

	fmt.Fprintln(w, "Repeated Instructions:")
	if len(data.RepeatedUserInstructions) == 0 {
		fmt.Fprintln(w, "  None")
	} else {
		for _, item := range data.RepeatedUserInstructions {
			fmt.Fprintf(w, "  - %s\n", item.Instruction)
		}
	}

	fmt.Fprintln(w, "Missing Context:")
	if len(data.MissingContext) == 0 {
		fmt.Fprintln(w, "  None")
	} else {
		for _, item := range data.MissingContext {
			fmt.Fprintf(w, "  - %s\n", item.Item)
		}
	}

	fmt.Fprintln(w, "Review-Derived Rules:")
	if len(data.ReviewDerivedRules) == 0 {
		fmt.Fprintln(w, "  None")
	} else {
		for _, item := range data.ReviewDerivedRules {
			fmt.Fprintf(w, "  - %s\n", item.Rule)
		}
	}

	fmt.Fprintln(w, "Failure Loops:")
	if len(data.FailureLoops) == 0 {
		fmt.Fprintln(w, "  None")
	} else {
		for _, item := range data.FailureLoops {
			fmt.Fprintf(w, "  - %s (%d)\n", item.Description, item.Count)
		}
	}

	fmt.Fprintln(w, "Skill Signals:")
	if len(data.SkillSignals) == 0 {
		fmt.Fprintln(w, "  None")
	} else {
		for _, item := range data.SkillSignals {
			fmt.Fprintf(w, "  - %s\n", item.SkillName)
		}
	}

	fmt.Fprintln(w, "Repo Gotchas:")
	if len(data.RepoGotchas) == 0 {
		fmt.Fprintln(w, "  None")
	} else {
		for _, item := range data.RepoGotchas {
			fmt.Fprintf(w, "  - %s\n", item)
		}
	}

	fmt.Fprintln(w, "Workflow Gaps:")
	if len(data.WorkflowGaps) == 0 {
		fmt.Fprintln(w, "  None")
	} else {
		for _, item := range data.WorkflowGaps {
			fmt.Fprintf(w, "  - %s\n", item)
		}
	}
}

func renderStringListSection(w io.Writer, title string, items []string, empty string) {
	fmt.Fprintf(w, "%s:\n", title)
	if len(items) == 0 {
		fmt.Fprintf(w, "  %s\n", empty)
		return
	}
	for _, item := range items {
		fmt.Fprintf(w, "  - %s\n", item)
	}
}

func summarySessionToView(row insightsdb.SessionRow) summarySessionView {
	return summarySessionView{
		CheckpointID: row.CheckpointID,
		SessionID:    row.SessionID,
		Agent:        row.Agent,
		Model:        row.Model,
		Branch:       row.Branch,
		OwnerID:      row.OwnerID,
		CreatedAt:    row.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Tokens:       row.TotalTokens,
		Turns:        row.TurnCount,
		HasSummary:   row.HasSummary,
		HasFacets:    row.HasFacets,
		Summary: summaryDetailView{
			Intent:                  row.Intent,
			Outcome:                 row.Outcome,
			Friction:                row.Friction,
			Learnings:               row.Learnings,
			ImplementationRationale: row.ImplementationRationale,
			Tradeoffs:               row.Tradeoffs,
			CodebasePatterns:        row.CodebasePatterns,
		},
		Facets: row.Facets,
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func fallbackText(value, fb string) string {
	if strings.TrimSpace(value) == "" {
		return fb
	}
	return value
}

// generateForSession generates summary and facets for a single session on demand.
// It opens its own database connection and checkpoint store.
func generateForSession(ctx context.Context, row insightsdb.SessionRow) (insightsdb.SessionRow, error) {
	repo, err := openRepository(ctx)
	if err != nil {
		return row, fmt.Errorf("open repository: %w", err)
	}
	store := checkpoint.NewGitStore(repo)

	cpID, err := checkpointid.NewCheckpointID(row.CheckpointID)
	if err != nil {
		return row, fmt.Errorf("invalid checkpoint ID: %w", err)
	}

	content, err := store.ReadSessionContent(ctx, cpID, row.SessionIndex)
	if err != nil {
		return row, fmt.Errorf("read session content: %w", err)
	}
	if len(content.Transcript) == 0 {
		return row, fmt.Errorf("empty transcript for checkpoint %s", row.CheckpointID)
	}

	condensed, err := summarize.BuildCondensedTranscriptFromBytes(content.Transcript, content.Metadata.Agent)
	if err != nil {
		return row, fmt.Errorf("condense transcript: %w", err)
	}
	if len(condensed) == 0 {
		return row, fmt.Errorf("condensed transcript empty for checkpoint %s", row.CheckpointID)
	}

	input := summarize.Input{
		Transcript:   condensed,
		FilesTouched: row.FilesTouched,
	}

	// Generate summary.
	gen := &summarize.ClaudeGenerator{}
	summary, genErr := gen.Generate(ctx, input)
	if genErr != nil {
		logging.Debug(ctx, "generateForSession: summary generation failed",
			"checkpoint_id", row.CheckpointID, "error", genErr)
	}
	if summary != nil {
		content.Metadata.Summary = summary
		row = metadataToSessionRow(row.CheckpointID, row.SessionIndex, &content.Metadata)
	}

	// Extract facets.
	formatted := summarize.FormatCondensedTranscript(input)
	extractor := &facets.Extractor{Runner: &llmcli.Runner{}}
	facetResult, _, extractErr := extractor.Extract(ctx, formatted)
	if extractErr != nil {
		logging.Debug(ctx, "generateForSession: facet extraction failed",
			"checkpoint_id", row.CheckpointID, "error", extractErr)
	}
	if facetResult != nil {
		row.Facets = *facetResult
		row.HasFacets = true
	}

	// Persist to database.
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return row, fmt.Errorf("find worktree root: %w", err)
	}
	idb, err := insightsdb.Open(filepath.Join(worktreeRoot, paths.EntireDir, "insights.db"))
	if err != nil {
		return row, fmt.Errorf("open insights db: %w", err)
	}
	defer func() { _ = idb.Close() }()

	if row.HasSummary {
		if updateErr := idb.UpdateSessionSummary(ctx, row); updateErr != nil {
			return row, fmt.Errorf("save summary: %w", updateErr)
		}
	}
	if row.HasFacets {
		if updateErr := idb.UpdateSessionFacets(ctx, row); updateErr != nil {
			return row, fmt.Errorf("save facets: %w", updateErr)
		}
	}

	// Return partial success even if one phase failed.
	if genErr != nil && extractErr != nil {
		return row, fmt.Errorf("summary: %w; facets: %w", genErr, extractErr)
	}
	return row, nil
}
