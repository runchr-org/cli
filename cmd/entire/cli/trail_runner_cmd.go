package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/trail"

	"github.com/spf13/cobra"
)

func newTrailRunnerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "trail-runner",
		Short:  "Run the trail execution engine",
		Hidden: true, // Experimental feature
		Long: `Trail runner discovers and executes open trails.

The runner:
1. Finds trails in 'open' state
2. Claims them atomically (prevents concurrent execution)
3. Creates a git worktree for isolated execution
4. Runs the configured agent with the trail description
5. Marks the trail as completed or failed
6. Cleans up the worktree

Examples:
  # Run once (process one open trail)
  entire trail-runner run-once

  # Run in daemon mode (continuously poll for trails)
  entire trail-runner start --daemon --poll-interval 30s

  # Run a specific trail
  entire trail run abc123def456`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newTrailRunnerStartCmd())
	cmd.AddCommand(newTrailRunnerRunOnceCmd())
	cmd.AddCommand(newTrailRunCmd())

	return cmd
}

func newTrailRunnerStartCmd() *cobra.Command {
	var (
		daemon       bool
		pollInterval time.Duration
		agentName    string
		model        string
		timeout      time.Duration
		dryRun       bool
		maxAttempts  int
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the trail runner",
		Long: `Start the trail runner to process open trails.

In daemon mode, the runner continuously polls for new trails.
Without daemon mode, it processes available trails once and exits.

After each agent run, validation is performed using 'entire validate'.
If validation fails, the agent is run again with feedback about the
failures, up to --max-attempts times.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTrailRunnerStart(cmd, daemon, pollInterval, agentName, model, timeout, dryRun, maxAttempts)
		},
	}

	cmd.Flags().BoolVar(&daemon, "daemon", false, "Run in daemon mode (continuous polling)")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 30*time.Second, "Polling interval in daemon mode")
	cmd.Flags().StringVar(&agentName, "agent", string(agent.DefaultAgentName), "Agent to use (e.g., claude-code)")
	cmd.Flags().StringVar(&model, "model", "", "Model override for the agent")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Timeout per trail execution")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be done without executing")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 3, "Max agent attempts before marking as failed")

	return cmd
}

func newTrailRunnerRunOnceCmd() *cobra.Command {
	var (
		agentName   string
		model       string
		timeout     time.Duration
		dryRun      bool
		maxAttempts int
	)

	cmd := &cobra.Command{
		Use:   "run-once",
		Short: "Run once and exit",
		Long: `Process available open trails once and exit.

After each agent run, validation is performed using 'entire validate'.
If validation fails, the agent is run again with feedback about the
failures, up to --max-attempts times.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTrailRunnerRunOnce(cmd, agentName, model, timeout, dryRun, maxAttempts)
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", string(agent.DefaultAgentName), "Agent to use (e.g., claude-code)")
	cmd.Flags().StringVar(&model, "model", "", "Model override for the agent")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Timeout per trail execution")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be done without executing")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 3, "Max agent attempts before marking as failed")

	return cmd
}

func newTrailRunCmd() *cobra.Command {
	var (
		agentName   string
		model       string
		timeout     time.Duration
		dryRun      bool
		maxAttempts int
	)

	cmd := &cobra.Command{
		Use:   "run <trail-id>",
		Short: "Run a specific trail",
		Long: `Run a specific trail by ID.

This command claims the trail, creates a worktree, runs the agent,
and updates the trail state based on the result.

After each agent run, validation is performed using 'entire validate'.
If validation fails, the agent is run again with feedback about the
failures, up to --max-attempts times.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTrailRun(cmd, args[0], agentName, model, timeout, dryRun, maxAttempts)
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", string(agent.DefaultAgentName), "Agent to use (e.g., claude-code)")
	cmd.Flags().StringVar(&model, "model", "", "Model override for the agent")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Timeout per trail execution")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be done without executing")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 3, "Max agent attempts before marking as failed")

	return cmd
}

func runTrailRunnerStart(cmd *cobra.Command, daemon bool, pollInterval time.Duration, agentName, model string, timeout time.Duration, dryRun bool, maxAttempts int) error {
	repo, err := openRepository()
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	repoRoot, err := paths.RepoRoot()
	if err != nil {
		return fmt.Errorf("failed to get repository root: %w", err)
	}

	config := trail.RunnerConfig{
		PollInterval:  pollInterval,
		MaxConcurrent: 1,
		Daemon:        daemon,
		AgentName:     agent.AgentName(agentName),
		Model:         model,
		Timeout:       timeout,
		DryRun:        dryRun,
		MaxAttempts:   maxAttempts,
	}

	runner := trail.NewRunner(repo, repoRoot, config)
	ctx := context.Background()

	if daemon {
		fmt.Fprintf(cmd.OutOrStdout(), "Starting trail runner in daemon mode (poll interval: %s)\n", pollInterval)
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Starting trail runner...")
	}

	if err := runner.Start(ctx); err != nil {
		return fmt.Errorf("runner failed: %w", err)
	}

	return nil
}

func runTrailRunnerRunOnce(cmd *cobra.Command, agentName, model string, timeout time.Duration, dryRun bool, maxAttempts int) error {
	repo, err := openRepository()
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	repoRoot, err := paths.RepoRoot()
	if err != nil {
		return fmt.Errorf("failed to get repository root: %w", err)
	}

	config := trail.RunnerConfig{
		MaxConcurrent: 1,
		Daemon:        false,
		AgentName:     agent.AgentName(agentName),
		Model:         model,
		Timeout:       timeout,
		DryRun:        dryRun,
		MaxAttempts:   maxAttempts,
	}

	runner := trail.NewRunner(repo, repoRoot, config)
	ctx := context.Background()

	results, err := runner.RunOnce(ctx)
	if err != nil {
		return fmt.Errorf("runner failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No open trails to run.")
		return nil
	}

	for _, result := range results {
		if result.Success {
			fmt.Fprintf(cmd.OutOrStdout(), "Trail %s completed successfully (%s, %d attempts)\n",
				result.Trail.ID.Short(), result.Duration.Round(time.Second), result.Attempts)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Trail %s failed after %d attempts: %v\n",
				result.Trail.ID.Short(), result.Attempts, result.Error)
		}
	}

	return nil
}

func runTrailRun(cmd *cobra.Command, idStr, agentName, model string, timeout time.Duration, dryRun bool, maxAttempts int) error {
	id, err := trail.NewTrailID(idStr)
	if err != nil {
		return err //nolint:wrapcheck // validation error
	}

	repo, err := openRepository()
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	repoRoot, err := paths.RepoRoot()
	if err != nil {
		return fmt.Errorf("failed to get repository root: %w", err)
	}

	config := trail.RunnerConfig{
		MaxConcurrent: 1,
		Daemon:        false,
		AgentName:     agent.AgentName(agentName),
		Model:         model,
		Timeout:       timeout,
		DryRun:        dryRun,
		MaxAttempts:   maxAttempts,
	}

	runner := trail.NewRunner(repo, repoRoot, config)
	ctx := context.Background()

	fmt.Fprintf(cmd.OutOrStdout(), "Running trail: %s (max %d attempts)\n", id, maxAttempts)

	result, err := runner.RunTrailByID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to run trail: %w", err)
	}

	if result.Success {
		fmt.Fprintf(cmd.OutOrStdout(), "\nTrail completed successfully (%s, %d attempts)\n",
			result.Duration.Round(time.Second), result.Attempts)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "\nTrail failed after %d attempts: %v\n", result.Attempts, result.Error)
		return NewSilentError(result.Error)
	}

	return nil
}
