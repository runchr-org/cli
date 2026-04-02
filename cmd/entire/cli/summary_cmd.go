package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/facets"
	"github.com/entireio/cli/cmd/entire/cli/githubidentity"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/summarytui"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
	"github.com/spf13/cobra"
)

type summaryOptions struct {
	Last       int
	Agent      string
	Branch     string
	Me         bool
	OutputJSON bool
}

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
	Intent    string                   `json:"intent,omitempty"`
	Outcome   string                   `json:"outcome,omitempty"`
	Friction  []string                 `json:"friction,omitempty"`
	Learnings []insightsdb.LearningRow `json:"learnings,omitempty"`
}

var runSummaryTUI = summarytui.RunWithCurrentBranch

func newSummaryCmd() *cobra.Command {
	opts := summaryOptions{Last: 10}

	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Browse source sessions with summaries and facets",
		RunE: func(cmd *cobra.Command, _ []string) error {
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

	return runSummaryTUI(ctx, rows, currentBranch)
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

	refreshCacheIfStale(ctx, idb) //nolint:errcheck,gosec // non-fatal; use cached data if refresh fails
	backfillSummaries(ctx, io.Discard, idb, opts.Last)
	backfillFacets(ctx, io.Discard, idb, opts.Last)

	limit := opts.Last
	if limit <= 0 {
		limit = 10
	}

	total, err := idb.SessionCount(ctx)
	if err != nil {
		return nil, fmt.Errorf("session count: %w", err)
	}
	if total == 0 {
		return nil, nil
	}

	rows, err := idb.QueryLastNSessions(ctx, total)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}

	ownerID := ""
	if opts.Me {
		ownerID, err = githubidentity.ResolveUsername(ctx)
		if err != nil {
			return nil, err
		}
	}

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
		if len(filtered) == limit {
			break
		}
	}

	return filtered, nil
}

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
			Intent:    row.Intent,
			Outcome:   row.Outcome,
			Friction:  row.Friction,
			Learnings: row.Learnings,
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

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
