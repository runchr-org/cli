package trail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/logging"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// RunnerConfig holds configuration for the trail runner.
type RunnerConfig struct {
	// PollInterval is how often to check for new trails in daemon mode.
	PollInterval time.Duration

	// MaxConcurrent is the maximum number of trails to run concurrently.
	// Currently only 1 is supported.
	MaxConcurrent int

	// Daemon enables continuous polling mode.
	Daemon bool

	// AgentName is the agent to use (e.g., "claude-code").
	// The agent must implement the Prompter interface.
	AgentName agent.AgentName

	// Model is the optional model override for the agent.
	Model string

	// Timeout is the maximum time to wait for a single trail execution.
	Timeout time.Duration

	// DryRun prevents actual execution, just prints what would be done.
	DryRun bool

	// MaxAttempts is the maximum number of agent attempts before marking as failed.
	// Each attempt runs the agent, then validates. If validation fails, the agent
	// is run again with feedback about what failed.
	MaxAttempts int

	// EntireCLI is the path to the entire CLI binary for running validation.
	// Defaults to "entire".
	EntireCLI string
}

// DefaultRunnerConfig returns the default runner configuration.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		PollInterval:  30 * time.Second,
		MaxConcurrent: 1,
		Daemon:        false,
		AgentName:     agent.DefaultAgentName,
		Model:         "",
		Timeout:       30 * time.Minute,
		DryRun:        false,
		MaxAttempts:   3,
		EntireCLI:     "entire",
	}
}

// Runner executes trails by discovering open tasks, claiming them,
// creating worktrees, running agents, and updating state.
type Runner struct {
	repo      *git.Repository
	repoRoot  string
	store     *Store
	state     *StateManager
	discovery *Discovery
	worktree  *WorktreeManager
	config    RunnerConfig
}

// NewRunner creates a new trail runner.
func NewRunner(repo *git.Repository, repoRoot string, config RunnerConfig) *Runner {
	return &Runner{
		repo:      repo,
		repoRoot:  repoRoot,
		store:     NewStore(repo),
		state:     NewStateManager(repo),
		discovery: NewDiscovery(repo),
		worktree:  NewWorktreeManager(repoRoot),
		config:    config,
	}
}

// RunResult holds the result of running a single trail.
type RunResult struct {
	Trail            *Trail
	Success          bool
	Output           string
	Error            error
	Duration         time.Duration
	Attempts         int
	ValidationPassed bool
}

// validationResult mirrors the ValidationResult from the validate command.
type validationResult struct {
	Passed   bool          `json:"passed"`
	Duration time.Duration `json:"duration"`
}

// Start runs the trail runner in daemon mode, continuously polling for new trails.
func (r *Runner) Start(ctx context.Context) error {
	logging.Info(ctx, "trail runner started",
		slog.Duration("poll_interval", r.config.PollInterval),
		slog.Bool("daemon", r.config.Daemon),
	)

	for {
		// Run one iteration
		results, err := r.RunOnce(ctx)
		if err != nil {
			logging.Error(ctx, "runner iteration failed", slog.String("error", err.Error()))
		}

		for _, result := range results {
			if result.Success {
				logging.Info(ctx, "trail completed successfully",
					slog.String("trail_id", result.Trail.ID.String()),
					slog.Duration("duration", result.Duration),
				)
			} else {
				logging.Error(ctx, "trail failed",
					slog.String("trail_id", result.Trail.ID.String()),
					slog.String("error", result.Error.Error()),
				)
			}
		}

		if !r.config.Daemon {
			return nil
		}

		// Wait before next poll
		select {
		case <-ctx.Done():
			logging.Info(ctx, "trail runner stopping")
			return fmt.Errorf("trail runner stopped: %w", ctx.Err())
		case <-time.After(r.config.PollInterval):
			// Continue to next iteration
		}
	}
}

// RunOnce performs a single iteration: find open trails and run them.
func (r *Runner) RunOnce(ctx context.Context) ([]RunResult, error) {
	// Find open trails
	openTrails, err := r.discovery.FindOpen(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to find open trails: %w", err)
	}

	if len(openTrails) == 0 {
		logging.Debug(ctx, "no open trails found")
		return nil, nil
	}

	logging.Info(ctx, "found open trails", slog.Int("count", len(openTrails)))

	var results []RunResult

	// Run trails up to max concurrent (currently only 1)
	maxToRun := min(len(openTrails), r.config.MaxConcurrent)
	for i := range maxToRun {
		trail := openTrails[i].Trail
		result := r.RunTrail(ctx, &trail)
		results = append(results, result)
	}

	return results, nil
}

// RunTrail executes a single trail.
func (r *Runner) RunTrail(ctx context.Context, trail *Trail) RunResult {
	start := time.Now()
	result := RunResult{Trail: trail}

	ctx = logging.WithComponent(ctx, "trail-runner")

	logging.Info(ctx, "starting trail execution",
		slog.String("trail_id", trail.ID.String()),
		slog.String("title", trail.Title),
	)

	if r.config.DryRun {
		logging.Info(ctx, "dry run - skipping execution")
		result.Success = true
		result.Output = "dry run"
		result.Duration = time.Since(start)
		return result
	}

	// Create worktree and get initial branch head
	worktreePath, branchHead, err := r.setupWorktree(ctx, trail)
	if err != nil {
		result.Error = fmt.Errorf("failed to setup worktree: %w", err)
		result.Duration = time.Since(start)
		return result
	}

	// Claim the trail
	if err := r.state.Claim(ctx, trail.ID, branchHead); err != nil {
		if errors.Is(err, ErrAlreadyClaimed) {
			logging.Info(ctx, "trail already claimed, skipping",
				slog.String("trail_id", trail.ID.String()),
			)
		}
		result.Error = err
		result.Duration = time.Since(start)
		// Clean up worktree if we couldn't claim
		if removeErr := r.worktree.Remove(ctx, trail.ID); removeErr != nil {
			logging.Warn(ctx, "failed to remove worktree after claim failure",
				slog.String("trail_id", trail.ID.String()),
				slog.String("error", removeErr.Error()),
			)
		}
		return result
	}

	// Run agent with validation loop
	maxAttempts := max(r.config.MaxAttempts, 1)

	var allOutput strings.Builder
	var lastValidationOutput string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result.Attempts = attempt

		logging.Info(ctx, "running agent",
			slog.String("trail_id", trail.ID.String()),
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", maxAttempts),
		)

		// Build prompt - include validation feedback if this is a retry
		prompt := trail.Description
		if attempt > 1 && lastValidationOutput != "" {
			prompt = fmt.Sprintf("%s\n\n---\n\nPrevious attempt failed validation. Please fix the issues and try again.\n\nValidation output:\n%s",
				trail.Description, lastValidationOutput)
		}

		// Run the agent
		output, agentErr := r.runAgentWithPrompt(ctx, prompt, worktreePath)
		allOutput.WriteString(fmt.Sprintf("\n=== Attempt %d ===\n", attempt))
		allOutput.WriteString(output)

		if agentErr != nil {
			result.Error = agentErr
			result.Success = false
			result.Output = allOutput.String()
			logging.Error(ctx, "agent failed",
				slog.String("trail_id", trail.ID.String()),
				slog.Int("attempt", attempt),
				slog.String("error", agentErr.Error()),
			)
			break
		}

		// Run validation
		logging.Info(ctx, "running validation",
			slog.String("trail_id", trail.ID.String()),
			slog.Int("attempt", attempt),
		)

		validationPassed, validationOutput, validationErr := r.runValidation(ctx, worktreePath)
		allOutput.WriteString("\n--- Validation ---\n")
		allOutput.WriteString(validationOutput)

		if validationErr != nil {
			logging.Warn(ctx, "validation command failed",
				slog.String("trail_id", trail.ID.String()),
				slog.String("error", validationErr.Error()),
			)
			// Treat validation command failure as validation failure
			validationPassed = false
		}

		result.ValidationPassed = validationPassed

		if validationPassed {
			logging.Info(ctx, "validation passed",
				slog.String("trail_id", trail.ID.String()),
				slog.Int("attempt", attempt),
			)
			result.Success = true
			result.Output = allOutput.String()
			break
		}

		logging.Info(ctx, "validation failed",
			slog.String("trail_id", trail.ID.String()),
			slog.Int("attempt", attempt),
		)

		lastValidationOutput = validationOutput

		// If this was the last attempt, mark as failed
		if attempt == maxAttempts {
			result.Error = errors.New("validation failed after all attempts")
			result.Success = false
			result.Output = allOutput.String()
		}
	}

	// Get final branch head
	finalHead, headErr := r.worktree.GetHeadCommit(ctx, trail.ID)
	finalHeadHash := plumbing.NewHash(finalHead)
	if headErr != nil || finalHeadHash.IsZero() {
		// Fall back to initial branch head if we can't get the current
		finalHeadHash = branchHead
	}

	// Update state based on result
	if result.Success {
		if err := r.state.Complete(ctx, trail.ID, finalHeadHash); err != nil {
			logging.Error(ctx, "failed to mark trail as completed",
				slog.String("trail_id", trail.ID.String()),
				slog.String("error", err.Error()),
			)
		}
	} else {
		if err := r.state.Fail(ctx, trail.ID, finalHeadHash); err != nil {
			logging.Error(ctx, "failed to mark trail as failed",
				slog.String("trail_id", trail.ID.String()),
				slog.String("error", err.Error()),
			)
		}
	}

	// Clean up worktree
	if err := r.worktree.Remove(ctx, trail.ID); err != nil {
		logging.Warn(ctx, "failed to remove worktree",
			slog.String("trail_id", trail.ID.String()),
			slog.String("error", err.Error()),
		)
	}

	result.Duration = time.Since(start)

	logging.Info(ctx, "trail execution finished",
		slog.String("trail_id", trail.ID.String()),
		slog.Bool("success", result.Success),
		slog.Int("attempts", result.Attempts),
		slog.Duration("duration", result.Duration),
	)

	return result
}

// setupWorktree creates a worktree for the trail and returns the path and branch head.
func (r *Runner) setupWorktree(ctx context.Context, trail *Trail) (string, plumbing.Hash, error) {
	branch := trail.GetBranch()

	// Ensure .worktrees is in .gitignore
	if err := EnsureWorktreesIgnored(r.repoRoot); err != nil {
		logging.Warn(ctx, "failed to ensure worktrees ignored",
			slog.String("error", err.Error()),
		)
	}

	// Create the worktree
	worktreePath, err := r.worktree.Create(ctx, trail.ID, branch, trail.BaseBranch)
	if err != nil {
		return "", plumbing.ZeroHash, err
	}

	// Get the branch head
	headCommit, err := r.worktree.GetHeadCommit(ctx, trail.ID)
	if err != nil {
		return "", plumbing.ZeroHash, fmt.Errorf("failed to get branch head: %w", err)
	}

	return worktreePath, plumbing.NewHash(headCommit), nil
}

// runAgentWithPrompt executes the agent with the given prompt using the Prompter interface.
func (r *Runner) runAgentWithPrompt(ctx context.Context, prompt string, worktreePath string) (string, error) {
	// Get the agent from the registry
	ag, err := agent.Get(r.config.AgentName)
	if err != nil {
		return "", fmt.Errorf("failed to get agent %q: %w", r.config.AgentName, err)
	}

	// Check if the agent implements Prompter
	prompter, ok := ag.(agent.Prompter)
	if !ok {
		return "", fmt.Errorf("agent %q does not support prompting (does not implement agent.Prompter)", r.config.AgentName)
	}

	// Create context with timeout
	if r.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.config.Timeout)
		defer cancel()
	}

	logging.Debug(ctx, "running agent",
		slog.String("agent", string(r.config.AgentName)),
		slog.String("worktree", worktreePath),
	)

	// Build prompt options
	opts := agent.PromptOptions{
		Model:   r.config.Model,
		WorkDir: worktreePath,
		ExtraEnv: []string{
			// Ensure entire hooks are enabled in the worktree
			"ENTIRE_ENABLED=1",
		},
	}

	// Run the prompt
	result, err := prompter.Prompt(ctx, prompt, opts)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("agent timed out after %s", r.config.Timeout)
		}
		return "", fmt.Errorf("agent failed: %w", err)
	}

	return result.Text, nil
}

// runValidation runs "entire validate --json" and returns whether validation passed.
func (r *Runner) runValidation(ctx context.Context, worktreePath string) (bool, string, error) {
	// Create context with timeout for validation (10 minutes)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	entireCLI := r.config.EntireCLI
	if entireCLI == "" {
		entireCLI = "entire"
	}

	//nolint:gosec // entireCLI is from trusted config
	cmd := exec.CommandContext(ctx, entireCLI, "validate", "--json")
	cmd.Dir = worktreePath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Combine output for logging
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	// Try to parse JSON result
	var result validationResult
	if jsonErr := json.Unmarshal(stdout.Bytes(), &result); jsonErr == nil {
		return result.Passed, output, nil
	}

	// If we couldn't parse JSON but command succeeded, assume passed
	if err == nil {
		return true, output, nil
	}

	// Command failed
	if ctx.Err() == context.DeadlineExceeded {
		return false, output, errors.New("validation timed out")
	}

	return false, output, fmt.Errorf("validation failed: %w", err)
}

// RunTrailByID executes a trail by its ID.
func (r *Runner) RunTrailByID(ctx context.Context, id TrailID) (RunResult, error) {
	trail, err := r.store.Get(ctx, id)
	if err != nil {
		return RunResult{}, fmt.Errorf("failed to get trail: %w", err)
	}
	if trail == nil {
		return RunResult{}, fmt.Errorf("trail not found: %s", id)
	}

	return r.RunTrail(ctx, trail), nil
}
