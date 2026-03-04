package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func init() {
	if env := os.Getenv("E2E_AGENT"); env != "" && env != "factoryai-droid" {
		return
	}
	if _, err := exec.LookPath("droid"); err != nil {
		return
	}
	Register(&Droid{})
}

// Droid implements the Agent interface for Factory AI Droid.
type Droid struct{}

func (d *Droid) Name() string               { return "factoryai-droid" }
func (d *Droid) Binary() string             { return "droid" }
func (d *Droid) EntireAgent() string        { return "factoryai-droid" }
func (d *Droid) PromptPattern() string      { return `>` }
func (d *Droid) TimeoutMultiplier() float64 { return 1.5 }

func (d *Droid) IsTransientError(out Output, err error) bool {
	if err == nil {
		return false
	}
	combined := out.Stdout + out.Stderr
	transientPatterns := []string{
		"overloaded",
		"rate limit",
		"529",
		"503",
		"ECONNRESET",
		"ETIMEDOUT",
	}
	for _, p := range transientPatterns {
		if strings.Contains(combined, p) {
			return true
		}
	}
	return false
}

// droidSettings represents the ~/.factory/settings.json structure used for
// BYOK (Bring Your Own Key) configuration.
type droidSettings struct {
	CustomModels           []droidCustomModel    `json:"customModels,omitempty"`
	SessionDefaultSettings *droidSessionDefaults `json:"sessionDefaultSettings,omitempty"`
	AutonomyMode           string                `json:"autonomyMode,omitempty"`
}

type droidCustomModel struct {
	Model           string `json:"model"`
	ID              string `json:"id"`
	Index           int    `json:"index"`
	BaseURL         string `json:"baseUrl"`
	DisplayName     string `json:"displayName"`
	MaxOutputTokens int    `json:"maxOutputTokens"`
	NoImageSupport  bool   `json:"noImageSupport"`
	Provider        string `json:"provider"`
	APIKey          string `json:"apiKey"`
}

type droidSessionDefaults struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

const (
	droidCustomModelDisplayName = "Haiku E2E [Custom]"
	droidCustomModelBaseID      = "claude-haiku-4-5-20251001"
	droidCustomModelID          = "custom:Haiku-E2E-[Custom]-0"
)

func (d *Droid) Bootstrap() error {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".factory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	settingsPath := filepath.Join(dir, "settings.json")

	// Read existing settings to merge (hooks may already be configured
	// in the repo-local .factory/settings.json, but the global config
	// at ~/.factory/settings.json might have other pre-existing entries).
	var settings droidSettings
	if data, readErr := os.ReadFile(settingsPath); readErr == nil {
		// Best-effort merge: ignore parse errors and start fresh
		_ = json.Unmarshal(data, &settings)
	}

	// Replace or add the BYOK model entry.
	byokModel := droidCustomModel{
		Model:           droidCustomModelBaseID,
		ID:              droidCustomModelID,
		Index:           0,
		BaseURL:         "https://api.anthropic.com",
		DisplayName:     droidCustomModelDisplayName,
		MaxOutputTokens: 8192,
		NoImageSupport:  false,
		Provider:        "anthropic",
		APIKey:          apiKey,
	}

	found := false
	for i, m := range settings.CustomModels {
		if m.Model == byokModel.Model {
			settings.CustomModels[i] = byokModel
			found = true
			break
		}
	}
	if !found {
		settings.CustomModels = append(settings.CustomModels, byokModel)
	}

	// Set the default model for interactive sessions.
	settings.SessionDefaultSettings = &droidSessionDefaults{
		Model:           droidCustomModelID,
		ReasoningEffort: "none",
	}

	// Auto-high allows all tool use without permission prompts (for E2E tests).
	settings.AutonomyMode = "auto-high"

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	return os.WriteFile(settingsPath, data, 0o644)
}

func (d *Droid) RunPrompt(ctx context.Context, dir string, prompt string, opts ...Option) (Output, error) {
	cfg := &runConfig{}
	for _, o := range opts {
		o(cfg)
	}

	// Model is configured via sessionDefaultSettings in ~/.factory/settings.json.
	args := []string{"exec", "--skip-permissions-unsafe", prompt}
	displayArgs := []string{"exec", "--skip-permissions-unsafe", fmt.Sprintf("%q", prompt)}

	cmd := exec.CommandContext(ctx, d.Binary(), args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.Env = filterEnv(os.Environ(), "ENTIRE_TEST_TTY")
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
		Command:  d.Binary() + " " + strings.Join(displayArgs, " "),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, err
}

func (d *Droid) StartSession(ctx context.Context, dir string) (Session, error) {
	name := fmt.Sprintf("droid-test-%d", time.Now().UnixNano())
	// Model is configured via sessionDefaultSettings in ~/.factory/settings.json.
	// Interactive mode doesn't support --model or --skip-permissions-unsafe (exec-only flags).
	s, err := NewTmuxSession(name, dir, []string{"ENTIRE_TEST_TTY"}, d.Binary())
	if err != nil {
		return nil, err
	}

	// Wait for the interactive prompt indicator.
	if _, err := s.WaitFor(`>`, 30*time.Second); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("waiting for startup prompt: %w", err)
	}
	s.stableAtSend = ""

	return s, nil
}
