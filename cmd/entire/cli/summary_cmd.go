package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	checkpointid "github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/summarytui"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
	"github.com/spf13/cobra"
)

type summaryOptions struct {
	Last             int
	Agent            string
	Branch           string
	OutputJSON       bool
	CheckpointPrefix string
}

const (
	defaultSummarySessionLimit = 10
	maxSummaryRecentSessions   = 200
)

type summarySessionView struct {
	CheckpointID string              `json:"checkpoint_id"`
	SessionID    string              `json:"session_id"`
	Agent        string              `json:"agent"`
	Model        string              `json:"model,omitempty"`
	Branch       string              `json:"branch,omitempty"`
	CreatedAt    string              `json:"created_at"`
	Tokens       int                 `json:"tokens"`
	Turns        int                 `json:"turns"`
	HasSummary   bool                `json:"has_summary"`
	Summary      *checkpoint.Summary `json:"summary,omitempty"`
}

var runSummaryTUI = summarytui.RunWithCurrentBranch //nolint:gochecknoglobals // injectable for testing

func newSummaryCmd() *cobra.Command {
	opts := summaryOptions{Last: defaultSummarySessionLimit}

	cmd := &cobra.Command{
		Use:   "summary [checkpoint-id]",
		Short: "Browse source sessions with summaries",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			if checkDisabledGuard(ctx, w) {
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
	if opts.CheckpointPrefix == "" {
		if branch, err := GetCurrentBranch(ctx); err == nil {
			currentBranch = branch
		}
	}

	// For the "repo" view, show all loaded sessions (no branch filter).
	// The TUI filters by current branch vs all in the UI.
	return runSummaryTUI(ctx, rows, currentBranch, rows, generateForSession)
}

func loadSummarySessions(ctx context.Context, opts summaryOptions) ([]summarytui.SessionData, error) {
	repo, err := openRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("not in a git repository: %w", err)
	}

	v1Store := checkpoint.NewGitStore(repo)
	v2Store := checkpoint.NewV2GitStore(repo, strategy.ResolveCheckpointURL(ctx, "origin"))
	preferV2 := settings.IsCheckpointsV2Enabled(ctx)

	committed, err := listCommittedForExplain(ctx, v1Store, v2Store, preferV2)
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}

	// Checkpoint prefix lookup: filter by prefix.
	if opts.CheckpointPrefix != "" {
		var filtered []checkpoint.CommittedInfo
		for _, info := range committed {
			if strings.HasPrefix(info.CheckpointID.String(), opts.CheckpointPrefix) {
				filtered = append(filtered, info)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("checkpoint not found: %s", opts.CheckpointPrefix)
		}
		committed = filtered
	}

	// Limit to most recent sessions.
	limit := min(len(committed), maxSummaryRecentSessions)
	committed = committed[:limit]

	// Bulk-load metadata for each checkpoint.
	sessions := make([]summarytui.SessionData, 0, len(committed))
	for _, info := range committed {
		session, loadErr := loadSessionFromCheckpoint(ctx, v1Store, v2Store, preferV2, info)
		if loadErr != nil {
			logging.Debug(ctx, "summary: skipping unreadable checkpoint",
				slog.String("checkpoint_id", info.CheckpointID.String()),
				slog.String("error", loadErr.Error()))
			continue
		}
		sessions = append(sessions, session)
	}

	outputLimit := normalizedSummarySessionLimit(opts.Last)
	hasCLIFilters := opts.Agent != "" || opts.Branch != ""

	// Apply CLI filters.
	filtered := make([]summarytui.SessionData, 0, len(sessions))
	for _, s := range sessions {
		if opts.Agent != "" && !strings.EqualFold(strings.TrimSpace(s.Agent), strings.TrimSpace(opts.Agent)) {
			continue
		}
		if opts.Branch != "" && !strings.EqualFold(strings.TrimSpace(s.Branch), strings.TrimSpace(opts.Branch)) {
			continue
		}
		filtered = append(filtered, s)
		// Only cap results when CLI filters or JSON output are in use.
		// The TUI has its own branch filter, so it needs the full result set.
		if hasCLIFilters && len(filtered) == outputLimit {
			break
		}
	}

	return filtered, nil
}

// loadSessionFromCheckpoint reads checkpoint metadata and converts to SessionData.
func loadSessionFromCheckpoint(ctx context.Context, v1Store *checkpoint.GitStore, v2Store *checkpoint.V2GitStore, preferV2 bool, info checkpoint.CommittedInfo) (summarytui.SessionData, error) {
	reader, summary, err := checkpoint.ResolveCommittedReaderForCheckpoint(ctx, info.CheckpointID, v1Store, v2Store, preferV2)
	if err != nil {
		return summarytui.SessionData{}, fmt.Errorf("resolve checkpoint: %w", err)
	}

	latestIndex := 0
	if summary != nil && len(summary.Sessions) > 0 {
		latestIndex = len(summary.Sessions) - 1
	}

	content, err := reader.ReadSessionContent(ctx, info.CheckpointID, latestIndex)
	if err != nil {
		return summarytui.SessionData{}, fmt.Errorf("read session content: %w", err)
	}

	return committedToSessionData(info, latestIndex, &content.Metadata), nil
}

// committedToSessionData converts checkpoint metadata into TUI SessionData.
func committedToSessionData(info checkpoint.CommittedInfo, sessionIndex int, meta *checkpoint.CommittedMetadata) summarytui.SessionData {
	s := summarytui.SessionData{
		CheckpointID: info.CheckpointID.String(),
		SessionID:    meta.SessionID,
		SessionIndex: sessionIndex,
		Agent:        string(meta.Agent),
		Model:        meta.Model,
		Branch:       meta.Branch,
		CreatedAt:    meta.CreatedAt,
		FilesTouched: meta.FilesTouched,
		Summary:      meta.Summary,
	}

	if meta.TokenUsage != nil {
		s.InputTokens = meta.TokenUsage.InputTokens
		s.CacheTokens = meta.TokenUsage.CacheCreationTokens + meta.TokenUsage.CacheReadTokens
		s.OutputTokens = meta.TokenUsage.OutputTokens
		s.TotalTokens = meta.TokenUsage.InputTokens + meta.TokenUsage.CacheCreationTokens + meta.TokenUsage.CacheReadTokens + meta.TokenUsage.OutputTokens
	}

	if meta.SessionMetrics != nil {
		s.DurationMs = meta.SessionMetrics.DurationMs
		s.TurnCount = meta.SessionMetrics.TurnCount
	}

	return s
}

func normalizedSummarySessionLimit(last int) int {
	if last <= 0 {
		return defaultSummarySessionLimit
	}
	return min(last, maxSummaryRecentSessions)
}

func renderSummaryJSON(w io.Writer, rows []summarytui.SessionData) error {
	views := make([]summarySessionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, sessionDataToView(row))
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(views); err != nil {
		return fmt.Errorf("encode summary sessions: %w", err)
	}
	return nil
}

func renderSummaryText(w io.Writer, rows []summarytui.SessionData) {
	s := termstyle.New(w)
	fmt.Fprintln(w, s.Render(s.Bold, "Session Summary"))
	if len(rows) == 0 {
		fmt.Fprintln(w, "No sessions found.")
		return
	}

	for _, row := range rows {
		fmt.Fprintf(w, "\n%s  %s  %s\n", row.CreatedAt.Format("2006-01-02 15:04"), row.Agent, row.SessionID)
		if row.Branch != "" {
			fmt.Fprintf(w, "Branch: %s\n", row.Branch)
		}
		fmt.Fprintf(w, "Checkpoint: %s\n", row.CheckpointID)

		hasSummary := row.Summary != nil
		fmt.Fprintf(w, "Summary: %s\n", yesNo(hasSummary))

		if hasSummary {
			fmt.Fprintln(w, "Intent:")
			fmt.Fprintf(w, "  %s\n", fallbackText(row.Summary.Intent, "No summary cached"))
			fmt.Fprintln(w, "Outcome:")
			fmt.Fprintf(w, "  %s\n", fallbackText(row.Summary.Outcome, "No summary cached"))
			renderStringListSection(w, "Friction", row.Summary.Friction, "No friction recorded")
			renderStringListSection(w, "Open Items", row.Summary.OpenItems, "No open items")
			renderLearningSection(w, row.Summary.Learnings)
		} else {
			fmt.Fprintln(w, "No summary cached")
		}
	}
}

func renderLearningSection(w io.Writer, learnings checkpoint.LearningsSummary) {
	hasLearnings := len(learnings.Repo) > 0 || len(learnings.Code) > 0 || len(learnings.Workflow) > 0
	if !hasLearnings {
		fmt.Fprintln(w, "Learnings:")
		fmt.Fprintln(w, "  No learnings recorded")
		return
	}

	fmt.Fprintln(w, "Learnings:")
	for _, item := range learnings.Repo {
		fmt.Fprintf(w, "  - [repo] %s\n", item)
	}
	for _, item := range learnings.Code {
		if item.Path != "" {
			fmt.Fprintf(w, "  - [code] %s (%s)\n", item.Finding, item.Path)
			continue
		}
		fmt.Fprintf(w, "  - [code] %s\n", item.Finding)
	}
	for _, item := range learnings.Workflow {
		fmt.Fprintf(w, "  - [workflow] %s\n", item)
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

func sessionDataToView(row summarytui.SessionData) summarySessionView {
	return summarySessionView{
		CheckpointID: row.CheckpointID,
		SessionID:    row.SessionID,
		Agent:        row.Agent,
		Model:        row.Model,
		Branch:       row.Branch,
		CreatedAt:    row.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Tokens:       row.TotalTokens,
		Turns:        row.TurnCount,
		HasSummary:   row.Summary != nil,
		Summary:      row.Summary,
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
// Persists the summary to the checkpoint store; facets are kept in-memory only.
func generateForSession(ctx context.Context, session summarytui.SessionData) (summarytui.SessionData, error) {
	repo, err := openRepository(ctx)
	if err != nil {
		return session, fmt.Errorf("open repository: %w", err)
	}

	v1Store := checkpoint.NewGitStore(repo)
	v2Store := checkpoint.NewV2GitStore(repo, strategy.ResolveCheckpointURL(ctx, "origin"))
	preferV2 := settings.IsCheckpointsV2Enabled(ctx)

	cpID, err := checkpointid.NewCheckpointID(session.CheckpointID)
	if err != nil {
		return session, fmt.Errorf("invalid checkpoint ID: %w", err)
	}

	reader, cpSummary, err := checkpoint.ResolveCommittedReaderForCheckpoint(ctx, cpID, v1Store, v2Store, preferV2)
	if err != nil {
		return session, fmt.Errorf("resolve checkpoint: %w", err)
	}

	content, err := reader.ReadSessionContent(ctx, cpID, session.SessionIndex)
	if err != nil {
		return session, fmt.Errorf("read session content: %w", err)
	}
	if len(content.Transcript) == 0 {
		return session, fmt.Errorf("empty transcript for checkpoint %s", session.CheckpointID)
	}

	// Scope transcript to this checkpoint's portion.
	scopedTranscript := scopeTranscriptForCheckpoint(content.Transcript, content.Metadata.GetTranscriptStart(), content.Metadata.Agent)
	if len(scopedTranscript) == 0 {
		return session, fmt.Errorf("scoped transcript empty for checkpoint %s", session.CheckpointID)
	}

	// Determine files touched for summary input.
	filesTouched := session.FilesTouched
	if cpSummary != nil && len(cpSummary.FilesTouched) > 0 {
		filesTouched = cpSummary.FilesTouched
	}

	// Generate summary (reuses explain.go's helper).
	summary, genErr := generateCheckpointAISummary(ctx, scopedTranscript, filesTouched, content.Metadata.Agent)
	if genErr != nil {
		logging.Debug(ctx, "generateForSession: summary generation failed",
			"checkpoint_id", session.CheckpointID, "error", genErr)
	}
	if summary != nil {
		// Persist to checkpoint store (same as explain --generate).
		v1Err := v1Store.UpdateSummary(ctx, cpID, summary)
		if v1Err != nil {
			logging.Debug(ctx, "generateForSession: v1 UpdateSummary failed",
				slog.String("checkpoint_id", session.CheckpointID),
				slog.String("error", v1Err.Error()))
		}
		if v2Store != nil {
			v2Err := v2Store.UpdateSummary(ctx, cpID, summary)
			if v2Err != nil {
				logging.Debug(ctx, "generateForSession: v2 UpdateSummary failed",
					slog.String("checkpoint_id", session.CheckpointID),
					slog.String("error", v2Err.Error()))
			}
		}
		session.Summary = summary
	}

	if genErr != nil {
		return session, fmt.Errorf("summary generation: %w", genErr)
	}
	return session, nil
}
