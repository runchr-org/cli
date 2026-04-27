// This file implements LoadV2 and the legacy → v2 synthesizer.
//
// Loading rules:
//   - If .entire/settings.json declares "schema": 2, the file is parsed
//     directly as Settings. A local override file, if present, must also
//     declare "schema": 2 (mixed-shape settings are rejected with a
//     descriptive error).
//   - Otherwise, the legacy Load() path runs and its EntireSettings result
//     is mapped into Settings via synthesizeFromLegacy.
//
// The synthesizer is the single source of truth for legacy → v2 mapping
// and is the implementation that `entire migrate-config` will reuse to
// rewrite settings.json into the new shape.

package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/entireio/cli/cmd/entire/cli/paths"
)

// Backend type constants used in BackendConfig.Type.
const (
	BackendTypeV1    = "v1"
	BackendTypeV2    = "v2"
	BackendTypeGmeta = "gmeta"
)

// LoadV2 loads settings as the v2 shape. Files declaring "schema": 2 are
// parsed natively; older files are loaded via the legacy parser and
// synthesized. The local override file is merged the same way.
//
// Returns Settings with sensible defaults populated (Schema=2, Enabled=true)
// if neither settings file exists.
func LoadV2(ctx context.Context) (*Settings, error) {
	mainAbs, err := paths.AbsPath(ctx, EntireSettingsFile)
	if err != nil {
		mainAbs = EntireSettingsFile
	}
	localAbs, err := paths.AbsPath(ctx, EntireSettingsLocalFile)
	if err != nil {
		localAbs = EntireSettingsLocalFile
	}

	mainData, mainErr := readSettingsFileIfExists(mainAbs)
	if mainErr != nil {
		return nil, fmt.Errorf("reading settings file: %w", mainErr)
	}
	localData, localErr := readSettingsFileIfExists(localAbs)
	if localErr != nil {
		return nil, fmt.Errorf("reading local settings file: %w", localErr)
	}

	mainIsV2 := isSchemaV2(mainData)
	localIsV2 := isSchemaV2(localData)

	switch {
	case mainData == nil && localData == nil:
		return defaultSettings(), nil

	case mixedShapesRejected(mainData, localData, mainIsV2, localIsV2):
		return nil, errors.New("mixed schema versions in settings files; run `entire migrate-config` to convert all settings to schema 2")

	case mainIsV2 || localIsV2:
		return loadFromV2Files(mainData, localData)

	default:
		// Both files (if present) are legacy shape. Defer to the legacy
		// loader to handle merging, then synthesize into v2.
		legacy, err := loadMergedSettings(mainAbs, localAbs)
		if err != nil {
			return nil, err
		}
		return synthesizeFromLegacy(legacy), nil
	}
}

// LoadV2FromBytes parses a single settings document from raw bytes.
// Accepts either schema-v2 JSON or legacy JSON; the shape is auto-detected
// via the schema field. No local-override merge is performed.
func LoadV2FromBytes(data []byte) (*Settings, error) {
	if len(data) == 0 {
		return defaultSettings(), nil
	}
	if isSchemaV2(data) {
		return parseV2Bytes(data)
	}
	legacy, err := LoadFromBytes(data)
	if err != nil {
		return nil, err
	}
	return synthesizeFromLegacy(legacy), nil
}

// readSettingsFileIfExists returns the file contents, or (nil, nil) if the
// file does not exist. Other I/O errors propagate.
func readSettingsFileIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is from AbsPath or constant
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err //nolint:wrapcheck // caller wraps with file context
	}
	return data, nil
}

// isSchemaV2 reports whether a settings JSON document declares schema >= 2.
// Returns false for nil/empty input or invalid JSON; callers handle those
// cases explicitly downstream.
func isSchemaV2(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	var probe struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Schema >= CurrentSchemaVersion
}

// mixedShapesRejected reports the only configuration we treat as an error:
// exactly one of (main, local) declares schema 2 while the other is legacy.
// Two missing files or two same-shape files are both fine.
func mixedShapesRejected(mainData, localData []byte, mainIsV2, localIsV2 bool) bool {
	bothPresent := mainData != nil && localData != nil
	return bothPresent && (mainIsV2 != localIsV2)
}

// loadFromV2Files parses one or both files as schema-v2 JSON and applies the
// local override on top of the main settings. At least one of the inputs is
// guaranteed by the caller to be a v2 document.
func loadFromV2Files(mainData, localData []byte) (*Settings, error) {
	settings := defaultSettings()
	if mainData != nil {
		parsed, err := parseV2Bytes(mainData)
		if err != nil {
			return nil, fmt.Errorf("parsing settings file: %w", err)
		}
		settings = parsed
	}
	if localData != nil {
		if err := mergeV2Override(settings, localData); err != nil {
			return nil, fmt.Errorf("merging local settings: %w", err)
		}
	}
	return settings, nil
}

// parseV2Bytes parses a single schema-v2 JSON document into Settings.
// Strict decoding rejects unknown fields so typos become loud failures.
func parseV2Bytes(data []byte) (*Settings, error) {
	settings := defaultSettings()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(settings); err != nil {
		return nil, fmt.Errorf("parsing settings: %w", err)
	}
	return settings, nil
}

// mergeV2Override applies local overrides on top of an existing Settings
// value. Field-level merge semantics: only fields explicitly present in
// the override JSON change the base.
//
// For now this delegates to a full reparse of the local file and replaces
// top-level groups wholesale. That is enough for the local-override use
// cases in the codebase today (toggling log level, enabling local_dev).
// More granular per-field merging can be added when a real call site needs
// it; the legacy code only got more granular for redaction/PII fields,
// and we will revisit that if the ergonomics become a problem.
func mergeV2Override(base *Settings, data []byte) error {
	override, err := parseV2Bytes(data)
	if err != nil {
		return err
	}

	var presence map[string]json.RawMessage
	if err := json.Unmarshal(data, &presence); err != nil {
		return fmt.Errorf("parsing local settings: %w", err)
	}

	if _, ok := presence["enabled"]; ok {
		base.Enabled = override.Enabled
	}
	if _, ok := presence["local_dev"]; ok {
		base.LocalDev = override.LocalDev
	}
	if _, ok := presence["logging"]; ok {
		base.Logging = override.Logging
	}
	if _, ok := presence["checkpoints"]; ok {
		base.Checkpoints = override.Checkpoints
	}
	if _, ok := presence["hooks"]; ok {
		base.Hooks = override.Hooks
	}
	if _, ok := presence["features"]; ok {
		base.Features = override.Features
	}
	if _, ok := presence["redaction"]; ok {
		base.Redaction = override.Redaction
	}
	if _, ok := presence["telemetry"]; ok {
		base.Telemetry = override.Telemetry
	}
	if _, ok := presence["summary_generation"]; ok {
		base.SummaryGeneration = override.SummaryGeneration
	}
	return nil
}

// defaultSettings returns the zero-value Settings with documented defaults
// applied (Schema = 2, Enabled = true). Callers should treat this as the
// starting point before parsing a file or merging overrides.
func defaultSettings() *Settings {
	return &Settings{
		Schema:  CurrentSchemaVersion,
		Enabled: true,
	}
}

// synthesizeFromLegacy maps an EntireSettings into a Settings, encoding the
// legacy → v2 migration. This is the single source of truth for the
// translation and is reused by `entire migrate-config`.
//
// Mappings:
//   - strategy_options.checkpoints_version=2 OR checkpoints_v2=true
//     → Checkpoints.Primary.Type = "v2"
//   - otherwise → Checkpoints.Primary.Type = "v1"
//   - strategy_options.gmeta=true → append {type: "gmeta"} to Mirrors
//   - strategy_options.checkpoint_remote → Checkpoints.Remote
//   - strategy_options.full_transcript_generation_retention_days
//     → Checkpoints.FullTranscriptRetentionDays
//   - strategy_options.filtered_fetches → Checkpoints.FilteredFetches
//   - strategy_options.push_sessions (explicit) → Checkpoints.PushSessions
//   - strategy_options.summarize.enabled → Features.Summarize
//   - log_level → Logging.Level
//   - sign_checkpoint_commits → Checkpoints.SignCommits
//   - commit_linking → Hooks.CommitLinking
//   - absolute_git_hook_path → Hooks.AbsoluteGitHookPath
//   - external_agents → Features.ExternalAgents
//   - vercel → Features.Vercel
//   - summary_generation.{provider,model} + summary_timeout_seconds
//     → SummaryGeneration.{Provider,Model,TimeoutSeconds}
//
// Dropped (no v2 destination):
//   - push_v2_refs (was a dual-write transition knob; v2 ref pushes are now
//     a property of the v2 backend's CheckpointSyncer, not a settings flag)
//   - strategy (deprecated tolerator field)
func synthesizeFromLegacy(s *EntireSettings) *Settings {
	if s == nil {
		return defaultSettings()
	}

	out := &Settings{
		Schema:   CurrentSchemaVersion,
		Enabled:  s.Enabled,
		LocalDev: s.LocalDev,
		Logging: LoggingConfig{
			Level: s.LogLevel,
		},
		Hooks: HooksConfig{
			CommitLinking:       s.CommitLinking,
			AbsoluteGitHookPath: s.AbsoluteGitHookPath,
		},
		Features: FeaturesConfig{
			Summarize:      s.IsSummarizeEnabled(),
			ExternalAgents: s.ExternalAgents,
			Vercel:         s.Vercel,
		},
		Redaction: s.Redaction,
		Telemetry: s.Telemetry,
	}

	out.Checkpoints = synthesizeCheckpointsConfig(s)

	if hasSummaryGeneration(s) {
		out.SummaryGeneration = &SummaryGenerationConfig{
			TimeoutSeconds: s.SummaryTimeoutSeconds,
		}
		if s.SummaryGeneration != nil {
			out.SummaryGeneration.Provider = s.SummaryGeneration.Provider
			out.SummaryGeneration.Model = s.SummaryGeneration.Model
		}
	}

	return out
}

// synthesizeCheckpointsConfig builds the v2 Checkpoints group from the
// legacy EntireSettings. Split out for readability since the mapping pulls
// from both top-level fields and the strategy_options soup.
func synthesizeCheckpointsConfig(s *EntireSettings) CheckpointsConfig {
	cfg := CheckpointsConfig{
		Primary:                     BackendConfig{Type: legacyPrimaryBackend(s)},
		Remote:                      s.GetCheckpointRemote(),
		FullTranscriptRetentionDays: legacyFullTranscriptRetention(s),
		SignCommits:                 s.SignCheckpointCommits,
		FilteredFetches:             s.IsFilteredFetchesEnabled(),
		PushSessions:                legacyPushSessions(s),
	}

	if legacyGmetaEnabled(s) {
		cfg.Mirrors = append(cfg.Mirrors, BackendConfig{Type: BackendTypeGmeta})
	}

	return cfg
}

// legacyPrimaryBackend returns "v2" if either checkpoints_version or
// checkpoints_v2 enables v2 in the legacy settings, "v1" otherwise.
func legacyPrimaryBackend(s *EntireSettings) string {
	if s.IsCheckpointsV2Enabled() {
		return BackendTypeV2
	}
	return BackendTypeV1
}

// legacyGmetaEnabled checks the legacy strategy_options.gmeta flag. The
// flag itself does not exist on this branch yet (it ships with the gmeta
// mainport), but the synthesizer recognizes it ahead of time so settings
// files written by that branch round-trip cleanly through Phase 0.
func legacyGmetaEnabled(s *EntireSettings) bool {
	if s.StrategyOptions == nil {
		return false
	}
	val, ok := s.StrategyOptions["gmeta"].(bool)
	return ok && val
}

// legacyFullTranscriptRetention returns the configured retention only when
// the legacy key is explicitly set; the legacy accessor returns a default
// when unset, which we suppress here so the v2 file stays minimal.
func legacyFullTranscriptRetention(s *EntireSettings) int {
	if s.StrategyOptions == nil {
		return 0
	}
	if _, ok := s.StrategyOptions["full_transcript_generation_retention_days"]; !ok {
		return 0
	}
	return s.GetFullTranscriptGenerationRetentionDays()
}

// legacyPushSessions extracts the explicit push_sessions boolean if set;
// otherwise returns nil so the v2 file stays minimal. The legacy semantics
// were "explicit false disables, otherwise default behavior", which we
// preserve by using a *bool here.
func legacyPushSessions(s *EntireSettings) *bool {
	if s.StrategyOptions == nil {
		return nil
	}
	val, ok := s.StrategyOptions["push_sessions"]
	if !ok {
		return nil
	}
	b, ok := val.(bool)
	if !ok {
		return nil
	}
	return &b
}

// hasSummaryGeneration reports whether any summary-generation field on the
// legacy settings is non-default. Used to decide whether to allocate the
// v2 SummaryGenerationConfig at all (vs. leaving it nil).
func hasSummaryGeneration(s *EntireSettings) bool {
	if s.SummaryGeneration != nil && (s.SummaryGeneration.Provider != "" || s.SummaryGeneration.Model != "") {
		return true
	}
	return s.SummaryTimeoutSeconds > 0
}
