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
	"time"
)

func init() {
	if env := os.Getenv("E2E_AGENT"); env != "" && env != "kiro" {
		return
	}
	if _, err := exec.LookPath("kiro-cli"); err != nil {
		return
	}
	Register(&Kiro{})
	// Kiro uses Amazon Q API which may have rate limits
	RegisterGate("kiro", 1)
}

// Kiro implements the Agent interface for Amazon's Kiro CLI.
type Kiro struct{}

func (k *Kiro) Name() string               { return "kiro" }
func (k *Kiro) Binary() string             { return "kiro-cli" }
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
	// kiro-cli uses Amazon Q / Builder ID auth.
	// On CI, ensure auth is available; locally, auth is handled by the desktop app.
	if os.Getenv("CI") == "" {
		return nil
	}

	if isTruthyEnvValue(os.Getenv("AMAZON_Q_SIGV4")) {
		if err := validateKiroSIGV4Inputs(
			os.Getenv("AWS_REGION"),
			os.Getenv("AWS_ACCESS_KEY_ID"),
			os.Getenv("AWS_SECRET_ACCESS_KEY"),
		); err != nil {
			return fmt.Errorf("kiro-cli sigv4 auth check failed: %w", err)
		}
		return nil
	}

	// Verify login status — fail fast if not authenticated.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kiro-cli", "whoami", "-f", "json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"kiro-cli auth check failed (run `kiro-cli login --use-device-flow`): %s",
			strings.TrimSpace(string(out)),
		)
	}
	if err := validateKiroWhoamiJSON(out); err != nil {
		return fmt.Errorf("kiro-cli auth check failed: %w", err)
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

	// kiro-cli --no-interactive mode does not fire agent hooks, so we must
	// use interactive (tmux) mode and send the prompt through the TUI.
	timeout := 2 * time.Minute
	if cfg.PromptTimeout > 0 {
		timeout = cfg.PromptTimeout
	}

	// --agent entire activates the agent profile that contains our hooks
	args := []string{"chat", "-a", "--agent", "entire"}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}

	name := fmt.Sprintf("kiro-run-%d", time.Now().UnixNano())
	cmdArgs := append([]string{"env"}, kiroAuthEnvArgs()...)
	cmdArgs = append(cmdArgs, k.Binary())
	cmdArgs = append(cmdArgs, args...)
	s, err := NewTmuxSession(name, dir, []string{"ENTIRE_TEST_TTY"}, cmdArgs[0], cmdArgs[1:]...)
	if err != nil {
		return Output{}, fmt.Errorf("starting kiro session: %w", err)
	}
	defer func() { _ = s.Close() }()

	// Wait for initial prompt — kiro-cli TUI shows "!>" in trust-all mode
	if _, err := s.WaitFor(k.PromptPattern(), 30*time.Second); err != nil {
		content := s.Capture()
		return Output{
			Command: k.Binary() + " " + strings.Join(args, " "),
			Stderr:  content,
		}, fmt.Errorf("waiting for kiro startup: %w", err)
	}
	s.stableAtSend = ""

	// Send the prompt
	if err := s.Send(prompt); err != nil {
		return Output{}, fmt.Errorf("sending prompt to kiro: %w", err)
	}

	// Wait for "Credits:" which only appears after the agent finishes a response.
	// More reliable than PromptPattern for single-prompt mode since it uniquely
	// identifies response completion without needing to distinguish echoed input.
	content, err := s.WaitFor(`Credits:`, timeout)
	exitCode := 0
	if err != nil {
		exitCode = -1
	}

	return Output{
		Command:  k.Binary() + " " + strings.Join(args, " ") + " " + fmt.Sprintf("%q", prompt),
		Stdout:   content,
		ExitCode: exitCode,
	}, err
}

func (k *Kiro) StartSession(ctx context.Context, dir string) (Session, error) {
	name := fmt.Sprintf("kiro-test-%d", time.Now().UnixNano())
	cmdArgs := append([]string{"env"}, kiroAuthEnvArgs()...)
	cmdArgs = append(cmdArgs, k.Binary(), "chat", "-a", "--agent", "entire")
	s, err := NewTmuxSession(name, dir, []string{"ENTIRE_TEST_TTY"}, cmdArgs[0], cmdArgs[1:]...)
	if err != nil {
		return nil, err
	}

	// Wait for the prompt indicator — kiro-cli TUI shows "!>" in trust-all mode
	if _, err := s.WaitFor(k.PromptPattern(), 15*time.Second); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("waiting for kiro startup prompt: %w", err)
	}
	s.stableAtSend = ""

	return &KiroSession{TmuxSession: s}, nil
}

// kiroAuthEnvArgs returns KEY=VALUE strings for AWS SIGV4 auth env vars
// that are set in the current process. These must be forwarded explicitly
// through tmux via the `env` command since tmux sessions inherit the
// server's environment, not the client's.
func kiroAuthEnvArgs() []string {
	var args []string
	for _, key := range []string{
		"AMAZON_Q_SIGV4",
		"AWS_REGION",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
	} {
		if v := os.Getenv(key); v != "" {
			args = append(args, key+"="+v)
		}
	}
	return args
}

// KiroSession wraps TmuxSession for Kiro's interactive sessions.
// After Send, WaitFor uses an end-of-line pattern to avoid matching
// the echoed "!>" prompt in the input line.
type KiroSession struct {
	*TmuxSession
}

func (s *KiroSession) WaitFor(pattern string, timeout time.Duration) (string, error) {
	// For initial waits (before any Send), use the original pattern ("!>").
	if s.stableAtSend == "" {
		return s.TmuxSession.WaitFor(pattern, timeout)
	}
	// After a Send, the echoed input line contains "!>" with text after it
	// (e.g., "[entire] 3% !> now commit it"). The real prompt has "!>" at
	// end-of-line (e.g., "[entire] 3% !>"). Match only the latter.
	return s.TmuxSession.WaitFor(`(?m)!>\s*$`, timeout)
}
