package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	checkpointid "github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/llmcli"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/permissions"
	"github.com/entireio/cli/cmd/entire/cli/summarize"
	"github.com/entireio/cli/cmd/entire/cli/termstyle"
	"github.com/spf13/cobra"
)

func newPermissionsCmd() *cobra.Command {
	var last int
	var outputJSON bool

	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Suggest conservative agent permission entries based on recent sessions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			if checkDisabledGuard(ctx, w) {
				return nil
			}

			return runPermissions(ctx, w, last, outputJSON)
		},
	}

	cmd.Flags().IntVar(&last, "last", 10, "number of recent sessions to inspect")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "output as JSON instead of styled terminal output")

	return cmd
}

func runPermissions(ctx context.Context, w io.Writer, last int, outputJSON bool) error {
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

	refreshCacheIfStale(ctx, idb) //nolint:errcheck,gosec // Non-fatal; continue with stale cache

	rows, err := idb.QueryLastNSessions(ctx, last)
	if err != nil {
		return fmt.Errorf("query sessions: %w", err)
	}

	observations := extractPermissionObservations(ctx, rows)
	input := permissions.AnalyzeCommands(observations, permissions.DiscoverRepoFacts(worktreeRoot), detectedPermissionAgents(ctx, rows))
	report, usage, err := (&permissions.Reviewer{Runner: &llmcli.Runner{}}).Review(ctx, input)
	if err != nil {
		return fmt.Errorf("review permission candidates: %w", err)
	}

	if outputJSON {
		return renderPermissionsJSON(w, *report)
	}
	renderPermissionsTerminal(w, *report)
	if usage != nil {
		renderUsageLine(w, usage)
	}
	return nil
}

func extractPermissionObservations(ctx context.Context, rows []insightsdb.SessionRow) []permissions.ObservedCommand {
	repo, err := openRepository(ctx)
	if err != nil {
		logging.Debug(ctx, "permissions: open repository failed", "error", err)
		return nil
	}
	store := checkpoint.NewGitStore(repo)

	observations := make([]permissions.ObservedCommand, 0, len(rows))
	for _, row := range rows {
		cpID, parseErr := checkpointid.NewCheckpointID(row.CheckpointID)
		if parseErr != nil {
			logging.Debug(ctx, "permissions: invalid checkpoint ID",
				"checkpoint_id", row.CheckpointID, "error", parseErr)
			continue
		}

		content, readErr := store.ReadSessionContent(ctx, cpID, row.SessionIndex)
		if readErr != nil {
			logging.Debug(ctx, "permissions: read session content failed",
				"checkpoint_id", row.CheckpointID, "session_index", row.SessionIndex, "error", readErr)
			continue
		}
		if len(content.Transcript) == 0 {
			continue
		}

		condensed, buildErr := summarize.BuildCondensedTranscriptFromBytes(content.Transcript, content.Metadata.Agent)
		if buildErr != nil {
			logging.Debug(ctx, "permissions: condense transcript failed",
				"checkpoint_id", row.CheckpointID, "agent", content.Metadata.Agent, "error", buildErr)
			continue
		}

		toolUses := make([]permissions.TranscriptToolUse, 0, len(condensed))
		for _, entry := range condensed {
			if entry.Type != summarize.EntryTypeTool {
				continue
			}
			toolUses = append(toolUses, permissions.TranscriptToolUse{
				ToolName: entry.ToolName,
				Detail:   entry.ToolDetail,
			})
		}

		agentName := row.Agent
		if strings.TrimSpace(agentName) == "" {
			agentName = string(content.Metadata.Agent)
		}

		for _, command := range permissions.ExtractShellCommands(toolUses) {
			observations = append(observations, permissions.ObservedCommand{
				Agent:        agentName,
				CheckpointID: row.CheckpointID,
				SessionID:    row.SessionID,
				Command:      command,
			})
		}
	}

	return observations
}

func detectedPermissionAgents(ctx context.Context, rows []insightsdb.SessionRow) []string {
	seen := make(map[string]struct{})
	var agentsOut []string

	for _, ag := range agent.DetectAll(ctx) {
		name := strings.TrimSpace(string(ag.Type()))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		agentsOut = append(agentsOut, name)
	}

	for _, row := range rows {
		name := strings.TrimSpace(row.Agent)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		agentsOut = append(agentsOut, name)
	}

	sort.Strings(agentsOut)
	return agentsOut
}

func renderPermissionsJSON(w io.Writer, report permissions.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("marshal permissions report: %w", err)
	}
	return nil
}

func renderPermissionsTerminal(w io.Writer, report permissions.Report) {
	s := termstyle.New(w)

	fmt.Fprintln(w, s.SectionRule("Permissions Suggestions"))
	fmt.Fprintf(w, "%s Analyzed %d recent sessions and %d shell commands\n",
		s.Render(s.Dim, "i"), report.SessionsAnalyzed, report.CommandsObserved)

	if len(report.Agents) == 0 {
		fmt.Fprintf(w, "\n%s No recent agent sessions found.\n", s.Render(s.Dim, "i"))
		return
	}

	for _, agentReport := range report.Agents {
		fmt.Fprintf(w, "\n%s\n", s.Render(s.Bold, agentReport.Agent))
		if len(agentReport.Suggestions) == 0 {
			fmt.Fprintf(w, "  %s no conservative suggestions from recent sessions\n", s.Render(s.Dim, "i"))
			continue
		}

		for _, suggestion := range agentReport.Suggestions {
			fmt.Fprintf(w, "  %s %s\n", s.Render(s.Green, "✓"), suggestion.Rule)
			fmt.Fprintf(w, "    safe because: %s\n", suggestion.Reason)
			fmt.Fprintf(w, "    observed in %d checkpoint(s)\n", suggestion.Count)
			if len(suggestion.Examples) > 0 {
				fmt.Fprintf(w, "    example: %s\n", suggestion.Examples[0])
			}
		}
	}
}
