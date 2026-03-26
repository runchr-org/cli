// Package settings provides configuration loading for Entire.
// This package is separate from cli to allow strategy package to import it
// without creating an import cycle (cli imports strategy).
package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

const (
	// EntireSettingsFile is the path to the Entire settings file
	EntireSettingsFile = ".entire/settings.json"
	// EntireSettingsLocalFile is the path to the local settings override file (not committed)
	EntireSettingsLocalFile = ".entire/settings.local.json"
)

const (
	memoryLoopModeOff                = "off"
	memoryLoopModeManual             = "manual"
	memoryLoopModeAuto               = "auto"
	memoryLoopActivationPolicyReview = "review"
	memoryLoopActivationPolicyAuto   = "auto"
)

// Commit linking mode constants.
const (
	// CommitLinkingAlways auto-links commits to sessions without prompting.
	CommitLinkingAlways = "always"
	// CommitLinkingPrompt prompts the user on each commit (default for existing users).
	CommitLinkingPrompt = "prompt"
)

// EntireSettings represents the .entire/settings.json configuration
type EntireSettings struct {

	// Enabled indicates whether Entire is active. When false, CLI commands
	// show a disabled message and hooks exit silently. Defaults to true.
	Enabled bool `json:"enabled"`

	// LocalDev indicates whether to use "go run" instead of the "entire" binary
	// This is used for development when the binary is not installed
	LocalDev bool `json:"local_dev,omitempty"`

	// LogLevel sets the logging verbosity (debug, info, warn, error).
	// Can be overridden by ENTIRE_LOG_LEVEL environment variable.
	// Defaults to "info".
	LogLevel string `json:"log_level,omitempty"`

	// StrategyOptions contains strategy-specific configuration
	StrategyOptions map[string]any `json:"strategy_options,omitempty"`

	// AbsoluteGitHookPath embeds the full binary path in git hooks instead of
	// bare "entire". This is needed for GUI git clients (Xcode, Tower, etc.)
	// that don't source shell profiles and can't find "entire" on PATH.
	AbsoluteGitHookPath bool `json:"absolute_git_hook_path,omitempty"`

	// Telemetry controls anonymous usage analytics.
	// nil = not asked yet (show prompt), true = opted in, false = opted out
	Telemetry *bool `json:"telemetry,omitempty"`

	// Redaction configures PII redaction behavior for transcripts and metadata.
	Redaction *RedactionSettings `json:"redaction,omitempty"`

	// CommitLinking controls how commits are linked to agent sessions.
	// "always" = auto-link without prompting, "prompt" = ask on each commit.
	// Defaults to "prompt" (preserves existing user behavior).
	CommitLinking string `json:"commit_linking,omitempty"`

	// ExternalAgents enables discovery and registration of external agent
	// plugins (entire-agent-* binaries on $PATH). Defaults to false.
	ExternalAgents bool `json:"external_agents,omitempty"`

	// EvolveConfig configures the automatic evolution loop.
	// When enabled, automatically suggests improvements after N sessions.
	EvolveConfig *EvolveSettings `json:"evolve,omitempty"`

	// MemoryLoopConfig configures the repo-scoped memory loop PoC.
	MemoryLoopConfig *MemoryLoopSettings `json:"memory_loop,omitempty"`

	// Deprecated: no longer used. Exists to tolerate old settings files
	// that still contain "strategy": "auto-commit" or similar.
	Strategy string `json:"strategy,omitempty"`
}

// RedactionSettings configures redaction behavior beyond the default secret detection.
type RedactionSettings struct {
	PII *PIISettings `json:"pii,omitempty"`
}

// PIISettings configures PII detection categories.
// When Enabled is true, email and phone default to true; address defaults to false.
type PIISettings struct {
	Enabled        bool              `json:"enabled"`
	Email          *bool             `json:"email,omitempty"`
	Phone          *bool             `json:"phone,omitempty"`
	Address        *bool             `json:"address,omitempty"`
	CustomPatterns map[string]string `json:"custom_patterns,omitempty"`
}

// EvolveSettings configures the automatic evolution loop that suggests
// context file improvements after a configurable number of sessions.
type EvolveSettings struct {
	// Enabled activates the evolution loop. Defaults to false (opt-in).
	Enabled bool `json:"enabled"`

	// SessionThreshold is the number of sessions before auto-suggesting
	// improvements. Defaults to 5.
	SessionThreshold int `json:"session_threshold,omitempty"`
}

// MemoryLoopSettings configures the repo-scoped memory loop PoC.
type MemoryLoopSettings struct {
	Enabled                bool   `json:"enabled"`
	Mode                   string `json:"mode,omitempty"`
	ActivationPolicy       string `json:"activation_policy,omitempty"`
	ClaudeInjectionEnabled *bool  `json:"claude_injection_enabled,omitempty"`
	MaxInjected            int    `json:"max_injected,omitempty"`
	DefaultRefreshWindow   int    `json:"default_refresh_window,omitempty"`
}

// MemoryLoopConfig is the effective memory-loop configuration with defaults applied.
type MemoryLoopConfig struct {
	Enabled              bool
	Mode                 string
	ActivationPolicy     string
	MaxInjected          int
	DefaultRefreshWindow int
}

// GetEvolveConfig returns the evolution loop configuration with defaults applied.
func (s *EntireSettings) GetEvolveConfig() EvolveSettings {
	if s.EvolveConfig == nil {
		return EvolveSettings{SessionThreshold: 5}
	}
	cfg := *s.EvolveConfig
	if cfg.SessionThreshold == 0 {
		cfg.SessionThreshold = 5
	}
	return cfg
}

// GetMemoryLoopConfig returns memory-loop configuration with defaults applied.
func (s *EntireSettings) GetMemoryLoopConfig() MemoryLoopConfig {
	cfg := MemoryLoopConfig{
		MaxInjected:          3,
		DefaultRefreshWindow: 20,
		Mode:                 memoryLoopModeOff,
		ActivationPolicy:     memoryLoopActivationPolicyReview,
	}
	if s.MemoryLoopConfig == nil {
		return cfg
	}
	if s.MemoryLoopConfig.Mode != "" {
		cfg.Enabled = true
		cfg.Mode = s.MemoryLoopConfig.Mode
	} else {
		cfg.Enabled = s.MemoryLoopConfig.Enabled
	}
	if s.MemoryLoopConfig.ActivationPolicy != "" {
		cfg.ActivationPolicy = s.MemoryLoopConfig.ActivationPolicy
	}
	if s.MemoryLoopConfig.Mode == "" && s.MemoryLoopConfig.ClaudeInjectionEnabled != nil {
		switch {
		case !cfg.Enabled:
			cfg.Mode = memoryLoopModeOff
		case *s.MemoryLoopConfig.ClaudeInjectionEnabled:
			cfg.Mode = memoryLoopModeAuto
		default:
			cfg.Mode = memoryLoopModeManual
		}
	}
	if s.MemoryLoopConfig.MaxInjected != 0 {
		cfg.MaxInjected = s.MemoryLoopConfig.MaxInjected
	}
	if s.MemoryLoopConfig.DefaultRefreshWindow != 0 {
		cfg.DefaultRefreshWindow = s.MemoryLoopConfig.DefaultRefreshWindow
	}
	if cfg.MaxInjected == 0 {
		cfg.MaxInjected = 3
	}
	if cfg.DefaultRefreshWindow == 0 {
		cfg.DefaultRefreshWindow = 20
	}
	return cfg
}

// GetCommitLinking returns the effective commit linking mode.
// Returns the explicit value if set, otherwise defaults to "prompt"
// to preserve existing user behavior.
func (s *EntireSettings) GetCommitLinking() string {
	if s.CommitLinking != "" {
		return s.CommitLinking
	}
	return CommitLinkingPrompt
}

// Load loads the Entire settings from .entire/settings.json,
// then applies any overrides from .entire/settings.local.json if it exists.
// Returns default settings if neither file exists.
// Works correctly from any subdirectory within the repository.
func Load(ctx context.Context) (*EntireSettings, error) {
	// Get absolute paths for settings files
	settingsFileAbs, err := paths.AbsPath(ctx, EntireSettingsFile)
	if err != nil {
		settingsFileAbs = EntireSettingsFile // Fallback to relative
	}
	localSettingsFileAbs, err := paths.AbsPath(ctx, EntireSettingsLocalFile)
	if err != nil {
		localSettingsFileAbs = EntireSettingsLocalFile // Fallback to relative
	}

	// Load base settings
	settings, err := loadFromFile(settingsFileAbs)
	if err != nil {
		return nil, fmt.Errorf("reading settings file: %w", err)
	}

	// Apply local overrides if they exist
	localData, err := os.ReadFile(localSettingsFileAbs) //nolint:gosec // path is from AbsPath or constant
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading local settings file: %w", err)
		}
		// Local file doesn't exist, continue without overrides
	} else {
		if err := mergeJSON(settings, localData); err != nil {
			return nil, fmt.Errorf("merging local settings: %w", err)
		}
	}

	if err := validateSettings(settings); err != nil {
		return nil, err
	}

	return settings, nil
}

// LoadFromFile loads settings from a specific file path without merging local overrides.
// Returns default settings if the file doesn't exist.
// Use this when you need to display individual settings files separately.
func LoadFromFile(filePath string) (*EntireSettings, error) {
	return loadFromFile(filePath)
}

// loadFromFile loads settings from a specific file path.
// Returns default settings if the file doesn't exist.
func loadFromFile(filePath string) (*EntireSettings, error) {
	settings := &EntireSettings{
		Enabled: true, // Default to enabled
	}

	data, err := os.ReadFile(filePath) //nolint:gosec // path is from caller
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return nil, fmt.Errorf("%w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(settings); err != nil {
		return nil, fmt.Errorf("parsing settings file: %w", err)
	}

	// Validate commit_linking if set
	if err := validateSettings(settings); err != nil {
		return nil, err
	}

	return settings, nil
}

// mergeJSON merges JSON data into existing settings.
// Only non-zero values from the JSON override existing settings.
func mergeJSON(settings *EntireSettings, data []byte) error {
	// First, validate that there are no unknown keys using strict decoding
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var temp EntireSettings
	if err := dec.Decode(&temp); err != nil {
		return fmt.Errorf("parsing JSON: %w", err)
	}

	// Parse into a map to check which fields are present
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing JSON: %w", err)
	}

	// Override enabled if present
	if enabledRaw, ok := raw["enabled"]; ok {
		var e bool
		if err := json.Unmarshal(enabledRaw, &e); err != nil {
			return fmt.Errorf("parsing enabled field: %w", err)
		}
		settings.Enabled = e
	}

	// Override local_dev if present
	if localDevRaw, ok := raw["local_dev"]; ok {
		var ld bool
		if err := json.Unmarshal(localDevRaw, &ld); err != nil {
			return fmt.Errorf("parsing local_dev field: %w", err)
		}
		settings.LocalDev = ld
	}

	// Override absolute_git_hook_path if present
	if ahpRaw, ok := raw["absolute_git_hook_path"]; ok {
		var ahp bool
		if err := json.Unmarshal(ahpRaw, &ahp); err != nil {
			return fmt.Errorf("parsing absolute_git_hook_path field: %w", err)
		}
		settings.AbsoluteGitHookPath = ahp
	}

	// Override log_level if present and non-empty
	if logLevelRaw, ok := raw["log_level"]; ok {
		var ll string
		if err := json.Unmarshal(logLevelRaw, &ll); err != nil {
			return fmt.Errorf("parsing log_level field: %w", err)
		}
		if ll != "" {
			settings.LogLevel = ll
		}
	}

	// Merge strategy_options if present
	if optionsRaw, ok := raw["strategy_options"]; ok {
		var opts map[string]any
		if err := json.Unmarshal(optionsRaw, &opts); err != nil {
			return fmt.Errorf("parsing strategy_options field: %w", err)
		}
		if settings.StrategyOptions == nil {
			settings.StrategyOptions = opts
		} else {
			for k, v := range opts {
				settings.StrategyOptions[k] = v
			}
		}
	}

	// Override telemetry if present
	if telemetryRaw, ok := raw["telemetry"]; ok {
		var t bool
		if err := json.Unmarshal(telemetryRaw, &t); err != nil {
			return fmt.Errorf("parsing telemetry field: %w", err)
		}
		settings.Telemetry = &t
	}

	// Merge redaction sub-fields if present (field-level, not wholesale replace).
	if redactionRaw, ok := raw["redaction"]; ok {
		if settings.Redaction == nil {
			settings.Redaction = &RedactionSettings{}
		}
		if err := mergeRedaction(settings.Redaction, redactionRaw); err != nil {
			return fmt.Errorf("parsing redaction field: %w", err)
		}
	}

	// Override commit_linking if present and non-empty
	if commitLinkingRaw, ok := raw["commit_linking"]; ok {
		var cl string
		if err := json.Unmarshal(commitLinkingRaw, &cl); err != nil {
			return fmt.Errorf("parsing commit_linking field: %w", err)
		}
		if cl != "" {
			switch cl {
			case CommitLinkingAlways, CommitLinkingPrompt:
				settings.CommitLinking = cl
			default:
				return fmt.Errorf("invalid commit_linking value %q: must be %q or %q", cl, CommitLinkingAlways, CommitLinkingPrompt)
			}
		}
	}

	// Override external_agents if present
	if externalAgentsRaw, ok := raw["external_agents"]; ok {
		var ea bool
		if err := json.Unmarshal(externalAgentsRaw, &ea); err != nil {
			return fmt.Errorf("parsing external_agents field: %w", err)
		}
		settings.ExternalAgents = ea
	}

	// Override evolve if present
	if evolveRaw, ok := raw["evolve"]; ok {
		var ev EvolveSettings
		if err := json.Unmarshal(evolveRaw, &ev); err != nil {
			return fmt.Errorf("parsing evolve field: %w", err)
		}
		settings.EvolveConfig = &ev
	}

	// Override memory_loop if present
	if memoryLoopRaw, ok := raw["memory_loop"]; ok {
		if settings.MemoryLoopConfig == nil {
			settings.MemoryLoopConfig = &MemoryLoopSettings{}
		}
		if err := mergeMemoryLoopSettings(settings.MemoryLoopConfig, memoryLoopRaw); err != nil {
			return fmt.Errorf("parsing memory_loop field: %w", err)
		}
	}

	return nil
}

func validateSettings(settings *EntireSettings) error {
	if settings.CommitLinking != "" && settings.CommitLinking != CommitLinkingAlways && settings.CommitLinking != CommitLinkingPrompt {
		return fmt.Errorf("invalid commit_linking value %q: must be %q or %q", settings.CommitLinking, CommitLinkingAlways, CommitLinkingPrompt)
	}
	if settings.MemoryLoopConfig == nil {
		return nil
	}
	if settings.MemoryLoopConfig.Mode != "" {
		switch settings.MemoryLoopConfig.Mode {
		case memoryLoopModeOff, memoryLoopModeManual, memoryLoopModeAuto:
		default:
			return fmt.Errorf(
				"invalid memory_loop.mode value %q: must be %q, %q, or %q",
				settings.MemoryLoopConfig.Mode,
				memoryLoopModeOff,
				memoryLoopModeManual,
				memoryLoopModeAuto,
			)
		}
	}
	if settings.MemoryLoopConfig.ActivationPolicy != "" {
		switch settings.MemoryLoopConfig.ActivationPolicy {
		case memoryLoopActivationPolicyReview, memoryLoopActivationPolicyAuto:
		default:
			return fmt.Errorf(
				"invalid memory_loop.activation_policy value %q: must be %q or %q",
				settings.MemoryLoopConfig.ActivationPolicy,
				memoryLoopActivationPolicyReview,
				memoryLoopActivationPolicyAuto,
			)
		}
	}
	return nil
}

func mergeMemoryLoopSettings(dst *MemoryLoopSettings, data json.RawMessage) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing memory_loop: %w", err)
	}

	if enabledRaw, ok := raw["enabled"]; ok {
		var enabled bool
		if err := json.Unmarshal(enabledRaw, &enabled); err != nil {
			return fmt.Errorf("parsing memory_loop.enabled: %w", err)
		}
		dst.Enabled = enabled
	}
	if modeRaw, ok := raw["mode"]; ok {
		var mode string
		if err := json.Unmarshal(modeRaw, &mode); err != nil {
			return fmt.Errorf("parsing memory_loop.mode: %w", err)
		}
		dst.Mode = mode
	}
	if policyRaw, ok := raw["activation_policy"]; ok {
		var policy string
		if err := json.Unmarshal(policyRaw, &policy); err != nil {
			return fmt.Errorf("parsing memory_loop.activation_policy: %w", err)
		}
		dst.ActivationPolicy = policy
	}
	if injectionRaw, ok := raw["claude_injection_enabled"]; ok {
		var enabled bool
		if err := json.Unmarshal(injectionRaw, &enabled); err != nil {
			return fmt.Errorf("parsing memory_loop.claude_injection_enabled: %w", err)
		}
		dst.ClaudeInjectionEnabled = &enabled
	}
	if maxInjectedRaw, ok := raw["max_injected"]; ok {
		var maxInjected int
		if err := json.Unmarshal(maxInjectedRaw, &maxInjected); err != nil {
			return fmt.Errorf("parsing memory_loop.max_injected: %w", err)
		}
		dst.MaxInjected = maxInjected
	}
	if refreshWindowRaw, ok := raw["default_refresh_window"]; ok {
		var refreshWindow int
		if err := json.Unmarshal(refreshWindowRaw, &refreshWindow); err != nil {
			return fmt.Errorf("parsing memory_loop.default_refresh_window: %w", err)
		}
		dst.DefaultRefreshWindow = refreshWindow
	}

	return nil
}

// mergeRedaction merges redaction overrides into existing RedactionSettings.
// Only fields present in the override JSON are applied.
func mergeRedaction(dst *RedactionSettings, data json.RawMessage) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing redaction: %w", err)
	}
	if piiRaw, ok := raw["pii"]; ok {
		if dst.PII == nil {
			dst.PII = &PIISettings{}
		}
		if err := mergePIISettings(dst.PII, piiRaw); err != nil {
			return err
		}
	}
	return nil
}

// mergePIISettings merges PII overrides into existing PIISettings.
// Only fields present in the override JSON are applied; missing fields
// are preserved from the base settings.
func mergePIISettings(dst *PIISettings, data json.RawMessage) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing pii: %w", err)
	}
	if v, ok := raw["enabled"]; ok {
		if err := json.Unmarshal(v, &dst.Enabled); err != nil {
			return fmt.Errorf("parsing pii.enabled: %w", err)
		}
	}
	if v, ok := raw["email"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return fmt.Errorf("parsing pii.email: %w", err)
		}
		dst.Email = &b
	}
	if v, ok := raw["phone"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return fmt.Errorf("parsing pii.phone: %w", err)
		}
		dst.Phone = &b
	}
	if v, ok := raw["address"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return fmt.Errorf("parsing pii.address: %w", err)
		}
		dst.Address = &b
	}
	if v, ok := raw["custom_patterns"]; ok {
		var cp map[string]string
		if err := json.Unmarshal(v, &cp); err != nil {
			return fmt.Errorf("parsing pii.custom_patterns: %w", err)
		}
		if dst.CustomPatterns == nil {
			dst.CustomPatterns = cp
		} else {
			for k, val := range cp {
				dst.CustomPatterns[k] = val
			}
		}
	}
	return nil
}

// IsSetUp returns true if Entire has been set up in the current repository.
// This checks if .entire/settings.json exists.
// Use this to avoid creating files/directories in repos where Entire was never enabled.
func IsSetUp(ctx context.Context) bool {
	settingsFileAbs, err := paths.AbsPath(ctx, EntireSettingsFile)
	if err != nil {
		return false
	}
	_, err = os.Stat(settingsFileAbs)
	return err == nil
}

// IsSetUpAny returns true if Entire has been set up in the current repository,
// checking both .entire/settings.json and .entire/settings.local.json.
// Use this to detect any prior setup, even if only local settings exist.
func IsSetUpAny(ctx context.Context) bool {
	if IsSetUp(ctx) {
		return true
	}
	localFileAbs, err := paths.AbsPath(ctx, EntireSettingsLocalFile)
	if err != nil {
		return false
	}
	_, err = os.Lstat(localFileAbs)
	return err == nil
}

// IsSetUpAndEnabled returns true if Entire is both set up and enabled.
// This checks if .entire/settings.json exists AND has enabled: true.
// Use this for hooks that should be no-ops when Entire is not active.
func IsSetUpAndEnabled(ctx context.Context) bool {
	if !IsSetUp(ctx) {
		return false
	}
	s, err := Load(ctx)
	if err != nil {
		return false
	}
	return s.Enabled
}

// IsCheckpointsV2Enabled checks if checkpoints v2 is enabled in settings.
// Returns false by default if settings cannot be loaded or the key is missing.
func IsCheckpointsV2Enabled(ctx context.Context) bool {
	settings, err := Load(ctx)
	if err != nil {
		return false
	}
	return settings.IsCheckpointsV2Enabled()
}

// IsSummarizeEnabled checks if auto-summarize is enabled in settings.
// Returns false by default if settings cannot be loaded or the key is missing.
func IsSummarizeEnabled(ctx context.Context) bool {
	settings, err := Load(ctx)
	if err != nil {
		return false
	}
	return settings.IsSummarizeEnabled()
}

// IsSummarizeEnabled checks if auto-summarize is enabled in this settings instance.
func (s *EntireSettings) IsSummarizeEnabled() bool {
	if s.StrategyOptions == nil {
		return false
	}
	summarizeOpts, ok := s.StrategyOptions["summarize"].(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := summarizeOpts["enabled"].(bool)
	if !ok {
		return false
	}
	return enabled
}

// CheckpointRemoteConfig holds the structured checkpoint remote configuration.
// Stored in strategy_options.checkpoint_remote as {"provider": "github", "repo": "org/repo"}.
type CheckpointRemoteConfig struct {
	Provider string // e.g., "github"
	Repo     string // e.g., "org/checkpoints-repo"
}

// Owner returns the owner portion of the repo field (before the slash).
// Returns empty string if the repo field doesn't contain a slash.
func (c *CheckpointRemoteConfig) Owner() string {
	parts := strings.SplitN(c.Repo, "/", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// GetCheckpointRemote returns the configured checkpoint remote.
// Expects a structured object: {"provider": "github", "repo": "org/repo"}.
// Returns nil if not configured, wrong type, or missing required fields.
func (s *EntireSettings) GetCheckpointRemote() *CheckpointRemoteConfig {
	if s.StrategyOptions == nil {
		return nil
	}
	val, ok := s.StrategyOptions["checkpoint_remote"]
	if !ok {
		return nil
	}
	m, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	provider, providerOK := m["provider"].(string)
	repo, repoOK := m["repo"].(string)
	if !providerOK || !repoOK || provider == "" || repo == "" {
		return nil
	}
	if !strings.Contains(repo, "/") {
		return nil
	}
	return &CheckpointRemoteConfig{Provider: provider, Repo: repo}
}

// IsCheckpointsV2Enabled checks if checkpoints v2 (dual-write to refs/entire/) is enabled.
// Returns false by default if the key is missing or not a bool.
func (s *EntireSettings) IsCheckpointsV2Enabled() bool {
	if s.StrategyOptions == nil {
		return false
	}
	val, ok := s.StrategyOptions["checkpoints_v2"].(bool)
	return ok && val
}

// IsPushSessionsDisabled checks if push_sessions is disabled in settings.
// Returns true if push_sessions is explicitly set to false.
func (s *EntireSettings) IsPushSessionsDisabled() bool {
	if s.StrategyOptions == nil {
		return false
	}
	val, exists := s.StrategyOptions["push_sessions"]
	if !exists {
		return false
	}
	if boolVal, ok := val.(bool); ok {
		return !boolVal // disabled = !push_sessions
	}
	return false
}

// IsExternalAgentsEnabled checks if external agent discovery is enabled in settings.
// Returns false by default if settings cannot be loaded or the key is missing.
func IsExternalAgentsEnabled(ctx context.Context) bool {
	s, err := Load(ctx)
	if err != nil {
		return false
	}
	return s.ExternalAgents
}

// Save saves the settings to .entire/settings.json.
func Save(ctx context.Context, settings *EntireSettings) error {
	return saveToFile(ctx, settings, EntireSettingsFile)
}

// SaveLocal saves the settings to .entire/settings.local.json.
func SaveLocal(ctx context.Context, settings *EntireSettings) error {
	return saveToFile(ctx, settings, EntireSettingsLocalFile)
}

// saveToFile saves settings to the specified file path.
func saveToFile(ctx context.Context, settings *EntireSettings, filePath string) error {
	// Get absolute path for the file
	filePathAbs, err := paths.AbsPath(ctx, filePath)
	if err != nil {
		filePathAbs = filePath // Fallback to relative
	}

	// Ensure directory exists
	dir := filepath.Dir(filePathAbs)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating settings directory: %w", err)
	}

	data, err := jsonutil.MarshalIndentWithNewline(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	//nolint:gosec // G306: settings file is config, not secrets; 0o644 is appropriate
	if err := os.WriteFile(filePathAbs, data, 0o644); err != nil {
		return fmt.Errorf("writing settings file: %w", err)
	}
	return nil
}
