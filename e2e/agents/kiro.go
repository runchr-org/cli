//go:build e2e

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func init() {
	if env := os.Getenv("E2E_AGENT"); env != "" && env != "kiro" {
		return
	}
	if _, err := exec.LookPath("kiro-cli-chat"); err != nil {
		return
	}
	Register(&Kiro{})
	// Kiro uses Amazon Q API which may have rate limits
	RegisterGate("kiro", 1)
}

// Kiro implements the Agent interface for Amazon's Kiro CLI.
type Kiro struct{}

func (k *Kiro) Name() string               { return "kiro" }
func (k *Kiro) Binary() string             { return "kiro-cli-chat" }
func (k *Kiro) EntireAgent() string        { return "kiro" }
func (k *Kiro) PromptPattern() string      { return `!>` }
func (k *Kiro) TimeoutMultiplier() float64 { return 1.5 }

func (k *Kiro) IsTransientError(out Output, _ error) bool {
	combined := out.Stdout + out.Stderr
	for _, p := range []string{"overloaded", "rate limit", "503", "529", "throttl"} {
		if strings.Contains(strings.ToLower(combined), p) {
			return true
		}
	}
	return false
}

func (k *Kiro) Bootstrap() error {
	// kiro-cli-chat is the standalone binary that supports headless SIGV4 auth.
	// The desktop app wrapper (kiro-cli) ignores AMAZON_Q_SIGV4 and always
	// forces browser OAuth, so E2E tests must use kiro-cli-chat.
	if os.Getenv("CI") == "" {
		return nil
	}

	if isTruthyEnvValue(os.Getenv("AMAZON_Q_SIGV4")) {
		if err := validateKiroSIGV4Inputs(
			os.Getenv("AWS_REGION"),
			os.Getenv("AWS_ACCESS_KEY_ID"),
			os.Getenv("AWS_SECRET_ACCESS_KEY"),
		); err != nil {
			return fmt.Errorf("kiro-cli-chat sigv4 auth check failed: %w", err)
		}
		return nil
	}

	// Verify login status — fail fast if not authenticated.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kiro-cli-chat", "whoami", "-f", "json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"kiro-cli-chat auth check failed (run `kiro-cli-chat login --use-device-flow`): %s",
			strings.TrimSpace(string(out)),
		)
	}
	if err := validateKiroWhoamiJSON(out); err != nil {
		return fmt.Errorf("kiro-cli-chat auth check failed: %w", err)
	}
	return nil
}

func isTruthyEnvValue(v string) bool {
	value := strings.TrimSpace(v)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	return lower != "0" && lower != "false"
}

func validateKiroSIGV4Inputs(region, accessKeyID, secretAccessKey string) error {
	if strings.TrimSpace(region) == "" {
		return errors.New("AWS_REGION is required when AMAZON_Q_SIGV4 is enabled")
	}
	if strings.TrimSpace(accessKeyID) == "" {
		return errors.New("AWS_ACCESS_KEY_ID is required when AMAZON_Q_SIGV4 is enabled")
	}
	if strings.TrimSpace(secretAccessKey) == "" {
		return errors.New("AWS_SECRET_ACCESS_KEY is required when AMAZON_Q_SIGV4 is enabled")
	}
	return nil
}

func validateKiroWhoamiJSON(out []byte) error {
	var response struct {
		Account json.RawMessage `json:"account"`
	}

	if err := json.Unmarshal(out, &response); err != nil {
		return fmt.Errorf("invalid whoami JSON: %w", err)
	}
	if len(response.Account) == 0 {
		return errors.New("account is missing")
	}
	if strings.EqualFold(strings.TrimSpace(string(response.Account)), "null") {
		return errors.New("account is null")
	}
	return nil
}

func (k *Kiro) RunPrompt(ctx context.Context, dir string, prompt string, opts ...Option) (Output, error) {
	cfg := &runConfig{}
	for _, o := range opts {
		o(cfg)
	}

	timeout := 2 * time.Minute
	if cfg.PromptTimeout > 0 {
		timeout = cfg.PromptTimeout
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// --no-interactive: runs a single prompt and exits; SIGV4 env vars
	// propagate naturally (no tmux env forwarding needed).
	// --trust-all-tools: auto-approve tool use (equivalent to -a in interactive mode).
	// --agent entire: activates the agent profile that contains our hooks.
	args := []string{"chat", "--no-interactive", "--trust-all-tools", "--agent", "entire"}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	args = append(args, prompt)

	// Build display args with quoted prompt for logging.
	displayArgs := make([]string, len(args))
	copy(displayArgs, args)
	displayArgs[len(displayArgs)-1] = fmt.Sprintf("%q", prompt)

	cmd := exec.CommandContext(cmdCtx, k.Binary(), args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return Output{
		Command:  k.Binary() + " " + strings.Join(displayArgs, " "),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, err
}

func (k *Kiro) StartSession(ctx context.Context, dir string) (Session, error) {
	return &KiroSession{kiro: k, dir: dir, ctx: ctx}, nil
}

// KiroSession runs each Send as a separate --no-interactive command.
// SIGV4 auth does not work with the interactive TUI, so this approach
// runs one kiro-cli-chat process per prompt (same pattern as setup-kiro-action).
type KiroSession struct {
	kiro       *Kiro
	dir        string
	ctx        context.Context
	lastOutput string
}

func (s *KiroSession) Send(input string) error {
	out, err := s.kiro.RunPrompt(s.ctx, s.dir, input)
	s.lastOutput = out.Stdout + out.Stderr
	return err
}

func (s *KiroSession) WaitFor(_ string, _ time.Duration) (string, error) {
	return s.lastOutput, nil
}

func (s *KiroSession) Capture() string { return s.lastOutput }
func (s *KiroSession) Close() error    { return nil }
