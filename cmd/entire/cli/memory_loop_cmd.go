package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/improve"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
	"github.com/spf13/cobra"
)

type memoryLoopScopeMode string

const (
	memoryLoopScopeRepo   memoryLoopScopeMode = "repo"
	memoryLoopScopeBranch memoryLoopScopeMode = "branch"
	memoryLoopScopeMe     memoryLoopScopeMode = "me"
)

type memoryLoopScope struct {
	Mode       memoryLoopScopeMode
	Branch     string
	OwnerEmail string
}

const (
	memoryLoopAddScopeFlagKind  = "kind"
	memoryLoopAddScopeFlagTitle = "title"
	memoryLoopAddScopeFlagBody  = "body"
)

type memoryLoopRefreshSummary struct {
	SessionCount         int
	ScopeLabel           string
	GeneratedCount       int
	ActivatedCount       int
	CandidateCount       int
	ActiveCount          int
	StoredCandidateCount int
	SuppressedCount      int
	ArchivedCount        int
	GeneratedTitles      []string
}

type memoryLoopAddOptions struct {
	Kind  string
	Title string
	Body  string
	Scope string
}

func newMemoryLoopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory-loop",
		Short: "Build and inspect repo-scoped memory for future Claude sessions",
	}

	cmd.AddCommand(newMemoryLoopRefreshCmd())
	cmd.AddCommand(newMemoryLoopShowCmd())
	cmd.AddCommand(newMemoryLoopStatusCmd())
	cmd.AddCommand(newMemoryLoopModeCmd())
	cmd.AddCommand(newMemoryLoopPolicyCmd())
	cmd.AddCommand(newMemoryLoopAddCmd())
	cmd.AddCommand(newMemoryLoopActivateCmd())
	cmd.AddCommand(newMemoryLoopPromoteCmd())
	cmd.AddCommand(newMemoryLoopSuppressCmd())
	cmd.AddCommand(newMemoryLoopUnsuppressCmd())
	cmd.AddCommand(newMemoryLoopArchiveCmd())
	cmd.AddCommand(newMemoryLoopPruneCmd())

	return cmd
}

func newMemoryLoopAddCmd() *cobra.Command {
	opts := memoryLoopAddOptions{Scope: string(memoryLoopScopeMe)}

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a manual memory entry",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMemoryLoopAdd(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Kind, "kind", "", "memory kind: repo_rule, workflow_rule, agent_instruction, skill_patch, anti_pattern")
	cmd.Flags().StringVar(&opts.Title, "title", "", "memory title")
	cmd.Flags().StringVar(&opts.Body, "body", "", "memory body")
	cmd.Flags().StringVar(&opts.Scope, "scope", opts.Scope, "memory scope: me or repo")
	mustMarkFlagRequired(cmd, memoryLoopAddScopeFlagKind)
	mustMarkFlagRequired(cmd, memoryLoopAddScopeFlagTitle)
	mustMarkFlagRequired(cmd, memoryLoopAddScopeFlagBody)
	return cmd
}

func newMemoryLoopRefreshCmd() *cobra.Command {
	var last int
	var scope string
	var branch string

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Rebuild the memory snapshot from recent sessions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMemoryLoopRefresh(cmd.Context(), cmd.OutOrStdout(), last, scope, branch)
		},
	}
	cmd.Flags().IntVar(&last, "last", memoryloop.DefaultRefreshWindow, "number of recent sessions to distill into memory")
	cmd.Flags().StringVar(&scope, "scope", string(memoryLoopScopeRepo), "session scope to use: repo, branch, or me")
	cmd.Flags().StringVar(&branch, "branch", "", "branch name to use when --scope branch (defaults to current branch)")
	return cmd
}

func newMemoryLoopShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the current memory snapshot and recent injection history",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMemoryLoopShow(cmd.Context(), cmd.OutOrStdout(), "")
		},
	}
}

func newMemoryLoopStatusCmd() *cobra.Command {
	var prompt string
	var verbose bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show memory-loop status and optional prompt preview",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMemoryLoopStatus(cmd.Context(), cmd.OutOrStdout(), prompt, verbose)
		},
	}
	cmd.Flags().StringVar(&prompt, "prompt", "", "preview which memories would inject for this prompt")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show scored memory matches and retrieval reasons for prompt preview")
	return cmd
}

func newMemoryLoopModeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mode [off|manual|auto]",
		Short: "Set memory loop mode",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := memoryloop.Mode(strings.ToLower(strings.TrimSpace(args[0])))
			switch mode {
			case memoryloop.ModeOff, memoryloop.ModeManual, memoryloop.ModeAuto:
				return setMemoryLoopMode(cmd.Context(), cmd.OutOrStdout(), mode)
			default:
				return fmt.Errorf("invalid mode: %s", args[0])
			}
		},
	}
}

func newMemoryLoopPolicyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "policy [review|auto]",
		Short: "Set memory loop activation policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			policy := memoryloop.ActivationPolicy(strings.ToLower(strings.TrimSpace(args[0])))
			switch policy {
			case memoryloop.ActivationPolicyReview, memoryloop.ActivationPolicyAuto:
				return setMemoryLoopPolicy(cmd.Context(), cmd.OutOrStdout(), policy)
			default:
				return fmt.Errorf("invalid activation policy: %s", args[0])
			}
		},
	}
}

func newMemoryLoopActivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "activate <id>",
		Short: "Activate a personal memory candidate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryLoopActivate(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

func newMemoryLoopPromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a repo-scoped memory candidate to active",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryLoopPromote(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

func newMemoryLoopSuppressCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suppress <id>",
		Short: "Suppress a memory so it no longer injects",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryLoopSuppress(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

func newMemoryLoopUnsuppressCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unsuppress <id>",
		Short: "Return a suppressed memory to candidate state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryLoopUnsuppress(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

func newMemoryLoopArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <id>",
		Short: "Archive a memory while preserving its history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryLoopArchive(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

func newMemoryLoopPruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Archive stale or ineffective generated memories",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMemoryLoopPrune(cmd.Context(), cmd.OutOrStdout(), time.Now().UTC())
		},
	}
}

func runMemoryLoopRefresh(ctx context.Context, w io.Writer, last int, scopeArg, branchArg string) error {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return fmt.Errorf("not in a git repository: %w", err)
	}

	cfg, cfgErr := LoadEntireSettings(ctx)
	if cfgErr != nil {
		return fmt.Errorf("load settings: %w", cfgErr)
	}

	idb, err := insightsdb.Open(filepath.Join(worktreeRoot, paths.EntireDir, "insights.db"))
	if err != nil {
		return fmt.Errorf("open insights cache: %w", err)
	}
	defer func() { _ = idb.Close() }()

	renderMemoryLoopRefreshProgress(w, "Refreshing cache...")
	refreshCacheIfStale(ctx, idb) //nolint:errcheck,gosec // non-fatal
	if settings.IsSummarizeEnabled(ctx) {
		renderMemoryLoopRefreshProgress(w, "Backfilling summaries...")
		backfillSummaries(ctx, w, idb, last)
		renderMemoryLoopRefreshProgress(w, "Backfilling facets...")
		backfillFacets(ctx, idb, last)
	}

	scope, err := resolveMemoryLoopScope(ctx, scopeArg, branchArg)
	if err != nil {
		return err
	}

	renderMemoryLoopRefreshProgress(w, "Loading scoped sessions...")
	rows, err := queryMemoryLoopRows(ctx, idb, scope, last)
	if err != nil {
		return fmt.Errorf("query sessions: %w", err)
	}

	analysis := improve.AnalyzePatterns(sessionRowsToSummaries(rows))
	gen := memoryloop.Generator{Runner: &llmcli.Runner{}}
	renderMemoryLoopRefreshProgress(w, "Distilling memories...")
	records, usage, err := gen.Generate(ctx, memoryloop.GenerateInput{
		Analysis:     analysis,
		Sessions:     rows,
		SourceWindow: last,
		MaxRecords:   memoryloop.DefaultMaxRecords,
	})
	if err != nil {
		return fmt.Errorf("generate memory snapshot: %w", err)
	}

	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load memory-loop state: %w", err)
	}

	memoryCfg := cfg.GetMemoryLoopConfig()
	maxInjected := memoryCfg.MaxInjected
	mode := memoryloop.Mode(memoryCfg.Mode)
	activationPolicy := memoryloop.ActivationPolicy(memoryCfg.ActivationPolicy)
	if state.Store != nil {
		if state.Store.Mode != "" {
			mode = state.Store.Mode
		}
		if state.Store.MaxInjected > 0 {
			maxInjected = state.Store.MaxInjected
		}
		if state.Store.ActivationPolicy != "" {
			activationPolicy = state.Store.ActivationPolicy
		}
	}

	now := time.Now().UTC()
	scopeValue := scopeValue(scope)
	renderMemoryLoopRefreshProgress(w, "Reconciling with existing memory history...")
	reconcile := memoryloop.ReconcileGeneratedRecords(
		existingMemoryRecords(state),
		records,
		scopeKindForRefresh(scope),
		scopeValue,
		activationPolicy,
		now,
	)
	reconcile.History.Scope = string(scope.Mode)
	reconcile.History.SourceWindow = last
	refreshHistory := append(existingRefreshHistory(state), reconcile.History)
	reconciledRecords := memoryloop.DeriveOutcomes(reconcile.Records, rows, now)

	state.Store = &memoryloop.Store{
		Version:          1,
		GeneratedAt:      now,
		SourceWindow:     last,
		Scope:            string(scope.Mode),
		ScopeValue:       scopeValue,
		Records:          reconciledRecords,
		Mode:             mode,
		ActivationPolicy: activationPolicy,
		MaxInjected:      maxInjected,
		RefreshHistory:   refreshHistory,
	}

	renderMemoryLoopRefreshProgress(w, "Saving memory store...")
	if err := memoryloop.SaveState(ctx, state); err != nil {
		return fmt.Errorf("save memory-loop state: %w", err)
	}

	activeCount, candidateCount, suppressedCount, archivedCount := countMemoryStatuses(reconciledRecords)
	generatedTitles := make([]string, 0, len(records))
	for _, record := range records {
		if title := strings.TrimSpace(record.Title); title != "" {
			generatedTitles = append(generatedTitles, title)
		}
	}
	renderMemoryLoopRefreshSummary(w, memoryLoopRefreshSummary{
		SessionCount:         len(rows),
		ScopeLabel:           formatMemoryLoopScopeLabel(scope),
		GeneratedCount:       reconcile.History.GeneratedCount,
		ActivatedCount:       reconcile.History.ActivatedCount,
		CandidateCount:       reconcile.History.CandidateCount,
		ActiveCount:          activeCount,
		StoredCandidateCount: candidateCount,
		SuppressedCount:      suppressedCount,
		ArchivedCount:        archivedCount,
		GeneratedTitles:      generatedTitles,
	})
	if usage != nil {
		renderUsageLine(w, usage)
	}

	return nil
}

func existingMemoryRecords(state *memoryloop.State) []memoryloop.MemoryRecord {
	if state == nil || state.Store == nil {
		return nil
	}
	return state.Store.Records
}

func existingRefreshHistory(state *memoryloop.State) []memoryloop.RefreshHistory {
	if state == nil || state.Store == nil {
		return nil
	}
	return append([]memoryloop.RefreshHistory(nil), state.Store.RefreshHistory...)
}

func renderMemoryLoopRefreshProgress(w io.Writer, line string) {
	fmt.Fprintln(w, strings.TrimSpace(line))
}

func renderMemoryLoopRefreshSummary(w io.Writer, summary memoryLoopRefreshSummary) {
	fmt.Fprintf(w, "Memory Loop refreshed from %d sessions\n", summary.SessionCount)
	if summary.ScopeLabel != "" {
		fmt.Fprintf(w, "Scope: %s\n", summary.ScopeLabel)
	}
	fmt.Fprintf(
		w,
		"This refresh: generated %d, activated %d, candidate %d\n",
		summary.GeneratedCount,
		summary.ActivatedCount,
		summary.CandidateCount,
	)
	fmt.Fprintf(
		w,
		"Stored memories: active %d, candidate %d, suppressed %d, archived %d\n",
		summary.ActiveCount,
		storedCandidateCount(summary),
		summary.SuppressedCount,
		summary.ArchivedCount,
	)
	for _, title := range summary.GeneratedTitles {
		fmt.Fprintf(w, "  - %s\n", title)
	}
}

func formatMemoryLoopScopeLabel(scope memoryLoopScope) string {
	label := string(scope.Mode)
	if value := strings.TrimSpace(scopeValue(scope)); value != "" {
		label = fmt.Sprintf("%s (%s)", label, value)
	}
	return label
}

func storedCandidateCount(summary memoryLoopRefreshSummary) int {
	if summary.StoredCandidateCount > 0 {
		return summary.StoredCandidateCount
	}
	return summary.CandidateCount
}

func scopeKindForRefresh(scope memoryLoopScope) memoryloop.ScopeKind {
	switch scope.Mode {
	case memoryLoopScopeMe:
		return memoryloop.ScopeKindMe
	case memoryLoopScopeRepo, memoryLoopScopeBranch:
		return memoryloop.ScopeKindRepo
	default:
		return memoryloop.ScopeKindRepo
	}
}

func runMemoryLoopShow(ctx context.Context, w io.Writer, _ string) error {
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load memory-loop state: %w", err)
	}

	s := termstyle.New(w)
	fmt.Fprintln(w, s.Render(s.Bold, "Entire Memory Loop"))

	if state.Snapshot == nil {
		fmt.Fprintln(w, "No memory snapshot found. Run `entire memory-loop refresh` first.")
		return nil
	}

	snapshot := state.Snapshot
	activeCount, candidateCount, suppressedCount, archivedCount := countMemoryStatuses(snapshot.Records)
	if !snapshot.GeneratedAt.IsZero() {
		fmt.Fprintf(w, "Last refresh: %s\n", snapshot.GeneratedAt.Format(time.RFC3339))
	}
	if snapshot.Scope != "" {
		fmt.Fprintf(w, "Scope: %s", snapshot.Scope)
		if snapshot.ScopeValue != "" {
			fmt.Fprintf(w, " (%s)", snapshot.ScopeValue)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Active memories: %d\n", activeCount)
	fmt.Fprintf(w, "Candidate memories: %d\n", candidateCount)
	fmt.Fprintf(w, "Suppressed memories: %d\n", suppressedCount)
	fmt.Fprintf(w, "Archived memories: %d\n", archivedCount)
	fmt.Fprintf(w, "Mode: %s\n", displayMemoryMode(snapshot.Mode))
	fmt.Fprintf(w, "Activation policy: %s\n", displayActivationPolicy(snapshot.ActivationPolicy))
	fmt.Fprintf(w, "Max injected: %d\n\n", snapshot.MaxInjected)
	renderDetailedMemorySections(w, s, snapshot.Records)
	renderRefreshHistorySection(w, s, snapshot.RefreshHistory)
	renderRecentInjectionsSection(w, s, state.InjectionLogs)

	frequent := frequentMemoryTitles(snapshot, state.InjectionLogs)
	if len(frequent) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, s.SectionRule("Most Injected"))
		for _, item := range frequent[:min(5, len(frequent))] {
			fmt.Fprintf(w, "  - %s\n", item)
		}
	}

	return nil
}

func runMemoryLoopStatus(ctx context.Context, w io.Writer, prompt string, verbose bool) error {
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load memory-loop state: %w", err)
	}

	s := termstyle.New(w)
	fmt.Fprintln(w, s.Render(s.Bold, "Entire Memory Loop"))

	if state.Snapshot == nil {
		fmt.Fprintln(w, "No memory snapshot found. Run `entire memory-loop refresh` first.")
		return nil
	}

	snapshot := state.Snapshot
	activeCount, candidateCount, suppressedCount, archivedCount := countMemoryStatuses(snapshot.Records)
	if !snapshot.GeneratedAt.IsZero() {
		fmt.Fprintf(w, "Last refresh: %s\n", snapshot.GeneratedAt.Format(time.RFC3339))
	}
	if snapshot.Scope != "" {
		fmt.Fprintf(w, "Scope: %s", snapshot.Scope)
		if snapshot.ScopeValue != "" {
			fmt.Fprintf(w, " (%s)", snapshot.ScopeValue)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Mode: %s\n", displayMemoryMode(snapshot.Mode))
	fmt.Fprintf(w, "Activation policy: %s\n", displayActivationPolicy(snapshot.ActivationPolicy))
	fmt.Fprintf(w, "Max injected: %d\n", snapshot.MaxInjected)
	fmt.Fprintf(w, "Active memories: %d\n", activeCount)
	fmt.Fprintf(w, "Candidate memories: %d\n", candidateCount)
	fmt.Fprintf(w, "Suppressed memories: %d\n", suppressedCount)
	fmt.Fprintf(w, "Archived memories: %d\n", archivedCount)

	if prompt != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, s.SectionRule("Prompt Preview"))
		matches := memoryloop.SelectRelevant(*snapshot, prompt, time.Now().UTC())
		if len(matches) == 0 {
			fmt.Fprintln(w, "  No memories would inject for that prompt.")
		} else {
			fmt.Fprintln(w, indentBlock(memoryloop.FormatInjectionBlock(matches), "  "))
			if verbose {
				for _, match := range matches {
					fmt.Fprintf(
						w,
						"  - %s [%s] score=%d reason=%s scope=%s status=%s\n",
						match.Record.ID,
						match.Record.Kind,
						match.Score,
						match.Reason,
						formatMemoryLoopRecordScope(match.Record),
						displayMemoryStatus(match.Record.Status),
					)
				}
			}
		}
	}

	return nil
}

func setMemoryLoopMode(ctx context.Context, w io.Writer, mode memoryloop.Mode) error {
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load memory-loop state: %w", err)
	}
	if err := ensureMemoryLoopStore(ctx, state); err != nil {
		return err
	}
	state.Store.Mode = mode
	if err := memoryloop.SaveState(ctx, state); err != nil {
		return fmt.Errorf("save memory-loop state: %w", err)
	}
	fmt.Fprintf(w, "Memory loop mode: %s\n", mode)
	return nil
}

func setMemoryLoopPolicy(ctx context.Context, w io.Writer, policy memoryloop.ActivationPolicy) error {
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load memory-loop state: %w", err)
	}
	if err := ensureMemoryLoopStore(ctx, state); err != nil {
		return err
	}
	state.Store.ActivationPolicy = policy
	if err := memoryloop.SaveState(ctx, state); err != nil {
		return fmt.Errorf("save memory-loop state: %w", err)
	}
	fmt.Fprintf(w, "Memory loop activation policy: %s\n", policy)
	return nil
}

func runMemoryLoopActivate(ctx context.Context, w io.Writer, id string) error {
	return runMemoryLoopLifecycleAction(ctx, w, id, memoryloop.LifecycleActionActivate)
}

func runMemoryLoopPromote(ctx context.Context, w io.Writer, id string) error {
	return runMemoryLoopLifecycleAction(ctx, w, id, memoryloop.LifecycleActionPromote)
}

func runMemoryLoopSuppress(ctx context.Context, w io.Writer, id string) error {
	return runMemoryLoopLifecycleAction(ctx, w, id, memoryloop.LifecycleActionSuppress)
}

func runMemoryLoopUnsuppress(ctx context.Context, w io.Writer, id string) error {
	return runMemoryLoopLifecycleAction(ctx, w, id, memoryloop.LifecycleActionUnsuppress)
}

func runMemoryLoopArchive(ctx context.Context, w io.Writer, id string) error {
	return runMemoryLoopLifecycleAction(ctx, w, id, memoryloop.LifecycleActionArchive)
}

func runMemoryLoopPrune(ctx context.Context, w io.Writer, now time.Time) error {
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load memory-loop state: %w", err)
	}
	if state.Store == nil {
		return errors.New("no memory store found; run `entire memory-loop refresh` first")
	}

	records, result := memoryloop.PruneRecords(state.Store.Records, now)
	state.Store.Records = records
	if err := memoryloop.SaveState(ctx, state); err != nil {
		return fmt.Errorf("save memory-loop state: %w", err)
	}

	fmt.Fprintf(w, "Pruned memories: archived %d\n", result.ArchivedCount)
	return nil
}

func runMemoryLoopAdd(ctx context.Context, w io.Writer, opts memoryLoopAddOptions) error {
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load memory-loop state: %w", err)
	}
	if err := ensureMemoryLoopStore(ctx, state); err != nil {
		return err
	}

	kind, err := parseMemoryLoopKind(opts.Kind)
	if err != nil {
		return err
	}
	scopeKind, scopeValue, ownerEmail, err := resolveAddScope(ctx, opts.Scope)
	if err != nil {
		return err
	}

	records, added, err := memoryloop.AddManualRecord(state.Store.Records, memoryloop.ManualRecordInput{
		Kind:       kind,
		Title:      opts.Title,
		Body:       opts.Body,
		ScopeKind:  scopeKind,
		ScopeValue: scopeValue,
		OwnerEmail: ownerEmail,
	}, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("add manual memory: %w", err)
	}

	state.Store.Records = records
	if err := memoryloop.SaveState(ctx, state); err != nil {
		return fmt.Errorf("save memory-loop state: %w", err)
	}

	fmt.Fprintf(w, "Added memory: %s\n", added.ID)
	return nil
}

func runMemoryLoopLifecycleAction(ctx context.Context, w io.Writer, id string, action memoryloop.LifecycleAction) error {
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("load memory-loop state: %w", err)
	}
	if state.Store == nil {
		return errors.New("no memory store found; run `entire memory-loop refresh` first")
	}

	records, _, err := memoryloop.TransitionRecordLifecycle(state.Store.Records, id, action, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("transition memory lifecycle: %w", err)
	}
	state.Store.Records = records
	if err := memoryloop.SaveState(ctx, state); err != nil {
		return fmt.Errorf("save memory-loop state: %w", err)
	}

	fmt.Fprintf(w, "%s memory: %s\n", lifecycleActionLabel(action), id)
	return nil
}

func ensureMemoryLoopStore(ctx context.Context, state *memoryloop.State) error {
	if state.Store != nil {
		return nil
	}

	cfg, err := LoadEntireSettings(ctx)
	if err == nil {
		memoryCfg := cfg.GetMemoryLoopConfig()
		state.Store = &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.Mode(memoryCfg.Mode),
			ActivationPolicy: memoryloop.ActivationPolicy(memoryCfg.ActivationPolicy),
			MaxInjected:      memoryCfg.MaxInjected,
		}
		return nil
	}
	return fmt.Errorf("load settings: %w", err)
}

func lifecycleActionLabel(action memoryloop.LifecycleAction) string {
	switch action {
	case memoryloop.LifecycleActionActivate:
		return "Activated"
	case memoryloop.LifecycleActionPromote:
		return "Promoted"
	case memoryloop.LifecycleActionSuppress:
		return "Suppressed"
	case memoryloop.LifecycleActionUnsuppress:
		return "Unsuppressed"
	case memoryloop.LifecycleActionArchive:
		return "Archived"
	default:
		return "Updated"
	}
}

func parseMemoryLoopKind(raw string) (memoryloop.Kind, error) {
	kind := memoryloop.Kind(strings.ToLower(strings.TrimSpace(raw)))
	switch kind {
	case memoryloop.KindRepoRule,
		memoryloop.KindWorkflowRule,
		memoryloop.KindAgentInstruction,
		memoryloop.KindSkillPatch,
		memoryloop.KindAntiPattern:
		return kind, nil
	default:
		return "", fmt.Errorf("invalid memory kind: %s", raw)
	}
}

func resolveAddScope(ctx context.Context, rawScope string) (memoryloop.ScopeKind, string, string, error) {
	switch memoryLoopScopeMode(strings.ToLower(strings.TrimSpace(rawScope))) {
	case "", memoryLoopScopeMe:
		author, err := GetGitAuthor(ctx)
		if err != nil {
			return "", "", "", fmt.Errorf("resolve git author: %w", err)
		}
		return memoryloop.ScopeKindMe, author.Email, author.Email, nil
	case memoryLoopScopeRepo:
		return memoryloop.ScopeKindRepo, "", "", nil
	case memoryLoopScopeBranch:
		return "", "", "", fmt.Errorf("invalid memory scope: %s", rawScope)
	default:
		return "", "", "", fmt.Errorf("invalid memory scope: %s", rawScope)
	}
}

func recentLogs(logs []memoryloop.InjectionLog, limit int) []memoryloop.InjectionLog {
	if len(logs) <= limit {
		return logs
	}
	return logs[len(logs)-limit:]
}

func renderDetailedMemorySections(w io.Writer, s termstyle.Styles, records []memoryloop.MemoryRecord) {
	renderMemorySection(w, s, "Active Memories", filterMemoryRecordsByStatus(records, memoryloop.StatusActive))
	renderMemorySection(w, s, "Candidate Memories", filterMemoryRecordsByStatus(records, memoryloop.StatusCandidate))
	renderMemorySection(w, s, "Suppressed Memories", filterMemoryRecordsByStatus(records, memoryloop.StatusSuppressed))
	renderMemorySection(w, s, "Archived Memories", filterMemoryRecordsByStatus(records, memoryloop.StatusArchived))
}

func renderMemorySection(w io.Writer, s termstyle.Styles, title string, records []memoryloop.MemoryRecord) {
	fmt.Fprintln(w, s.SectionRule(title))
	if len(records) == 0 {
		fmt.Fprintln(w, "  No memories.")
		fmt.Fprintln(w)
		return
	}
	for _, record := range records {
		fmt.Fprintf(w, "  - %s [%s] (%s)\n", record.Title, record.Kind, displayMemoryStatus(record.Status))
		if record.Body != "" {
			fmt.Fprintf(w, "    %s\n", record.Body)
		}
	}
	fmt.Fprintln(w)
}

func renderRefreshHistorySection(w io.Writer, s termstyle.Styles, history []memoryloop.RefreshHistory) {
	fmt.Fprintln(w, s.SectionRule("Recent Refreshes"))
	if len(history) == 0 {
		fmt.Fprintln(w, "  No refresh history recorded yet.")
		fmt.Fprintln(w)
		return
	}
	for _, item := range recentRefreshHistory(history, 10) {
		fmt.Fprintf(
			w,
			"  - %s  %s%s  generated %d, activated %d, candidate %d\n",
			item.At.Format(time.RFC3339),
			item.Scope,
			formatOptionalScopeValue(item.ScopeValue),
			item.GeneratedCount,
			item.ActivatedCount,
			item.CandidateCount,
		)
	}
	fmt.Fprintln(w)
}

func renderRecentInjectionsSection(w io.Writer, s termstyle.Styles, logs []memoryloop.InjectionLog) {
	fmt.Fprintln(w, s.SectionRule("Recent Injections"))
	if len(logs) == 0 {
		fmt.Fprintln(w, "  No injections recorded yet.")
		return
	}
	for _, entry := range recentLogs(logs, 10) {
		fmt.Fprintf(w, "  - %s  %s\n", entry.InjectedAt.Format(time.RFC3339), entry.SessionID)
		if entry.PromptPreview != "" {
			fmt.Fprintf(w, "    %s\n", entry.PromptPreview)
		}
	}
}

func filterMemoryRecordsByStatus(records []memoryloop.MemoryRecord, status memoryloop.Status) []memoryloop.MemoryRecord {
	filtered := make([]memoryloop.MemoryRecord, 0, len(records))
	for _, record := range records {
		recordStatus := record.Status
		if recordStatus == "" {
			recordStatus = memoryloop.StatusActive
		}
		if recordStatus == status {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func recentRefreshHistory(history []memoryloop.RefreshHistory, limit int) []memoryloop.RefreshHistory {
	if len(history) <= limit {
		return history
	}
	return history[len(history)-limit:]
}

func countMemoryStatuses(records []memoryloop.MemoryRecord) (active, candidate, suppressed, archived int) {
	for _, record := range records {
		switch record.Status {
		case memoryloop.StatusActive:
			active++
		case memoryloop.StatusCandidate:
			candidate++
		case memoryloop.StatusSuppressed:
			suppressed++
		case memoryloop.StatusArchived:
			archived++
		default:
			active++
		}
	}
	return active, candidate, suppressed, archived
}

func mustMarkFlagRequired(cmd *cobra.Command, name string) {
	if err := cmd.MarkFlagRequired(name); err != nil {
		panic(fmt.Sprintf("mark %q required: %v", name, err))
	}
}

func displayMemoryStatus(status memoryloop.Status) string {
	if status == "" {
		return string(memoryloop.StatusActive)
	}
	return string(status)
}

func displayMemoryMode(mode memoryloop.Mode) string {
	if mode == "" {
		return string(memoryloop.ModeOff)
	}
	return string(mode)
}

func displayActivationPolicy(policy memoryloop.ActivationPolicy) string {
	if policy == "" {
		return string(memoryloop.ActivationPolicyReview)
	}
	return string(policy)
}

func frequentMemoryTitles(snapshot *memoryloop.Snapshot, logs []memoryloop.InjectionLog) []string {
	if snapshot == nil {
		return nil
	}
	titleByID := make(map[string]string, len(snapshot.Records))
	for _, record := range snapshot.Records {
		titleByID[record.ID] = record.Title
	}
	counts := make(map[string]int)
	for _, log := range logs {
		for _, id := range log.InjectedMemoryIDs {
			counts[id]++
		}
	}

	type pair struct {
		title string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for id, count := range counts {
		pairs = append(pairs, pair{title: titleByID[id], count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].title < pairs[j].title
	})

	out := make([]string, 0, len(pairs))
	for _, item := range pairs {
		if item.title == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%d)", item.title, item.count))
	}
	return out
}

func indentBlock(text, indent string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func formatOptionalScopeValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return fmt.Sprintf(" (%s)", strings.TrimSpace(value))
}

func formatMemoryLoopRecordScope(record memoryloop.MemoryRecord) string {
	scope := string(record.ScopeKind)
	if scope == "" {
		scope = string(memoryloop.ScopeKindMe)
	}
	if strings.TrimSpace(record.ScopeValue) == "" {
		return scope
	}
	return fmt.Sprintf("%s(%s)", scope, strings.TrimSpace(record.ScopeValue))
}

func resolveMemoryLoopScope(ctx context.Context, scopeArg, branchArg string) (memoryLoopScope, error) {
	scope := memoryLoopScope{Mode: memoryLoopScopeMode(strings.ToLower(strings.TrimSpace(scopeArg)))}
	if scope.Mode == "" {
		scope.Mode = memoryLoopScopeRepo
	}

	switch scope.Mode {
	case memoryLoopScopeRepo:
		return scope, nil
	case memoryLoopScopeBranch:
		if branch := strings.TrimSpace(branchArg); branch != "" {
			scope.Branch = branch
			return scope, nil
		}
		repo, err := openRepository(ctx)
		if err != nil {
			return memoryLoopScope{}, fmt.Errorf("open git repository: %w", err)
		}
		branch := strategy.GetCurrentBranchName(repo)
		if branch == "" {
			return memoryLoopScope{}, errors.New("current HEAD is detached; pass --branch when using --scope branch")
		}
		scope.Branch = branch
		return scope, nil
	case memoryLoopScopeMe:
		repo, err := openRepository(ctx)
		if err != nil {
			return memoryLoopScope{}, fmt.Errorf("open git repository: %w", err)
		}
		_, email := checkpoint.GetGitAuthorFromRepo(repo)
		if email == "" {
			return memoryLoopScope{}, errors.New("could not determine git user.email for --scope me")
		}
		scope.OwnerEmail = email
		return scope, nil
	default:
		return memoryLoopScope{}, fmt.Errorf("invalid scope %q: must be repo, branch, or me", scopeArg)
	}
}

func queryMemoryLoopRows(ctx context.Context, idb *insightsdb.InsightsDB, scope memoryLoopScope, last int) ([]insightsdb.SessionRow, error) {
	switch scope.Mode {
	case memoryLoopScopeRepo:
		rows, err := idb.QueryLastNSessions(ctx, last)
		if err != nil {
			return nil, fmt.Errorf("query repo-scoped sessions: %w", err)
		}
		return rows, nil
	case memoryLoopScopeBranch:
		rows, err := idb.QueryByBranch(ctx, scope.Branch, last)
		if err != nil {
			return nil, fmt.Errorf("query branch-scoped sessions: %w", err)
		}
		return rows, nil
	case memoryLoopScopeMe:
		rows, err := idb.QueryByOwnerEmail(ctx, scope.OwnerEmail, last)
		if err != nil {
			return nil, fmt.Errorf("query owner-scoped sessions: %w", err)
		}
		return rows, nil
	default:
		return nil, fmt.Errorf("unsupported scope mode %q", scope.Mode)
	}
}

func filterMemoryLoopRows(rows []insightsdb.SessionRow, scope memoryLoopScope, limit int) ([]insightsdb.SessionRow, error) {
	filtered := make([]insightsdb.SessionRow, 0, len(rows))
	for _, row := range rows {
		switch scope.Mode {
		case memoryLoopScopeRepo:
			filtered = append(filtered, row)
		case memoryLoopScopeBranch:
			if row.Branch == scope.Branch {
				filtered = append(filtered, row)
			}
		case memoryLoopScopeMe:
			if strings.EqualFold(row.OwnerEmail, scope.OwnerEmail) {
				filtered = append(filtered, row)
			}
		default:
			return nil, fmt.Errorf("unsupported scope mode %q", scope.Mode)
		}
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func scopeValue(scope memoryLoopScope) string {
	switch scope.Mode {
	case memoryLoopScopeRepo:
		return ""
	case memoryLoopScopeBranch:
		return scope.Branch
	case memoryLoopScopeMe:
		return scope.OwnerEmail
	default:
		return ""
	}
}
